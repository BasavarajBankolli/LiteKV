# LiteKV

A persistent key-value store built from scratch in Go, implementing the LSM Tree (Log-Structured Merge-Tree) storage architecture used by LevelDB, RocksDB, and Apache Cassandra.

Built to understand how production databases work under the hood — every component (WAL, MemTable, SSTable, Bloom filter, MVCC) is written from first principles with no storage library dependencies.

**748K writes/sec · 800K reads/sec** — benchmarked on Windows 11, NVMe SSD, Go 1.21

---

## Dashboard

![LiteKV Dashboard](screenshots/dashboard.png)

The React dashboard shows live engine stats, LSM tree level layout, throughput charts, and SSTable distribution — all pulled from the REST API every 2 seconds.

![LiteKV Explorer](screenshots/explorer.png)

The Explorer tab lets you GET, PUT, and DELETE keys directly from the browser, with a live operation log and one-click demo data seeding.

---

## How it works

### Write path

```
Put("user:1", "Alice")
    │
    ├── 1. Append to WAL on disk
    │         Binary record with CRC32 checksum.
    │         On crash, WAL is replayed on startup
    │         to rebuild the MemTable exactly as it was.
    │
    ├── 2. Insert into MemTable (skip list in RAM)
    │         O(log n) sorted insert.
    │         Serves all reads instantly with no disk I/O.
    │
    └── 3. MemTable full → flush to SSTable on disk
              Runs in a background goroutine.
              A fresh MemTable takes new writes immediately.
              WAL is truncated after flush succeeds.
```

### Read path

```
Get("user:1")
    │
    ├── 1. Check active MemTable      O(log n), no disk I/O
    ├── 2. Check immutable MemTable   O(log n), no disk I/O
    └── 3. Search SSTables L0 → L6
              For each SSTable:
              └── Bloom filter: "does this key definitely NOT exist?"
                      NO  → skip file entirely, zero disk reads
                      MAYBE → binary search sparse index → read block
```

### Compaction

When L0 accumulates 4 or more SSTables, a background goroutine merge-sorts them into a single L1 file. This deduplicates keys (keeping the latest version), drops tombstones at the bottom level, and reclaims disk space. Keeps read amplification bounded as data grows.

---

## Architecture

```
┌──────────────────────────────────────────────────┐
│                   Client Layer                   │
│           REST :8080        gRPC :9090           │
└─────────────────────┬────────────────────────────┘
                      │
┌─────────────────────▼────────────────────────────┐
│                 Storage Engine                   │
│                                                  │
│  ┌──────────────┐     ┌────────────────────┐    │
│  │  MemTable    │     │  Write-Ahead Log   │    │
│  │  (skip list) │     │  (append-only)     │    │
│  └──────┬───────┘     └────────────────────┘    │
│         │ flush when full                        │
│  ┌──────▼───────────────────────────────────┐   │
│  │             LSM Levels                   │   │
│  │   L0: [sst][sst][sst]  ← newest         │   │
│  │   L1: [────────sst────────]              │   │
│  │   L2: [──────────────sst──────────]      │   │
│  │        each SSTable has a Bloom filter   │   │
│  └──────────────────────────────────────────┘   │
└──────────────────────────────────────────────────┘
```

---

## Performance

Run `go run ./cmd/server --bench` to measure on your own machine.

| Operation | Throughput | Notes |
|---|---|---|
| Write (buffered WAL) | **748K ops/sec** | No fsync per write, 256KB buffer |
| Read (MemTable hit) | **800K ops/sec** | Zero disk I/O |
| Write (sync mode) | ~500 ops/sec | fsync every write, full crash safety |

**Why it's fast:** Reads and writes go directly to an in-process skip list — no network round-trip, no serialization. The WAL uses a 256KB `bufio` buffer so the OS gets one write call per ~1000 ops instead of one per op.

**vs Redis:**

| | LiteKV | Redis |
|---|---|---|
| Write throughput | 748K ops/sec | ~100–150K ops/sec |
| Read throughput | 800K ops/sec | ~100–150K ops/sec |
| Dataset size | Disk-bounded | RAM-bounded |
| Persistence | WAL + SSTable | RDB / AOF |
| Transactions | MVCC / ACID | MULTI/EXEC |

Redis requires a TCP connection per operation even on localhost. LiteKV is an embedded engine — the caller is in the same process.

---

## Getting started

**Requirements:** Go 1.21+, Node.js 18+ (dashboard only)

```bash
git clone https://github.com/BasavarajBankolli/litekv
cd litekv
go mod tidy
```

**Start the server:**
```bash
go run ./cmd/server
```

**Start with demo data** (LSM levels visible on dashboard immediately):
```bash
go run ./cmd/server --seed
```

**Benchmark:**
```bash
go run ./cmd/server --bench
```

**Start the dashboard:**
```bash
cd frontend
npm install
npm start
# open http://localhost:3000
```

### Flags

| Flag | Default | Description |
|---|---|---|
| `--dir` | `./data` | Directory for WAL and SSTable files |
| `--rest` | `:8080` | REST API listen address |
| `--grpc` | `:9090` | gRPC listen address |
| `--mem` | `256` | MemTable flush threshold in KB |
| `--seed` | false | Pre-load 5000 keys on startup |
| `--bench` | false | Run throughput benchmark and exit |

---

## REST API

Base URL: `http://localhost:8080`

