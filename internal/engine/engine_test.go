package engine

import (
	"fmt"
	"os"
	"testing"
)

func newEngine(t testing.TB) (*Engine, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "litekv-engine-*")
	if err != nil {
		t.Fatal(err)
	}
	eng, err := Open(Config{Dir: dir, MaxMemTableMB: 1})
	if err != nil {
		t.Fatal(err)
	}
	return eng, func() {
		eng.Close()
		os.RemoveAll(dir)
	}
}

func TestEnginePutGet(t *testing.T) {
	eng, cleanup := newEngine(t)
	defer cleanup()

	if err := eng.Put("hello", []byte("world")); err != nil {
		t.Fatalf("put: %v", err)
	}

	val, err := eng.Get("hello")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(val) != "world" {
		t.Fatalf("got %q, want %q", val, "world")
	}
}

func TestEngineMissingKey(t *testing.T) {
	eng, cleanup := newEngine(t)
	defer cleanup()

	val, err := eng.Get("nonexistent")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if val != nil {
		t.Fatalf("expected nil for missing key, got %q", val)
	}
}

func TestEngineDelete(t *testing.T) {
	eng, cleanup := newEngine(t)
	defer cleanup()

	eng.Put("key", []byte("value"))
	eng.Delete("key")

	val, _ := eng.Get("key")
	if val != nil {
		t.Fatalf("expected deleted key to return nil, got %q", val)
	}
}

func TestEngineTransaction(t *testing.T) {
	eng, cleanup := newEngine(t)
	defer cleanup()

	// Atomic batch: write 3 keys, delete 1
	eng.Put("existing", []byte("old"))

	txn := eng.Begin()
	txn.Put("k1", []byte("v1"))
	txn.Put("k2", []byte("v2"))
	txn.Put("k3", []byte("v3"))
	txn.Delete("existing")

	if err := eng.Commit(txn); err != nil {
		t.Fatalf("commit: %v", err)
	}

	for _, tc := range []struct{ key, want string }{
		{"k1", "v1"}, {"k2", "v2"}, {"k3", "v3"},
	} {
		v, _ := eng.Get(tc.key)
		if string(v) != tc.want {
			t.Errorf("key %q: got %q, want %q", tc.key, v, tc.want)
		}
	}

	v, _ := eng.Get("existing")
	if v != nil {
		t.Error("expected 'existing' to be deleted")
	}
}

func TestEngineTransactionAbort(t *testing.T) {
	eng, cleanup := newEngine(t)
	defer cleanup()

	txn := eng.Begin()
	txn.Put("willnotexist", []byte("value"))
	txn.Abort()

	val, _ := eng.Get("willnotexist")
	if val != nil {
		t.Fatal("aborted transaction should not persist data")
	}
}

func TestEngineCrashRecovery(t *testing.T) {
	dir, _ := os.MkdirTemp("", "litekv-crash-*")
	defer os.RemoveAll(dir)

	// Write some data, then close without flushing SSTable
	eng1, err := Open(Config{Dir: dir, MaxMemTableMB: 64})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		eng1.Put(fmt.Sprintf("key:%d", i), []byte(fmt.Sprintf("val:%d", i)))
	}
	eng1.Close() // WAL is intact

	// Reopen and verify WAL replay
	eng2, err := Open(Config{Dir: dir, MaxMemTableMB: 64})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer eng2.Close()

	for i := 0; i < 100; i++ {
		v, err := eng2.Get(fmt.Sprintf("key:%d", i))
		if err != nil {
			t.Fatalf("get after recovery: %v", err)
		}
		want := fmt.Sprintf("val:%d", i)
		if string(v) != want {
			t.Errorf("key %d: got %q, want %q", i, v, want)
		}
	}
}

func TestEngineMemTableFlush(t *testing.T) {
	// Use a tiny MemTable to force SSTable flush
	eng, cleanup := newEngine(t) // 1 MB
	defer cleanup()

	// Write ~2 MB of data to force at least one flush
	value := make([]byte, 1024) // 1 KB
	for i := 0; i < 2048; i++ {
		eng.Put(fmt.Sprintf("key:%06d", i), value)
	}

	// All keys should still be readable after flush
	v, err := eng.Get("key:000000")
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 1024 {
		t.Fatalf("expected 1024 bytes, got %d", len(v))
	}
}

func TestEngineStats(t *testing.T) {
	eng, cleanup := newEngine(t)
	defer cleanup()

	eng.Put("k", []byte("v"))
	stats := eng.Stats()

	if stats["memtable_entries"].(int) == 0 {
		t.Error("expected non-zero memtable entries")
	}
	if stats["clock_version"].(uint64) == 0 {
		t.Error("expected non-zero clock version")
	}
}
