// Package engine is the heart of LiteKV: an LSM Tree storage engine.
//
// Write path:
//  1. Append record to WAL (durable)
//  2. Insert into active MemTable
//  3. When MemTable is full → flush to Level-0 SSTable, truncate WAL
//
// Read path:
//  1. Check active MemTable
//  2. Check immutable MemTable (being flushed)
//  3. For each level (L0 → Lmax): check Bloom filter, then SSTable index, then disk
//
// Compaction:
//   - L0 → L1: triggered when L0 has ≥4 SSTables
//   - Merge-sort overlapping SSTables, drop tombstones and old versions
package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/BasavarajBankolli/litekv/internal/memtable"
	"github.com/BasavarajBankolli/litekv/internal/mvcc"
	"github.com/BasavarajBankolli/litekv/internal/sstable"
	"github.com/BasavarajBankolli/litekv/internal/wal"
)

const (
	maxMemTableSize    = 4 * 1024 * 1024 // 4 MB
	l0CompactionTrigger = 4               // compact when L0 has this many SSTables
	maxLevels          = 7
)

// Config holds tunable engine parameters.
type Config struct {
	Dir            string
	MaxMemTableMB  int
	MaxMemTableKB  int
	L0TriggerCount int
}

// Engine is the main LSM storage engine.
type Engine struct {
	cfg       Config
	mu        sync.RWMutex
	active    *memtable.MemTable
	immutable *memtable.MemTable // being flushed
	wal       *wal.WAL
	levels    [][]*sstable.SSTable // levels[i] = SSTables at level i
	clock     *mvcc.Clock
	txnMgr    *mvcc.TxnManager
	flushCh   chan struct{}
	compactCh chan int
	closeCh   chan struct{}
	wg        sync.WaitGroup
	fileSeq   uint64
}

// Open opens or creates an engine at cfg.Dir.
func Open(cfg Config) (*Engine, error) {
	if err := os.MkdirAll(cfg.Dir, 0755); err != nil {
		return nil, err
	}
	if cfg.MaxMemTableMB == 0 {
		cfg.MaxMemTableMB = 4
	}
	if cfg.L0TriggerCount == 0 {
		cfg.L0TriggerCount = l0CompactionTrigger
	}

	kb := cfg.MaxMemTableKB
	if kb == 0 { kb = cfg.MaxMemTableMB * 1024 }
	if kb == 0 { kb = 4 * 1024 }
	maxMem := int64(kb) * 1024
	clock := mvcc.NewClock(0)

	e := &Engine{
		cfg:       cfg,
		active:    memtable.New(maxMem),
		levels:    make([][]*sstable.SSTable, maxLevels),
		clock:     clock,
		txnMgr:    mvcc.NewTxnManager(clock),
		flushCh:   make(chan struct{}, 1),
		compactCh: make(chan int, maxLevels),
		closeCh:   make(chan struct{}),
	}

	// Open WAL and replay
	w, err := wal.Open(cfg.Dir)
	if err != nil {
		return nil, fmt.Errorf("engine: open WAL: %w", err)
	}
	e.wal = w

	if err := e.recoverFromWAL(); err != nil {
		return nil, fmt.Errorf("engine: WAL recovery: %w", err)
	}

	// Load existing SSTables from disk
	if err := e.loadSSTables(); err != nil {
		return nil, fmt.Errorf("engine: load sstables: %w", err)
	}

	// Background workers
	e.wg.Add(2)
	go e.flushWorker()
	go e.compactionWorker()

	return e, nil
}

// Put writes a key-value pair.
func (e *Engine) Put(key string, value []byte) error {
	version := e.clock.Tick()
	if err := e.wal.Append(wal.Record{
		Type: wal.RecordPut, Key: []byte(key), Value: value, TxnID: version,
	}); err != nil {
		return err
	}
	e.mu.Lock()
	e.active.Put(key, value, version)
	full := e.active.IsFull()
	e.mu.Unlock()

	if full {
		e.triggerFlush()
	}
	return nil
}

// Delete removes a key by writing a tombstone.
func (e *Engine) Delete(key string) error {
	version := e.clock.Tick()
	if err := e.wal.Append(wal.Record{
		Type: wal.RecordDelete, Key: []byte(key), TxnID: version,
	}); err != nil {
		return err
	}
	e.mu.Lock()
	e.active.Delete(key, version)
	full := e.active.IsFull()
	e.mu.Unlock()

	if full {
		e.triggerFlush()
	}
	return nil
}

