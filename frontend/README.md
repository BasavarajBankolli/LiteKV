# LiteKV Dashboard — React Frontend

A live dashboard for the LiteKV storage engine. Shows real-time stats, LSM tree structure, key explorer, and ACID transaction builder.

## Setup

### 1. Start the LiteKV server
```powershell
# From the project root
go run ./cmd/server --rest :8080 --grpc :9090
```

### 2. Start the React dashboard
```powershell
cd frontend
npm install
npm start
```

Open http://localhost:3000

The React dev server proxies API calls to `:8080` automatically (configured in `package.json`).

## Features

**Dashboard tab**
- Live MemTable size, entry count, WAL size, SSTable count
- Throughput chart (ops/sec, updates every 2s)
- LSM level layout bar chart
- Engine info panel (clock version, Bloom filter rate, compaction trigger)

**Explorer tab**
- GET / PUT / DELETE individual keys
- Operation log with timestamps
- One-click "Seed demo data" to populate the engine
- REST API reference

**Transactions tab**
- Build and commit multi-op ACID batch transactions
- ACID properties explained (Atomicity, Consistency, Isolation, Durability)

## Production Build

```powershell
cd frontend
npm run build
```

Outputs to `frontend/build/`. Serve statically or embed in the Go binary with `//go:embed`.
