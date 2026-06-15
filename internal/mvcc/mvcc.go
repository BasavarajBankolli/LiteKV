// Package mvcc implements Multi-Version Concurrency Control.
// Every write gets a monotonically increasing version (timestamp).
// Readers snapshot the current version and see a consistent view
// without blocking writers — enabling lock-free concurrent reads.
package mvcc

import (
	"sync/atomic"
)

// Clock is a logical clock for assigning MVCC versions.
// In production you'd use a hybrid logical clock (HLC).
type Clock struct {
	version uint64
}

// NewClock creates a clock starting at the given version (use 0 for fresh DB).
func NewClock(startVersion uint64) *Clock {
	return &Clock{version: startVersion}
}

// Tick increments and returns the next version. Thread-safe.
func (c *Clock) Tick() uint64 {
	return atomic.AddUint64(&c.version, 1)
}

// Current returns the current version without incrementing.
func (c *Clock) Current() uint64 {
	return atomic.LoadUint64(&c.version)
}

// Snapshot captures a read-consistent point in time.
// Any reads using this snapshot will ignore writes with version > SnapshotVersion.
type Snapshot struct {
	Version uint64
}

// NewSnapshot captures the current logical time.
func (c *Clock) NewSnapshot() Snapshot {
	return Snapshot{Version: c.Current()}
}

// Transaction tracks a pending set of writes.
type Transaction struct {
	ID      uint64
	clock   *Clock
	version uint64 // assigned at commit time
	writes  []Write
	aborted bool
}

// Write is a pending key-value operation within a transaction.
type Write struct {
	Key     string
	Value   []byte
	Deleted bool
}

// TxnManager manages active transactions.
type TxnManager struct {
	clock  *Clock
	nextID uint64
}

// NewTxnManager creates a transaction manager.
func NewTxnManager(clock *Clock) *TxnManager {
	return &TxnManager{clock: clock}
}

// Begin starts a new read-write transaction.
func (tm *TxnManager) Begin() *Transaction {
	id := atomic.AddUint64(&tm.nextID, 1)
	return &Transaction{
		ID:    id,
		clock: tm.clock,
	}
}

// Put stages a write in the transaction buffer.
func (t *Transaction) Put(key string, value []byte) {
	t.writes = append(t.writes, Write{Key: key, Value: value})
}

// Delete stages a delete (tombstone) in the transaction buffer.
func (t *Transaction) Delete(key string) {
	t.writes = append(t.writes, Write{Key: key, Deleted: true})
}

// Commit assigns a version and returns the staged writes for application.
// The caller is responsible for applying these to the MemTable + WAL.
func (t *Transaction) Commit() ([]Write, uint64) {
	t.version = t.clock.Tick()
	return t.writes, t.version
}

// Abort discards all staged writes.
func (t *Transaction) Abort() {
	t.writes = nil
	t.aborted = true
}

// IsAborted reports whether the transaction was aborted.
func (t *Transaction) IsAborted() bool {
	return t.aborted
}

// Writes returns the staged writes (for inspection before commit).
func (t *Transaction) Writes() []Write {
	return t.writes
}
