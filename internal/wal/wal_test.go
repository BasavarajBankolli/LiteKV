package wal

import (
	"os"
	"testing"
)

func TestWALWriteReplay(t *testing.T) {
	dir, _ := os.MkdirTemp("", "wal-test-*")
	defer os.RemoveAll(dir)

	w, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	records := []Record{
		{Type: RecordPut, TxnID: 1, Key: []byte("hello"), Value: []byte("world")},
		{Type: RecordPut, TxnID: 2, Key: []byte("foo"), Value: []byte("bar")},
		{Type: RecordDelete, TxnID: 3, Key: []byte("hello")},
		{Type: RecordTxnBegin, TxnID: 4},
		{Type: RecordPut, TxnID: 4, Key: []byte("txn-key"), Value: []byte("txn-val")},
		{Type: RecordTxnCommit, TxnID: 4},
	}

	for _, r := range records {
		if err := w.Append(r); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	w.Close()

	// Reopen and replay
	w2, _ := Open(dir)
	defer w2.Close()

	var replayed []Record
	if err := w2.Replay(func(r Record) error {
		replayed = append(replayed, r)
		return nil
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}

	if len(replayed) != len(records) {
		t.Fatalf("got %d records, want %d", len(replayed), len(records))
	}

	for i, want := range records {
		got := replayed[i]
		if got.Type != want.Type || string(got.Key) != string(want.Key) {
			t.Errorf("record %d: got {%v %q}, want {%v %q}", i, got.Type, got.Key, want.Type, want.Key)
		}
	}
}

func TestWALTruncate(t *testing.T) {
	dir, _ := os.MkdirTemp("", "wal-trunc-*")
	defer os.RemoveAll(dir)

	w, _ := Open(dir)
	defer w.Close()

	w.Append(Record{Type: RecordPut, Key: []byte("k"), Value: []byte("v")})
	if w.Size() == 0 {
		t.Fatal("expected non-zero size after write")
	}

	w.Truncate()
	if w.Size() != 0 {
		t.Fatalf("expected zero size after truncate, got %d", w.Size())
	}

	// Should replay nothing after truncate
	var count int
	w.Replay(func(Record) error { count++; return nil })
	if count != 0 {
		t.Fatalf("expected 0 records after truncate, got %d", count)
	}
}

func BenchmarkWALAppend(b *testing.B) {
	dir, _ := os.MkdirTemp("", "wal-bench-*")
	defer os.RemoveAll(dir)
	w, _ := Open(dir)
	defer w.Close()

	rec := Record{Type: RecordPut, TxnID: 1, Key: []byte("benchkey"), Value: make([]byte, 128)}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w.Append(rec)
	}
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/sec")
}
