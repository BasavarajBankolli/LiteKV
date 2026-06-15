// Package sstable implements Sorted String Tables — the on-disk format of the LSM tree.
// Each SSTable is an immutable, sorted sequence of key-value pairs.
//
// File layout:
//
//	[Data Block 0][Data Block 1]...[Index Block][Bloom Filter][Footer]
//
// Footer (last 32 bytes):
//
//	[indexOffset: 8B][indexLen: 8B][bloomOffset: 8B][bloomLen: 8B]
package sstable

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/BasavarajBankolli/litekv/internal/bloom"
	"github.com/BasavarajBankolli/litekv/internal/memtable"
)

const footerSize = 32
const blockSize = 4 * 1024 // 4 KB data blocks

// Entry is one record stored in the SSTable.
type Entry struct {
	Key     string
	Value   []byte
	Deleted bool
	Version uint64
}

// IndexEntry maps a key to its offset+length in the data section.
type IndexEntry struct {
	Key    string
	Offset int64
	Length int32
}

// SSTable is an opened, immutable on-disk sorted table.
type SSTable struct {
	path   string
	file   *os.File
	index  []IndexEntry
	filter *bloom.Filter
	Level  int
	// Key range for fast level-search pruning
	MinKey string
	MaxKey string
}

// Writer builds an SSTable from a sorted stream of entries.
type Writer struct {
	file   *os.File
	writer *bufio.Writer
	index  []IndexEntry
	filter *bloom.Filter
	offset int64
}

// NewWriter opens path for writing a new SSTable.
func NewWriter(path string, expectedEntries int) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &Writer{
		file:   f,
		writer: bufio.NewWriterSize(f, 64*1024),
		filter: bloom.New(expectedEntries, 0.01), // 1% FP rate
	}, nil
}

// Append writes one entry to the SSTable.
func (w *Writer) Append(e memtable.Entry) error {
	startOffset := w.offset

	// Encode: [keyLen:4][key][valLen:4 (negative = tombstone)][val][version:8]
	keyBytes := []byte(e.Key)
	rec := encodeRecord(keyBytes, e.Value, e.Deleted, e.Version)

	if _, err := w.writer.Write(rec); err != nil {
		return err
	}
	w.offset += int64(len(rec))

	// Add to sparse index and Bloom filter
	w.index = append(w.index, IndexEntry{
		Key:    e.Key,
		Offset: startOffset,
		Length: int32(len(rec)),
	})
	w.filter.Add(keyBytes)
	return nil
}

// Close finalises the SSTable: writes index, bloom filter, and footer.
func (w *Writer) Close() error {
	if err := w.writer.Flush(); err != nil {
		return err
	}

	// --- Write Index Block ---
	indexOffset := w.offset
	for _, ie := range w.index {
		entry := encodeIndexEntry(ie)
		if _, err := w.file.Write(entry); err != nil {
			return err
		}
		w.offset += int64(len(entry))
	}
	indexLen := w.offset - indexOffset

	// --- Write Bloom Filter ---
	bloomOffset := w.offset
	bloomData := w.filter.Encode()
	if _, err := w.file.Write(bloomData); err != nil {
		return err
	}
	bloomLen := int64(len(bloomData))

	// --- Write Footer ---
	footer := make([]byte, footerSize)
	binary.LittleEndian.PutUint64(footer[0:8], uint64(indexOffset))
	binary.LittleEndian.PutUint64(footer[8:16], uint64(indexLen))
	binary.LittleEndian.PutUint64(footer[16:24], uint64(bloomOffset))
	binary.LittleEndian.PutUint64(footer[24:32], uint64(bloomLen))
	if _, err := w.file.Write(footer); err != nil {
		return err
	}

	return w.file.Close()
}

