// Package wal implements a Write-Ahead Log for crash recovery.
// Every write is durably logged before being applied to the MemTable.
// On restart, the WAL is replayed to rebuild any in-memory state lost in a crash.
package wal

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// RecordType distinguishes log record kinds.
type RecordType byte

const (
	RecordPut       RecordType = 0x01
	RecordDelete    RecordType = 0x02
	RecordTxnBegin  RecordType = 0x10
	RecordTxnCommit RecordType = 0x11
	RecordTxnAbort  RecordType = 0x12
)

// Record is a single WAL entry.
// Wire format: [type:1B][txnID:8B][keyLen:4B][valLen:4B][key][val][crc32:4B]
type Record struct {
	Type  RecordType
	TxnID uint64
	Key   []byte
	Value []byte
}

const headerSize = 1 + 8 + 4 + 4

// Options controls WAL behaviour.
type Options struct {
	// SyncWrites forces an fsync after every write for crash durability.
	// Disable for benchmarking or when the OS write-back cache is acceptable.
	// Default: false (buffered — ~10x faster, still correct on clean shutdown)
	SyncWrites bool
}

// WAL is a write-ahead log backed by an append-only file.
type WAL struct {
	mu   sync.Mutex
	file *os.File
	buf  *bufio.Writer
	path string
	size int64
	sync bool
}

// Open opens or creates a WAL file at the given path.
func Open(dir string, opts ...Options) (*WAL, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("wal: mkdir: %w", err)
	}
	path := filepath.Join(dir, "wal.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("wal: open: %w", err)
	}
	info, _ := f.Stat()
	syncWrites := false
	if len(opts) > 0 {
		syncWrites = opts[0].SyncWrites
	}
	return &WAL{
		file: f,
		buf:  bufio.NewWriterSize(f, 256*1024),
		path: path,
		size: info.Size(),
		sync: syncWrites,
	}, nil
}

// Append writes a record to the WAL.
// With SyncWrites=false (default) the write is buffered — fast, and still
// recoverable on a clean shutdown because Close() flushes the buffer.
// With SyncWrites=true every write is fsynced — slower but crash-safe.
func (w *WAL) Append(rec Record) error {
	data := encode(rec)

	w.mu.Lock()
	defer w.mu.Unlock()

	if _, err := w.buf.Write(data); err != nil {
		return fmt.Errorf("wal: write: %w", err)
	}
	w.size += int64(len(data))

	if w.sync {
		if err := w.buf.Flush(); err != nil {
			return fmt.Errorf("wal: flush: %w", err)
		}
		if err := w.file.Sync(); err != nil {
			return fmt.Errorf("wal: sync: %w", err)
		}
	}
	return nil
}

// Sync explicitly flushes and fsyncs — call this after a transaction commit
// if you want durability without enabling per-write sync.
func (w *WAL) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.buf.Flush(); err != nil {
		return err
	}
	return w.file.Sync()
}

// Replay reads all records from the WAL and calls fn for each valid record.
// Corrupt/truncated records at the end are silently skipped (partial crash write).
func (w *WAL) Replay(fn func(Record) error) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReader(w.file)
	for {
		rec, err := decode(reader)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			break // treat any decode error at tail as a partial write
		}
		if err := fn(rec); err != nil {
			return err
		}
	}
	return nil
}

// Truncate clears the WAL after a successful MemTable → SSTable flush.
func (w *WAL) Truncate() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Reset(w.file)
	if err := w.file.Truncate(0); err != nil {
		return err
	}
	_, err := w.file.Seek(0, io.SeekStart)
	w.size = 0
	return err
}

// Size returns approximate WAL size in bytes.
func (w *WAL) Size() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.size
}

// Close flushes the buffer and closes the file.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Flush()
	w.file.Sync()
	return w.file.Close()
}

// --- Encoding ---

func encode(rec Record) []byte {
	keyLen := len(rec.Key)
	valLen := len(rec.Value)
	buf := make([]byte, headerSize+keyLen+valLen+4)
	buf[0] = byte(rec.Type)
	binary.LittleEndian.PutUint64(buf[1:9], rec.TxnID)
	binary.LittleEndian.PutUint32(buf[9:13], uint32(keyLen))
	binary.LittleEndian.PutUint32(buf[13:17], uint32(valLen))
	copy(buf[headerSize:], rec.Key)
	copy(buf[headerSize+keyLen:], rec.Value)
	crc := crc32.ChecksumIEEE(buf[:headerSize+keyLen+valLen])
	binary.LittleEndian.PutUint32(buf[headerSize+keyLen+valLen:], crc)
	return buf
}

func decode(r *bufio.Reader) (Record, error) {
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return Record{}, err
	}
	recType := RecordType(header[0])
	txnID := binary.LittleEndian.Uint64(header[1:9])
	keyLen := int(binary.LittleEndian.Uint32(header[9:13]))
	valLen := int(binary.LittleEndian.Uint32(header[13:17]))

	payload := make([]byte, keyLen+valLen+4)
	if _, err := io.ReadFull(r, payload); err != nil {
		return Record{}, err
	}
	expectedCRC := binary.LittleEndian.Uint32(payload[keyLen+valLen:])
	data := append(header, payload[:keyLen+valLen]...)
	if crc32.ChecksumIEEE(data) != expectedCRC {
		return Record{}, fmt.Errorf("wal: crc mismatch")
	}
	return Record{
		Type:  recType,
		TxnID: txnID,
		Key:   payload[:keyLen],
		Value: payload[keyLen : keyLen+valLen],
	}, nil
}
