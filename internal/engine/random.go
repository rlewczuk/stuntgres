package engine

import (
	"crypto/rand"
	"encoding/binary"
	"math"
	random "math/rand/v2"
	"sync"
)

// randomState is pointer-owned with its Session. Its mutex covers an entire
// builtin operation; argument evaluation happens before acquiring it. No
// transaction/catalog/lifecycle lock is acquired while this mutex is held.
// State deliberately survives rollback and native catalog replacement.
type randomState struct {
	mu          sync.Mutex
	pcg         random.PCG
	rand        random.Rand
	initialized bool
}

func (s *randomState) seed(a, b uint64) {
	s.pcg.Seed(a, b)
	if !s.initialized {
		s.rand = *random.New(&s.pcg)
		s.initialized = true
	}
}
func (s *randomState) initialize() error {
	if s.initialized {
		return nil
	}
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return err
	}
	s.seed(binary.LittleEndian.Uint64(entropy[:8]), binary.LittleEndian.Uint64(entropy[8:]))
	return nil
}
func (s *randomState) reseed(value float64) {
	if value == 0 {
		value = 0
	} // PostgreSQL treats the two signed zeros alike.
	bits := math.Float64bits(value)
	s.seed(bits^0x9e3779b97f4a7c15, bits^0xd1b54a32d192ed03)
}

// The wraparound width zero denotes the entire signed 64-bit domain.
// Rejection avoids modulo bias, including ranges crossing signed zero.
func randomInteger(lower, upper int64, next func() uint64) int64 {
	if lower == upper {
		return lower
	}
	span := uint64(upper) - uint64(lower) + 1
	if span == 0 {
		return int64(uint64(lower) + next())
	}
	threshold := -span % span
	for {
		word := next()
		if word >= threshold {
			return int64(uint64(lower) + word%span)
		}
	}
}
