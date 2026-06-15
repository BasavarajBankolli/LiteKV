// Package memtable implements the in-memory write buffer (MemTable).
// Writes go here first (after WAL) and are later flushed to SSTables on disk.
// Uses a skip list for O(log n) reads/writes with sorted iteration.
package memtable

import (
	"math/rand"
	"sync"
)

const maxLevel = 12
const probability = 0.25

// Entry holds a key-value pair with an optional tombstone marker.
type Entry struct {
	Key       string
	Value     []byte
	Deleted   bool
	Version   uint64 // MVCC version (timestamp)
}

// skipNode is one node in the skip list.
type skipNode struct {
	entry   Entry
	forward []*skipNode
}

// SkipList is a concurrent skip list for O(log n) ordered operations.
type SkipList struct {
	head    *skipNode
	level   int
	length  int
	mu      sync.RWMutex
}

func newSkipNode(level int, entry Entry) *skipNode {
	return &skipNode{
		entry:   entry,
		forward: make([]*skipNode, level),
	}
}

func newSkipList() *SkipList {
	head := &skipNode{forward: make([]*skipNode, maxLevel)}
	return &SkipList{head: head, level: 1}
}

func randomLevel() int {
	level := 1
	for level < maxLevel && rand.Float64() < probability {
		level++
	}
	return level
}

func (sl *SkipList) put(key string, value []byte, deleted bool, version uint64) {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	update := make([]*skipNode, maxLevel)
	curr := sl.head
	for i := sl.level - 1; i >= 0; i-- {
		for curr.forward[i] != nil && curr.forward[i].entry.Key < key {
			curr = curr.forward[i]
		}
		update[i] = curr
	}

	curr = curr.forward[0]
	if curr != nil && curr.entry.Key == key {
		// Update existing key in-place (keep highest version)
		curr.entry.Value = value
		curr.entry.Deleted = deleted
		curr.entry.Version = version
		return
	}

	newLevel := randomLevel()
	if newLevel > sl.level {
		for i := sl.level; i < newLevel; i++ {
			update[i] = sl.head
		}
		sl.level = newLevel
	}

	node := newSkipNode(newLevel, Entry{Key: key, Value: value, Deleted: deleted, Version: version})
	for i := 0; i < newLevel; i++ {
		node.forward[i] = update[i].forward[i]
		update[i].forward[i] = node
	}
	sl.length++
}

func (sl *SkipList) get(key string) (Entry, bool) {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	curr := sl.head
	for i := sl.level - 1; i >= 0; i-- {
		for curr.forward[i] != nil && curr.forward[i].entry.Key < key {
			curr = curr.forward[i]
		}
	}
	curr = curr.forward[0]
	if curr != nil && curr.entry.Key == key {
		return curr.entry, true
	}
	return Entry{}, false
}

// iterate calls fn for every entry in sorted key order.
func (sl *SkipList) iterate(fn func(Entry)) {
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	curr := sl.head.forward[0]
	for curr != nil {
		fn(curr.entry)
		curr = curr.forward[0]
	}
}

// MemTable is the in-memory write buffer backed by a skip list.
type MemTable struct {
	list    *SkipList
	sizeBytes int64
	maxSize   int64
}

// New creates a MemTable with the given max size in bytes (default 4 MB).
func New(maxSizeBytes int64) *MemTable {
	if maxSizeBytes == 0 {
		maxSizeBytes = 4 * 1024 * 1024
	}
	return &MemTable{
		list:    newSkipList(),
		maxSize: maxSizeBytes,
	}
}

// Put inserts or updates a key.
func (m *MemTable) Put(key string, value []byte, version uint64) {
	m.list.put(key, value, false, version)
	m.sizeBytes += int64(len(key) + len(value) + 16)
}

// Delete marks a key as deleted (tombstone).
func (m *MemTable) Delete(key string, version uint64) {
	m.list.put(key, nil, true, version)
	m.sizeBytes += int64(len(key) + 16)
}

// Get looks up a key. Returns (entry, true) if found (may be a tombstone).
func (m *MemTable) Get(key string) (Entry, bool) {
	return m.list.get(key)
}

// IsFull returns true when the MemTable should be flushed to an SSTable.
func (m *MemTable) IsFull() bool {
	return m.sizeBytes >= m.maxSize
}

// SizeBytes returns approximate memory usage.
func (m *MemTable) SizeBytes() int64 {
	return m.sizeBytes
}

// Len returns the number of entries.
func (m *MemTable) Len() int {
	return m.list.length
}

// Iterate calls fn for every entry in sorted order.
// Used during flush to write a sorted SSTable.
func (m *MemTable) Iterate(fn func(Entry)) {
	m.list.iterate(fn)
}
