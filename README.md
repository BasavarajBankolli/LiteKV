# LiteKV

A persistent, embedded key-value store built in Go from first principles.

Implements an LSM Tree (Log-Structured Merge-Tree) storage engine — the same architecture used by LevelDB, RocksDB, and Cassandra. Built to understand how production databases work under the hood.

---

## Architecture

```
              Write Path
              ──────────
  Put(k,v) → WAL (fsync) → MemTable (skip list)
                                  │
                           [full: 4MB]
                                  ↓
                          SSTable (L0) ← immutable, sorted
                                  │
                         [L0 ≥ 4 files]
                                  ↓
                          Compaction → L1, L2, ...


              Read Path
              ─────────
  Get(k) → MemTable → Immutable MemTable
               → L0 SSTables (Bloom filter → index → disk)
               → L1 SSTables ...
```

### Components

**Write-Ahead Log (WAL)**
Every write is durably appended to the WAL before touching memory. On a crash, the WAL is replayed on startup to rebuild the MemTable. Wire format: `[type:1B][txnID:8B][keyLen:4B][valLen:4B][key][val][crc32:4B]`. The CRC catches partial writes from mid-crash truncations.

**MemTable**
An in-memory sorted buffer backed by a skip list (O(log n) reads and writes). When it reaches 4 MB, it becomes immutable and is flushed to disk as an SSTable in a background goroutine while a fresh MemTable takes writes.

**SSTable (Sorted String Table)**
Immutable, sorted on-disk files. Layout: data blocks → sparse index → Bloom filter → footer. The footer stores byte offsets so the index and filter can be loaded at open time without scanning the whole file.

**Bloom Filters**
Each SSTable has a Bloom filter sized for 1% false-positive rate. Before doing any disk I/O, `Get` asks the filter: "might this key exist?" A definitive NO skips the disk read entirely. On a workload with many misses (common in practice), this eliminates the majority of disk seeks.

**MVCC (Multi-Version Concurrency Control)**
A logical clock assigns monotonically increasing versions to every write. Readers snapshot the current version and see a consistent view without taking locks — enabling concurrent reads with zero reader-writer contention.

**Compaction**
Background goroutine monitors L0. When L0 accumulates ≥ 4 SSTables, it merge-sorts them (k-way merge) into a single L1 file, dropping duplicate keys (keeping latest version) and tombstones at the bottom level. This bounds read amplification and reclaims disk space.

---

## Performance

Run `go run ./cmd/server --bench` to measure on your machine.

| Operation        | Benchmarked (Windows, NVMe SSD) | Notes                             |
|-----------------|----------------------------------|-----------------------------------|
| Write (Put)     | **573K ops/sec**                 | Buffered WAL, no per-write fsync  |
| Read (Get, hot) | **622K ops/sec**                 | MemTable hit, zero disk I/O       |

Benchmarked on Windows 11, Go 1.21, NVMe SSD (D: drive). The WAL runs in buffered mode by default — enable `SyncWrites: true` for full crash durability at the cost of throughput.

**vs Redis (single-threaded, in-memory, linux benchmark):**

| Metric           | LiteKV              | Redis          |
|-----------------|---------------------|----------------|
| Write throughput | **573K ops/sec**    | ~100–150K ops/s |
| Read throughput  | **622K ops/sec**    | ~100–150K ops/s |
| Persistence      | WAL + SSTable       | RDB / AOF      |
| Dataset size     | Disk-bounded        | RAM-bounded    |
| Transactions     | MVCC / ACID         | MULTI/EXEC     |

LiteKV matches or exceeds Redis throughput on the hot path because the MemTable is a skip list in process memory — no network round-trip, no serialization overhead. The key architectural difference: LiteKV persists datasets larger than available RAM via the LSM tree.

---

## Quick Start

```bash
git clone https://github.com/yourusername/litekv
cd litekv
go mod tidy

# Start the server
go run ./cmd/server --dir ./data --rest :8080 --grpc :9090

# Run throughput benchmark
go run ./cmd/server --bench
```

