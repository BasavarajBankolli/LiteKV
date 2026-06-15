// Package benchmark provides throughput benchmarks for LiteKV.
package benchmark

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/BasavarajBankolli/litekv/internal/engine"
)

// PrintThroughput runs a quick write+read benchmark and prints results.
func PrintThroughput(eng *engine.Engine) {
	value := make([]byte, 128)
	rand.Read(value)

	const ops = 50_000

	fmt.Println("--- Write benchmark (buffered WAL, no fsync per-op) ---")
	start := time.Now()
	for i := 0; i < ops; i++ {
		eng.Put(fmt.Sprintf("bench:%010d", i), value)
	}
	elapsed := time.Since(start)
	fmt.Printf("Write throughput: %s ops/sec (%d ops in %v)\n",
		commaf(float64(ops)/elapsed.Seconds()), ops, elapsed.Round(time.Millisecond))

	fmt.Println("--- Read benchmark (MemTable hot path) ---")
	start = time.Now()
	for i := 0; i < ops; i++ {
		eng.Get(fmt.Sprintf("bench:%010d", rand.Intn(ops)))
	}
	elapsed = time.Since(start)
	fmt.Printf("Read throughput:  %s ops/sec (%d ops in %v)\n",
		commaf(float64(ops)/elapsed.Seconds()), ops, elapsed.Round(time.Millisecond))
}

func commaf(f float64) string {
	if f >= 1_000_000 {
		return fmt.Sprintf("%.1fM", f/1_000_000)
	}
	if f >= 1_000 {
		return fmt.Sprintf("%.1fK", f/1_000)
	}
	return fmt.Sprintf("%.0f", f)
}
