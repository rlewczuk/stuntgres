package engine

import "sync"

// replacementAdmission reserves catalog lifetimes without retaining its mutex.
// Readers may enter the relation manager while reserved, but queued SQL releases
// its reservation before waiting. Waiting replacements take priority over new readers.
type replacementAdmission struct {
	mu      sync.Mutex
	changed sync.Cond
	readers int
	writers int
	writing bool
}

func (a *replacementAdmission) conditionLocked() {
	if a.changed.L == nil {
		a.changed.L = &a.mu
	}
}

func (a *replacementAdmission) RLock() {
	a.mu.Lock()
	a.conditionLocked()
	for a.writing || a.writers != 0 {
		a.changed.Wait()
	}
	a.readers++
	a.mu.Unlock()
}

func (a *replacementAdmission) RUnlock() {
	a.mu.Lock()
	a.readers--
	if a.readers == 0 {
		a.changed.Broadcast()
	}
	a.mu.Unlock()
}

func (a *replacementAdmission) Lock() {
	a.mu.Lock()
	a.conditionLocked()
	a.writers++
	for a.writing || a.readers != 0 {
		a.changed.Wait()
	}
	a.writers--
	a.writing = true
	a.mu.Unlock()
}

func (a *replacementAdmission) TryLock() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.writing || a.readers != 0 || a.writers != 0 {
		return false
	}
	a.conditionLocked()
	a.writing = true
	return true
}

func (a *replacementAdmission) Unlock() {
	a.mu.Lock()
	a.writing = false
	a.changed.Broadcast()
	a.mu.Unlock()
}
