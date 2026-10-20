package session

import (
	"crypto/rand"
	"encoding/binary"
	random "math/rand/v2"
	"sync"
)

// RandomState is pointer-owned with its Session. Its mutex covers an entire
// builtin operation; argument evaluation happens before acquiring it. No
// transaction/catalog/lifecycle lock is acquired while this mutex is held.
// State deliberately survives rollback and native catalog replacement.
type RandomState struct {
	Mu          sync.Mutex
	pcg         random.PCG
	Rand        random.Rand
	Initialized bool
}

func (s *RandomState) seed(a, b uint64) {
	s.pcg.Seed(a, b)
	if !s.Initialized {
		s.Rand = *random.New(&s.pcg)
		s.Initialized = true
	}
}
func (s *RandomState) initialize() error {
	if s.Initialized {
		return nil
	}
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return err
	}
	s.seed(binary.LittleEndian.Uint64(entropy[:8]), binary.LittleEndian.Uint64(entropy[8:]))
	return nil
}
func (s *RandomState) SeedWords(a, b uint64) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.seed(a, b)
}
func (s *RandomState) Borrow() (*random.Rand, error) {
	s.Mu.Lock()
	if err := s.initialize(); err != nil {
		s.Mu.Unlock()
		return nil, err
	}
	return &s.Rand, nil
}
func (s *RandomState) Release() { s.Mu.Unlock() }