// Get retrieves the value for a key. Returns (nil, nil) if not found.
func (e *Engine) Get(key string) ([]byte, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// 1. Active MemTable
	if entry, ok := e.active.Get(key); ok {
		if entry.Deleted {
			return nil, nil
		}
		return entry.Value, nil
	}

	// 2. Immutable MemTable
	if e.immutable != nil {
		if entry, ok := e.immutable.Get(key); ok {
			if entry.Deleted {
				return nil, nil
			}
			return entry.Value, nil
		}
	}

	// 3. SSTables — search from newest (L0) to oldest
	for level := 0; level < maxLevels; level++ {
		tables := e.levels[level]
		// L0 SSTables may overlap, search newest-first
		for i := len(tables) - 1; i >= 0; i-- {
			sst := tables[i]
			// Key-range pruning
			if key < sst.MinKey || key > sst.MaxKey {
				continue
			}
			entry, found, err := sst.Get(key)
			if err != nil {
				return nil, err
			}
			if found {
				if entry.Deleted {
					return nil, nil
				}
				return entry.Value, nil
			}
		}
	}
	return nil, nil
}

// Begin starts a new ACID transaction.
func (e *Engine) Begin() *mvcc.Transaction {
	return e.txnMgr.Begin()
}

// Commit applies a transaction atomically.
func (e *Engine) Commit(txn *mvcc.Transaction) error {
	writes, version := txn.Commit()
	if len(writes) == 0 {
		return nil
	}

	// Write all ops to WAL before applying to MemTable
	if err := e.wal.Append(wal.Record{Type: wal.RecordTxnBegin, TxnID: txn.ID}); err != nil {
		return err
	}
	for _, w := range writes {
		recType := wal.RecordPut
		if w.Deleted {
			recType = wal.RecordDelete
		}
		if err := e.wal.Append(wal.Record{
			Type: recType, TxnID: version, Key: []byte(w.Key), Value: w.Value,
		}); err != nil {
			return err
		}
	}
	if err := e.wal.Append(wal.Record{Type: wal.RecordTxnCommit, TxnID: txn.ID}); err != nil {
		return err
	}

	// Apply to MemTable
	e.mu.Lock()
	for _, w := range writes {
		if w.Deleted {
			e.active.Delete(w.Key, version)
		} else {
			e.active.Put(w.Key, w.Value, version)
		}
	}
	full := e.active.IsFull()
	e.mu.Unlock()

	if full {
		e.triggerFlush()
	}
	return nil
}

// Stats returns basic engine statistics.
func (e *Engine) Stats() map[string]interface{} {
	e.mu.RLock()
	defer e.mu.RUnlock()
	levelCounts := make([]int, maxLevels)
	for i, lvl := range e.levels {
		levelCounts[i] = len(lvl)
	}
	return map[string]interface{}{
		"memtable_size_bytes": e.active.SizeBytes(),
		"memtable_entries":    e.active.Len(),
		"wal_size_bytes":      e.wal.Size(),
		"level_counts":        levelCounts,
		"clock_version":       e.clock.Current(),
	}
}

// Close shuts down background workers and closes all resources.
func (e *Engine) Close() error {
	close(e.closeCh)
	e.wg.Wait()

	// Final flush
	e.mu.Lock()
	if e.active.Len() > 0 {
		e.doFlush()
	}
	e.mu.Unlock()

	for _, level := range e.levels {
		for _, sst := range level {
			sst.Close()
		}
	}
	return e.wal.Close()
}

// --- Internal ---

func (e *Engine) triggerFlush() {
	select {
	case e.flushCh <- struct{}{}:
	default:
	}
}

func (e *Engine) flushWorker() {
	defer e.wg.Done()
	for {
		select {
		case <-e.flushCh:
			e.mu.Lock()
			if e.active.IsFull() {
				e.doFlush()
			}
			e.mu.Unlock()
			// Check if L0 needs compaction
			if len(e.levels[0]) >= e.cfg.L0TriggerCount {
				select {
				case e.compactCh <- 0:
				default:
				}
			}
		case <-e.closeCh:
			return
		}
	}
}

// doFlush moves the active MemTable to immutable, writes it as an SSTable.
// Must be called with e.mu held.
func (e *Engine) doFlush() {
	e.immutable = e.active
	kb2 := e.cfg.MaxMemTableKB
		if kb2 == 0 { kb2 = e.cfg.MaxMemTableMB * 1024 }
		if kb2 == 0 { kb2 = 4 * 1024 }
		maxMem := int64(kb2) * 1024
	e.active = memtable.New(maxMem)

	// Write SSTable outside the lock would be ideal; simplified here
	go func(imm *memtable.MemTable) {
		if err := e.writeSSTable(imm, 0); err != nil {
			fmt.Printf("engine: flush error: %v\n", err)
			return
		}
		e.mu.Lock()
		if e.immutable == imm {
			e.immutable = nil
		}
		e.mu.Unlock()
		e.wal.Truncate()
	}(e.immutable)
}

func (e *Engine) writeSSTable(mem *memtable.MemTable, level int) error {
	e.mu.Lock()
	e.fileSeq++
	seq := e.fileSeq
	e.mu.Unlock()

	path := filepath.Join(e.cfg.Dir, fmt.Sprintf("L%d_%06d.sst", level, seq))
	w, err := sstable.NewWriter(path, mem.Len())
	if err != nil {
		return err
	}

	mem.Iterate(func(entry memtable.Entry) {
		w.Append(entry)
	})
	if err := w.Close(); err != nil {
		return err
	}

	sst, err := sstable.Open(path, level)
	if err != nil {
		return err
	}

	e.mu.Lock()
	e.levels[level] = append(e.levels[level], sst)
	e.mu.Unlock()
	return nil
}

