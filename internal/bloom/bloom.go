// Package bloom provides a space-efficient probabilistic data structure
// used to eliminate unnecessary disk reads for keys that don't exist.
package bloom

import (
	"encoding/binary"
	"math"
)

// Filter is a Bloom filter with k hash functions.
type Filter struct {
	bits    []byte
	numBits uint64
	numHash uint64
}

// New creates a Bloom filter sized for expectedItems at the given false-positive rate.
// Formula: m = -n*ln(p) / (ln2)^2, k = (m/n)*ln2
func New(expectedItems int, falsePositiveRate float64) *Filter {
	m := optimalBits(expectedItems, falsePositiveRate)
	k := optimalHashCount(m, uint64(expectedItems))
	return &Filter{
		bits:    make([]byte, (m+7)/8),
		numBits: m,
		numHash: k,
	}
}

// Add inserts a key into the filter.
func (f *Filter) Add(key []byte) {
	h1, h2 := hash(key)
	for i := uint64(0); i < f.numHash; i++ {
		bit := (h1 + i*h2) % f.numBits
		f.bits[bit/8] |= 1 << (bit % 8)
	}
}

// MightContain returns false if key is definitely NOT in the set.
// Returns true if key MIGHT be in the set (with false-positive probability).
func (f *Filter) MightContain(key []byte) bool {
	h1, h2 := hash(key)
	for i := uint64(0); i < f.numHash; i++ {
		bit := (h1 + i*h2) % f.numBits
		if f.bits[bit/8]&(1<<(bit%8)) == 0 {
			return false // Definitely not present
		}
	}
	return true // Possibly present
}

// Encode serializes the filter to bytes for persistence in SSTable footer.
func (f *Filter) Encode() []byte {
	buf := make([]byte, 16+len(f.bits))
	binary.LittleEndian.PutUint64(buf[0:8], f.numBits)
	binary.LittleEndian.PutUint64(buf[8:16], f.numHash)
	copy(buf[16:], f.bits)
	return buf
}

// Decode deserializes a filter from bytes.
func Decode(data []byte) *Filter {
	numBits := binary.LittleEndian.Uint64(data[0:8])
	numHash := binary.LittleEndian.Uint64(data[8:16])
	bits := make([]byte, len(data)-16)
	copy(bits, data[16:])
	return &Filter{bits: bits, numBits: numBits, numHash: numHash}
}

// FalsePositiveRate estimates current FP rate given number of inserted items.
func (f *Filter) FalsePositiveRate(insertedItems int) float64 {
	exponent := -float64(f.numHash) * float64(insertedItems) / float64(f.numBits)
	return math.Pow(1-math.Exp(exponent), float64(f.numHash))
}

// --- Helpers ---

func optimalBits(n int, p float64) uint64 {
	return uint64(math.Ceil(-float64(n) * math.Log(p) / (math.Log(2) * math.Log(2))))
}

func optimalHashCount(m, n uint64) uint64 {
	k := uint64(math.Round(float64(m) / float64(n) * math.Log(2)))
	if k < 1 {
		return 1
	}
	return k
}

// hash returns two independent 64-bit hash values using FNV-1a + variant.
// We use double-hashing: H(i) = h1 + i*h2 to simulate k hash functions.
func hash(key []byte) (uint64, uint64) {
	var h1, h2 uint64 = 14695981039346656037, 1099511628211
	for _, b := range key {
		h1 ^= uint64(b)
		h1 *= 1099511628211
	}
	// Second hash using FNV with different seed
	h2 = 0xcbf29ce484222325
	for i := len(key) - 1; i >= 0; i-- {
		h2 ^= uint64(key[i])
		h2 *= 0x100000001b3
	}
	if h2 == 0 {
		h2 = 1 // Avoid zero second hash
	}
	return h1, h2
}