// Open reads an existing SSTable, loading its index and bloom filter into memory.
func Open(path string, level int) (*SSTable, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("sstable: open %s: %w", path, err)
	}

	// Read footer
	info, _ := f.Stat()
	footerBuf := make([]byte, footerSize)
	if _, err := f.ReadAt(footerBuf, info.Size()-footerSize); err != nil {
		return nil, fmt.Errorf("sstable: read footer: %w", err)
	}

	indexOffset := int64(binary.LittleEndian.Uint64(footerBuf[0:8]))
	indexLen := int64(binary.LittleEndian.Uint64(footerBuf[8:16]))
	bloomOffset := int64(binary.LittleEndian.Uint64(footerBuf[16:24]))
	bloomLen := int64(binary.LittleEndian.Uint64(footerBuf[24:32]))

	// Load index
	indexData := make([]byte, indexLen)
	if _, err := f.ReadAt(indexData, indexOffset); err != nil {
		return nil, fmt.Errorf("sstable: read index: %w", err)
	}
	index, err := decodeIndex(indexData)
	if err != nil {
		return nil, err
	}

	// Load bloom filter
	bloomData := make([]byte, bloomLen)
	if _, err := f.ReadAt(bloomData, bloomOffset); err != nil {
		return nil, fmt.Errorf("sstable: read bloom: %w", err)
	}
	filter := bloom.Decode(bloomData)

	sst := &SSTable{
		path:   path,
		file:   f,
		index:  index,
		filter: filter,
		Level:  level,
	}
	if len(index) > 0 {
		sst.MinKey = index[0].Key
		sst.MaxKey = index[len(index)-1].Key
	}
	return sst, nil
}

// Get looks up a key. Returns (entry, true) if found.
// First checks the Bloom filter to skip the disk read on definite misses.
func (s *SSTable) Get(key string) (Entry, bool, error) {
	// Bloom filter check — eliminates unnecessary disk reads
	if !s.filter.MightContain([]byte(key)) {
		return Entry{}, false, nil // Definitely not here
	}

	// Binary search the sparse index
	pos := s.searchIndex(key)
	if pos < 0 {
		return Entry{}, false, nil
	}

	// Read the record from disk
	ie := s.index[pos]
	recBuf := make([]byte, ie.Length)
	if _, err := s.file.ReadAt(recBuf, ie.Offset); err != nil {
		return Entry{}, false, fmt.Errorf("sstable: read record: %w", err)
	}

	e, err := decodeRecord(recBuf)
	if err != nil {
		return Entry{}, false, err
	}
	if e.Key != key {
		return Entry{}, false, nil
	}
	return e, true, nil
}

// Iterator returns all entries in sorted order. Used for compaction.
func (s *SSTable) Iterator() (*Iterator, error) {
	f, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	// Read data section only (before index)
	var dataEnd int64
	if len(s.index) > 0 {
		last := s.index[len(s.index)-1]
		dataEnd = last.Offset + int64(last.Length)
	}
	return &Iterator{file: f, reader: bufio.NewReader(f), dataEnd: dataEnd}, nil
}

// Close releases the file handle.
func (s *SSTable) Close() error {
	return s.file.Close()
}

// Path returns the file path.
func (s *SSTable) Path() string {
	return s.path
}