### GET /v1/keys/:key

Read a value by key. Returns 404 if the key does not exist.

```bash
curl http://localhost:8080/v1/keys/user:1
```

```json
{
  "key": "user:1",
  "value": "QWxpY2U="
}
```

> Values are returned as base64-encoded bytes. Decode with `atob()` in the browser or `base64 -d` on the command line.

---

### PUT /v1/keys/:key

Write a key-value pair. Creates or overwrites.

```bash
curl -X PUT http://localhost:8080/v1/keys/user:1 \
     -H "Content-Type: application/json" \
     -d '{"value": "Alice"}'
```

```json
{ "ok": true }
```

---

### DELETE /v1/keys/:key

Delete a key. Writes a tombstone — the key is logically removed immediately and physically reclaimed during the next compaction.

```bash
curl -X DELETE http://localhost:8080/v1/keys/user:1
```

```json
{ "ok": true }
```

---

### POST /v1/batch

Commit multiple operations atomically. All ops apply or none do (ACID transaction backed by MVCC).

```bash
curl -X POST http://localhost:8080/v1/batch \
     -H "Content-Type: application/json" \
     -d '{
       "ops": [
         {"type": "put",    "key": "user:1", "value": "Alice"},
         {"type": "put",    "key": "user:2", "value": "Bob"},
         {"type": "delete", "key": "user:old"}
       ]
     }'
```

```json
{ "ok": true, "ops_applied": 3 }
```

---

### GET /v1/stats

Returns live engine statistics.

```bash
curl http://localhost:8080/v1/stats
```

```json
{
  "clock_version": 5000,
  "level_counts": [1, 0, 0, 0, 0, 0, 0],
  "memtable_entries": 1024,
  "memtable_size_bytes": 92160,
  "wal_size_bytes": 392601
}
```

---

### GET /healthz

Health check. Returns 200 if the server is up.

```bash
curl http://localhost:8080/healthz
```

```json
{ "status": "ok" }
```

---

## Go API

```go
eng, err := engine.Open(engine.Config{Dir: "./data"})
if err != nil {
    log.Fatal(err)
}
defer eng.Close()

// Single operations
eng.Put("user:1", []byte("Alice"))

val, err := eng.Get("user:1")
// val == []byte("Alice"), err == nil

// Returns nil, nil for missing keys
val, _ = eng.Get("nonexistent")
// val == nil

eng.Delete("user:1")

// ACID transaction — all ops are atomic
txn := eng.Begin()
txn.Put("account:alice", []byte("1000"))
txn.Put("account:bob",   []byte("500"))
txn.Delete("account:old")

if err := eng.Commit(txn); err != nil {
    txn.Abort()
    log.Fatal(err)
}
```

---

## Project structure

```
litekv/
├── cmd/server/             # Server entry point
├── internal/
│   ├── engine/             # LSM engine — wires all components together
│   ├── wal/                # Write-Ahead Log, CRC32, crash recovery
│   ├── memtable/           # Skip list in-memory write buffer
│   ├── sstable/            # On-disk sorted tables, sparse index, Bloom footer
│   ├── bloom/              # Bloom filter, FNV double-hashing
│   ├── mvcc/               # Logical clock, transaction manager
│   ├── rest/               # REST/JSON API (Gin)
│   └── grpcserver/         # gRPC interface
├── frontend/               # React dashboard
├── benchmark/              # Throughput benchmarks
└── proto/                  # Protobuf definitions
```

---

## Tests

```bash
# All tests
go test ./...

# With race detector
go test -race ./...

# Specific package
go test ./internal/engine/ -v -run TestEngineCrashRecovery

# Go benchmark suite
go test ./benchmark/ -bench=. -benchtime=10s -v
```

---

## Design decisions

**Skip list for MemTable** — gives O(log n) reads and writes like a balanced BST, but is far simpler to implement correctly under concurrent access. Redis uses skip lists for sorted sets for the same reason.

**Sparse index over dense** — a dense index needs one entry per key, which exhausts memory at scale. A sparse index stores one entry per data block, binary-searches to the right block, then scans within it. This is exactly how LevelDB's block index works.

**Double-hashing for Bloom filters** — instead of k independent hash functions, two FNV-1a variants (h1, h2) derive k bit positions as `h1 + i*h2`. Mathematically equivalent to k independent hashes at a fraction of the compute cost.

**MVCC over read-write locks** — a global RWLock blocks readers when a writer holds it and vice versa. MVCC assigns a monotonic version to every write; readers snapshot the current version and see a consistent view without taking any lock. This is how PostgreSQL and CockroachDB handle concurrency.

**Buffered WAL by default** — per-write fsync costs ~2ms on Windows, which caps throughput at ~500 ops/sec. The default 256KB buffer amortises disk writes across ~1000 ops, reaching 748K ops/sec. Enable `SyncWrites: true` when you need full crash durability over raw throughput.

---

## References

- [The Log-Structured Merge-Tree — O'Neil et al., 1996](https://www.cs.umb.edu/~poneil/lsmtree.pdf)
- [LevelDB implementation notes](https://github.com/google/leveldb/blob/main/doc/impl.md)
- [Designing Data-Intensive Applications, Chapter 3](https://dataintensive.net/) — Kleppmann
- [RocksDB tuning guide](https://github.com/facebook/rocksdb/wiki/RocksDB-Tuning-Guide)