func (e *Engine) compactionWorker() {
	defer e.wg.Done()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case level := <-e.compactCh:
			e.compact(level)
		case <-ticker.C:
			// Periodic compaction check
			e.mu.RLock()
			for i := 0; i < maxLevels-1; i++ {
				if len(e.levels[i]) >= e.cfg.L0TriggerCount {
					e.mu.RUnlock()
					e.compact(i)
					e.mu.RLock()
				}
			}
			e.mu.RUnlock()
		case <-e.closeCh:
			return
		}
	}
}

// compact merges SSTables at `level` into `level+1`.
// Uses a k-way merge maintaining sort order, dropping old versions & tombstones.
func (e *Engine) compact(level int) {
	e.mu.Lock()
	if len(e.levels[level]) < 2 {
		e.mu.Unlock()
		return
	}
	// Take all SSTables at this level for compaction
	tables := make([]*sstable.SSTable, len(e.levels[level]))
	copy(tables, e.levels[level])
	e.levels[level] = nil
	e.mu.Unlock()

	e.mu.Lock()
	e.fileSeq++
	seq := e.fileSeq
	e.mu.Unlock()

	outPath := filepath.Join(e.cfg.Dir, fmt.Sprintf("L%d_%06d.sst", level+1, seq))
	writer, err := sstable.NewWriter(outPath, 10000)
	if err != nil {
		fmt.Printf("engine: compaction writer: %v\n", err)
		return
	}

	// Open iterators for all input SSTables
	type heapItem struct {
		entry sstable.Entry
		iter  *sstable.Iterator
		idx   int
	}

	iters := make([]*sstable.Iterator, 0, len(tables))
	for _, t := range tables {
		it, err := t.Iterator()
		if err == nil {
			iters = append(iters, it)
		}
	}

	// Collect all entries, merge-sort, pick highest version per key
	var all []sstable.Entry
	for _, it := range iters {
		for it.Next() {
			all = append(all, it.Entry())
		}
		it.Close()
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].Key != all[j].Key {
			return all[i].Key < all[j].Key
		}
		return all[i].Version > all[j].Version // newer first
	})

	// Write deduplicated output — keep only latest version, drop tombstones at bottom level
	seen := ""
	for _, entry := range all {
		if entry.Key == seen {
			continue // older version
		}
		seen = entry.Key
		if entry.Deleted && level == maxLevels-2 {
			continue // Drop tombstone at max level
		}
		writer.Append(memtable.Entry{
			Key: entry.Key, Value: entry.Value,
			Deleted: entry.Deleted, Version: entry.Version,
		})
	}
	writer.Close()

	// Open compacted SSTable and replace
	newSST, err := sstable.Open(outPath, level+1)
	if err != nil {
		fmt.Printf("engine: open compacted sst: %v\n", err)
		return
	}

	e.mu.Lock()
	e.levels[level+1] = append(e.levels[level+1], newSST)
	e.mu.Unlock()

	// Remove old files
	for _, t := range tables {
		path := t.Path()
		t.Close()
		os.Remove(path)
	}
}

func (e *Engine) recoverFromWAL() error {
	pendingTxns := make(map[uint64][]wal.Record)

	return e.wal.Replay(func(rec wal.Record) error {
		switch rec.Type {
		case wal.RecordPut:
			e.active.Put(string(rec.Key), rec.Value, rec.TxnID)
		case wal.RecordDelete:
			e.active.Delete(string(rec.Key), rec.TxnID)
		case wal.RecordTxnBegin:
			pendingTxns[rec.TxnID] = nil
		case wal.RecordTxnCommit:
			for _, r := range pendingTxns[rec.TxnID] {
				if r.Type == wal.RecordPut {
					e.active.Put(string(r.Key), r.Value, r.TxnID)
				} else if r.Type == wal.RecordDelete {
					e.active.Delete(string(r.Key), r.TxnID)
				}
			}
			delete(pendingTxns, rec.TxnID)
		case wal.RecordTxnAbort:
			delete(pendingTxns, rec.TxnID)
		}
		return nil
	})
}

func (e *Engine) loadSSTables() error {
	for level := 0; level < maxLevels; level++ {
		pattern := filepath.Join(e.cfg.Dir, fmt.Sprintf("L%d_*.sst", level))
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		sort.Strings(matches)
		for _, path := range matches {
			sst, err := sstable.Open(path, level)
			if err != nil {
				return fmt.Errorf("engine: load sst %s: %w", path, err)
			}
			e.levels[level] = append(e.levels[level], sst)
		}
	}
	return nil
}
