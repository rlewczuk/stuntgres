// Package hbarules owns mutable simulated HBA diagnostics as immutable snapshots.
package hbarules

import "sync"

// Rule contains independent diagnostic facts, never authentication instructions.
// Nil pointers and slices denote SQL NULL; present empty values remain present.
type Rule struct {
	RuleNumber *int32
	FileName   *string
	LineNumber *int32
	Type       *string
	Database   []string
	UserName   []string
	Address    *string
	Netmask    *string
	AuthMethod *string
	Options    []string
	Error      *string
}

// Source is a stable Driver-owned publication holder. Its zero value is empty.
// Writers copy before locking and never take Driver or Engine locks while locked.
type Source struct {
	mu      sync.RWMutex
	current *Snapshot
}

// Snapshot privately owns immutable facts. It contains no catalog descriptors.
type Snapshot struct{ rules []Rule }

// Set copies the whole batch before publishing it once. The caller must not
// mutate inputs during Set, but owns them again without restriction after return.
func (s *Source) Set(rules []Rule) {
	var next *Snapshot
	if len(rules) != 0 {
		owned := make([]Rule, len(rules))
		for i, rule := range rules {
			owned[i] = Rule{
				RuleNumber: clonePointer(rule.RuleNumber),
				FileName:   clonePointer(rule.FileName),
				LineNumber: clonePointer(rule.LineNumber),
				Type:       clonePointer(rule.Type),
				Database:   cloneStrings(rule.Database),
				UserName:   cloneStrings(rule.UserName),
				Address:    clonePointer(rule.Address),
				Netmask:    clonePointer(rule.Netmask),
				AuthMethod: clonePointer(rule.AuthMethod),
				Options:    cloneStrings(rule.Options),
				Error:      clonePointer(rule.Error),
			}
		}
		next = &Snapshot{rules: owned}
	}
	s.mu.Lock()
	s.current = next
	s.mu.Unlock()
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	owned := make([]string, len(values))
	copy(owned, values)
	return owned
}

// Capture retains immutable backing without copying. A nil source is empty.
func (s *Source) Capture() *Snapshot {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	current := s.current
	s.mu.RUnlock()
	return current
}

// Rules borrows immutable internal backing; consumers must never mutate it.
// Nil snapshots represent an empty rule set.
func (s *Snapshot) Rules() []Rule {
	if s == nil {
		return nil
	}
	return s.rules
}
