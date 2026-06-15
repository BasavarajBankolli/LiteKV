package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"

	"github.com/BasavarajBankolli/litekv/benchmark"
	"github.com/BasavarajBankolli/litekv/internal/engine"
	"github.com/BasavarajBankolli/litekv/internal/grpcserver"
	"github.com/BasavarajBankolli/litekv/internal/rest"
)

func main() {
	dir      := flag.String("dir",   "./data", "data directory")
	restAddr := flag.String("rest",  ":8080",  "REST listen address")
	grpcAddr := flag.String("grpc",  ":9090",  "gRPC listen address")
	memKB    := flag.Int("mem",      256,       "MemTable flush threshold in KB (default 256KB — good for demos)")
	runBench := flag.Bool("bench",   false,     "run throughput benchmark and exit")
	seed     := flag.Bool("seed",    false,     "seed 5000 keys on startup so LSM tree is visible immediately")
	flag.Parse()

	eng, err := engine.Open(engine.Config{
		Dir:           *dir,
		MaxMemTableKB: *memKB, // convert KB → MB (min 1)
	})
	if err != nil {
		log.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	if *runBench {
		fmt.Println("=== LiteKV Throughput Benchmark ===")
		benchmark.PrintThroughput(eng)
		os.Exit(0)
	}

	fmt.Println("╔══════════════════════════════════╗")
	fmt.Println("║       LiteKV — LSM Store         ║")
	fmt.Println("╠══════════════════════════════════╣")
	fmt.Printf( "║  data dir  : %-20s║\n", *dir)
	fmt.Printf( "║  REST      : %-20s║\n", *restAddr)
	fmt.Printf( "║  gRPC      : %-20s║\n", *grpcAddr)
	fmt.Printf( "║  MemTable  : %dKB%-17s║\n", *memKB, "")
	fmt.Println("╚══════════════════════════════════╝")

	// Seed data so LSM tree is visible on dashboard immediately
	if *seed {
		fmt.Print("Seeding 5000 keys to populate LSM tree... ")
		categories := []string{"user", "session", "product", "order", "config", "cache", "event", "log"}
		for i := 0; i < 5000; i++ {
			cat := categories[rand.Intn(len(categories))]
			key := fmt.Sprintf("%s:%06d", cat, i)
			val := fmt.Sprintf(`{"id":%d,"name":"record_%d","score":%d,"active":true}`, i, i, rand.Intn(1000))
			eng.Put(key, []byte(val))
		}
		fmt.Println("done ✓")
		fmt.Println("→ Check the dashboard — you should see SSTables in L0/L1")
	}

	// Start gRPC server
	grpcSrv := grpcserver.NewServer(*grpcAddr, eng)
	go func() {
		if err := grpcSrv.Serve(); err != nil {
			log.Printf("gRPC server error: %v", err)
		}
	}()

	// Start REST server
	restSrv := rest.New(eng)
	go func() {
		if err := restSrv.Run(*restAddr); err != nil {
			log.Printf("REST server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println("\nShutting down...")
	grpcSrv.Stop()
	eng.Close()
	fmt.Println("Goodbye.")
}
