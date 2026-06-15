package bloom

import (
	"fmt"
	"testing"
)

func TestBloomBasic(t *testing.T) {
	f := New(1000, 0.01)

	keys := []string{"apple", "banana", "cherry", "date", "elderberry"}
	for _, k := range keys {
		f.Add([]byte(k))
	}

	// All inserted keys must pass
	for _, k := range keys {
		if !f.MightContain([]byte(k)) {
			t.Errorf("false negative for key %q", k)
		}
	}
}

func TestBloomFalsePositiveRate(t *testing.T) {
	const n = 10_000
	f := New(n, 0.01)

	for i := 0; i < n; i++ {
		f.Add([]byte(fmt.Sprintf("key:%d", i)))
	}

	// Check FP rate on unseen keys
	fps := 0
	const trials = 10_000
	for i := n; i < n+trials; i++ {
		if f.MightContain([]byte(fmt.Sprintf("key:%d", i))) {
			fps++
		}
	}
	fpRate := float64(fps) / float64(trials)
	t.Logf("False positive rate: %.4f (target ≤0.01)", fpRate)
	if fpRate > 0.02 { // Allow 2x tolerance
		t.Errorf("FP rate too high: %.4f", fpRate)
	}
}

func TestBloomEncodeDecode(t *testing.T) {
	f := New(100, 0.01)
	keys := []string{"a", "b", "c", "d", "e"}
	for _, k := range keys {
		f.Add([]byte(k))
	}

	data := f.Encode()
	f2 := Decode(data)

	for _, k := range keys {
		if !f2.MightContain([]byte(k)) {
			t.Errorf("decode: false negative for %q", k)
		}
	}
}

func BenchmarkBloomAdd(b *testing.B) {
	f := New(b.N+1, 0.01)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.Add([]byte(fmt.Sprintf("key:%d", i)))
	}
}

func BenchmarkBloomContains(b *testing.B) {
	f := New(100_000, 0.01)
	for i := 0; i < 100_000; i++ {
		f.Add([]byte(fmt.Sprintf("key:%d", i)))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.MightContain([]byte(fmt.Sprintf("key:%d", i%100_000)))
	}
}
