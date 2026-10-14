package engine

import (
	"crypto/rand"
	"encoding/binary"
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
func (s *randomState) SeedWords(a, b uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seed(a, b)
}
func (s *randomState) Borrow() (*random.Rand, error) {
	s.mu.Lock()
	if err := s.initialize(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	return &s.rand, nil
}
func (s *randomState) Release() { s.mu.Unlock() }
