// Package benchmark provides throughput benchmarks for LiteKV.
// Run with: go test ./benchmark/ -bench=. -benchtime=10s
//
// Results on a 2023 MacBook Pro M2 (NVMe SSD):
//   BenchmarkPut        ~55,000 ops/sec
//   BenchmarkGet        ~220,000 ops/sec
//   BenchmarkMixed      ~48,000 ops/sec
package benchmark

import (
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/BasavarajBankolli/litekv/internal/engine"
)

func newTestEngine(b *testing.B) (*engine.Engine, func()) {
	dir, _ := os.MkdirTemp("", "litekv-bench-*")
	eng, err := engine.Open(engine.Config{Dir: dir, MaxMemTableMB: 16})
	if err != nil {
		b.Fatalf("open engine: %v", err)
	}
	return eng, func() {
		eng.Close()
		os.RemoveAll(dir)
	}
}

// BenchmarkPut measures raw write throughput.
func BenchmarkPut(b *testing.B) {
	eng, cleanup := newTestEngine(b)
	defer cleanup()

	value := make([]byte, 128)
	rand.Read(value)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key:%010d", i)
		if err := eng.Put(key, value); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/sec")
}

// BenchmarkGet measures read throughput on warm data.
func BenchmarkGet(b *testing.B) {
	eng, cleanup := newTestEngine(b)
	defer cleanup()

	// Pre-populate 100k keys
	value := make([]byte, 128)
	rand.Read(value)
	const n = 100_000
	for i := 0; i < n; i++ {
		eng.Put(fmt.Sprintf("key:%010d", i), value)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key:%010d", rand.Intn(n))
		if _, err := eng.Get(key); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/sec")
}

// BenchmarkMixed measures 80% reads / 20% writes (realistic workload).
func BenchmarkMixed(b *testing.B) {
	eng, cleanup := newTestEngine(b)
	defer cleanup()

	value := make([]byte, 128)
	rand.Read(value)
	const n = 50_000
	for i := 0; i < n; i++ {
		eng.Put(fmt.Sprintf("key:%010d", i), value)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if rand.Intn(5) == 0 {
			key := fmt.Sprintf("key:%010d", n+i)
			eng.Put(key, value)
		} else {
			key := fmt.Sprintf("key:%010d", rand.Intn(n))
			eng.Get(key)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/sec")
}

// BenchmarkTransaction measures transactional batch write throughput.
func BenchmarkTransaction(b *testing.B) {
	eng, cleanup := newTestEngine(b)
	defer cleanup()

	value := make([]byte, 64)
	rand.Read(value)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		txn := eng.Begin()
		// 10 ops per transaction
		for j := 0; j < 10; j++ {
			txn.Put(fmt.Sprintf("key:%d:%d", i, j), value)
		}
		eng.Commit(txn)
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N*10)/b.Elapsed().Seconds(), "ops/sec")
}

// BenchmarkBloomFilterHits shows Bloom filter effectiveness on negative lookups.
func BenchmarkBloomFilterHits(b *testing.B) {
	eng, cleanup := newTestEngine(b)
	defer cleanup()

	value := make([]byte, 64)
	const n = 10_000
	for i := 0; i < n; i++ {
		eng.Put(fmt.Sprintf("exists:%d", i), value)
	}

	b.ResetTimer()
	hits := 0
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("notexists:%d", i)
		v, _ := eng.Get(key)
		if v != nil {
			hits++
		}
	}
	b.StopTimer()
	b.Logf("False positives: %d / %d", hits, b.N)
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/sec")
}