// searchIndex finds the index entry matching key using binary search.
func (s *SSTable) searchIndex(key string) int {
	lo, hi := 0, len(s.index)-1
	result := -1
	for lo <= hi {
		mid := (lo + hi) / 2
		if s.index[mid].Key == key {
			return mid
		} else if s.index[mid].Key < key {
			result = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return result
}

// --- Iterator ---

// Iterator reads SSTable entries sequentially for compaction merges.
type Iterator struct {
	file    *os.File
	reader  *bufio.Reader
	dataEnd int64
	pos     int64
	current Entry
	done    bool
	err     error
}

func (it *Iterator) Next() bool {
	if it.done || it.pos >= it.dataEnd {
		it.done = true
		return false
	}
	// Read key length to know record size
	var keyLen uint32
	if err := binary.Read(it.reader, binary.LittleEndian, &keyLen); err != nil {
		if err == io.EOF {
			it.done = true
			return false
		}
		it.err = err
		return false
	}
	it.pos += 4

	key := make([]byte, keyLen)
	if _, err := io.ReadFull(it.reader, key); err != nil {
		it.err = err
		return false
	}
	it.pos += int64(keyLen)

	var valLen int32
	if err := binary.Read(it.reader, binary.LittleEndian, &valLen); err != nil {
		it.err = err
		return false
	}
	it.pos += 4

	deleted := valLen < 0
	absLen := valLen
	if absLen < 0 {
		absLen = -absLen
	}

	val := make([]byte, absLen)
	if absLen > 0 {
		if _, err := io.ReadFull(it.reader, val); err != nil {
			it.err = err
			return false
		}
	}
	it.pos += int64(absLen)

	var version uint64
	if err := binary.Read(it.reader, binary.LittleEndian, &version); err != nil {
		it.err = err
		return false
	}
	it.pos += 8

	it.current = Entry{Key: string(key), Value: val, Deleted: deleted, Version: version}
	return true
}

func (it *Iterator) Entry() Entry { return it.current }
func (it *Iterator) Err() error   { return it.err }
func (it *Iterator) Close() error { return it.file.Close() }

// --- Codec helpers ---

func encodeRecord(key, val []byte, deleted bool, version uint64) []byte {
	valLen := int32(len(val))
	if deleted {
		valLen = -1
	}
	size := 4 + len(key) + 4 + len(val) + 8
	buf := make([]byte, size)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(key)))
	copy(buf[4:], key)
	off := 4 + len(key)
	binary.LittleEndian.PutUint32(buf[off:off+4], uint32(valLen))
	if !deleted && len(val) > 0 {
		copy(buf[off+4:], val)
	}
	off += 4 + len(val)
	if deleted {
		off = 4 + len(key) + 4
	}
	binary.LittleEndian.PutUint64(buf[off:off+8], version)
	return buf
}

func decodeRecord(buf []byte) (Entry, error) {
	if len(buf) < 8 {
		return Entry{}, fmt.Errorf("sstable: record too short")
	}
	keyLen := int(binary.LittleEndian.Uint32(buf[0:4]))
	key := string(buf[4 : 4+keyLen])
	off := 4 + keyLen
	rawValLen := int32(binary.LittleEndian.Uint32(buf[off : off+4]))
	deleted := rawValLen < 0
	absValLen := rawValLen
	if absValLen < 0 {
		absValLen = -absValLen
	}
	off += 4
	val := buf[off : off+int(absValLen)]
	off += int(absValLen)
	version := binary.LittleEndian.Uint64(buf[off : off+8])
	return Entry{Key: key, Value: val, Deleted: deleted, Version: version}, nil
}

func encodeIndexEntry(ie IndexEntry) []byte {
	keyBytes := []byte(ie.Key)
	buf := make([]byte, 4+len(keyBytes)+8+4)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(keyBytes)))
	copy(buf[4:], keyBytes)
	off := 4 + len(keyBytes)
	binary.LittleEndian.PutUint64(buf[off:off+8], uint64(ie.Offset))
	binary.LittleEndian.PutUint32(buf[off+8:off+12], uint32(ie.Length))
	return buf
}

func decodeIndex(data []byte) ([]IndexEntry, error) {
	var entries []IndexEntry
	off := 0
	for off < len(data) {
		if off+4 > len(data) {
			break
		}
		keyLen := int(binary.LittleEndian.Uint32(data[off : off+4]))
		off += 4
		if off+keyLen+12 > len(data) {
			break
		}
		key := string(data[off : off+keyLen])
		off += keyLen
		offset := int64(binary.LittleEndian.Uint64(data[off : off+8]))
		length := int32(binary.LittleEndian.Uint32(data[off+8 : off+12]))
		off += 12
		entries = append(entries, IndexEntry{Key: key, Offset: offset, Length: length})
	}
	return entries, nil
}