### REST API

```bash
# Write a key
curl -X PUT http://localhost:8080/v1/keys/hello \
     -H "Content-Type: application/json" \
     -d '{"value": "world"}'

# Read a key
curl http://localhost:8080/v1/keys/hello
# {"key":"hello","value":"d29ybGQ="}   ← base64("world")

# Delete a key
curl -X DELETE http://localhost:8080/v1/keys/hello

# Atomic batch write (ACID transaction)
curl -X POST http://localhost:8080/v1/batch \
     -H "Content-Type: application/json" \
     -d '{
       "ops": [
         {"type": "put",    "key": "user:1", "value": "alice"},
         {"type": "put",    "key": "user:2", "value": "bob"},
         {"type": "delete", "key": "user:0"}
       ]
     }'

# Engine stats
curl http://localhost:8080/v1/stats
```

### Go API

```go
eng, _ := engine.Open(engine.Config{Dir: "./data"})
defer eng.Close()

// Single writes
eng.Put("user:1", []byte(`{"name":"alice"}`))
val, _ := eng.Get("user:1")

// ACID transaction
txn := eng.Begin()
txn.Put("account:alice", []byte("1000"))
txn.Put("account:bob",   []byte("500"))
txn.Delete("account:old")
eng.Commit(txn)   // atomic: all-or-nothing
```

---

## Running Tests

```bash
# All unit + integration tests
go test ./...

# Just benchmarks
go test ./benchmark/ -bench=. -benchtime=10s -v

# Race detector
go test -race ./...

# Single package
go test ./internal/engine/ -v -run TestEngineCrashRecovery
```

---

## Project Structure

```
litekv/
├── cmd/server/         # Entry point: starts REST + gRPC
├── internal/
│   ├── bloom/          # Bloom filter (FNV double-hashing)
│   ├── wal/            # Write-Ahead Log with CRC checksums
│   ├── memtable/       # Skip list in-memory write buffer
│   ├── sstable/        # On-disk sorted string tables
│   ├── mvcc/           # Logical clock + transaction manager
│   ├── engine/         # LSM engine wiring all components
│   ├── grpcserver/     # gRPC interface
│   └── rest/           # REST/JSON interface (Gin)
├── proto/              # Protobuf definitions
└── benchmark/          # Throughput benchmarks
```

---

## Design Decisions

**Why skip list for MemTable?** A skip list gives O(log n) reads/writes like a balanced BST, but is simpler to implement correctly under concurrent access. Redis uses skip lists for sorted sets for the same reason.

**Why sparse index instead of dense?** A dense index requires one entry per key — for millions of keys this exhausts memory. A sparse index stores one entry per block and binary searches within blocks on disk. This is how LevelDB's block index works.

**Why double-hashing for Bloom filters?** Instead of k independent hash functions (expensive), we compute two hashes (h1, h2) and derive k positions as `h1 + i*h2`. This is mathematically equivalent with far less computation.

**Why MVCC instead of locks?** Locks cause readers to block writers and vice versa. With MVCC, each write gets a new version; readers snapshot a version at start time and never block. This is how PostgreSQL and CockroachDB handle concurrency.

---

## What's Not Implemented (intentionally)

- Distributed consensus (Raft) — would need 3× the codebase
- Compression (Snappy/LZ4) for SSTable blocks
- Block cache (LRU) for hot SSTable blocks
- Prefix scans / range iterators over REST

These are natural extensions — the core LSM engine handles them at the `Iterator` level.

---

## References

- [The Log-Structured Merge-Tree (O'Neil et al., 1996)](https://www.cs.umb.edu/~poneil/lsmtree.pdf)
- [LevelDB Implementation Notes](https://github.com/google/leveldb/blob/main/doc/impl.md)
- [Designing Data-Intensive Applications — Chapter 3](https://dataintensive.net/) (Kleppmann)
- [RocksDB Tuning Guide](https://github.com/facebook/rocksdb/wiki/RocksDB-Tuning-Guide)
