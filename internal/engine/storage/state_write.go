package storage

import "stuntgres/internal/engine/ids"

// Store map writes are confined to these methods. Engine callers hold the
// owning Engine.mu, including bootstrap and isolated replacement engines.
// Standalone storage fixtures instead have exclusive ownership of their Store.
// Reads remain direct; detached candidate maps are built by the engine.
//
// Installs retain the supplied pointer and ordinary nil-map assignment behavior.
// Replacements retain the supplied map itself, including nil, in one assignment.
// These methods do not lock, allocate, copy, validate or call back into the engine.

// InstallRelationLocked stores relation at a.
func (s *Store) InstallRelationLocked(a ids.ObjectAddress, relation *Relation) {
	s.Relations[a] = relation
}

// RemoveRelationLocked removes the relation at a.
func (s *Store) RemoveRelationLocked(a ids.ObjectAddress) { delete(s.Relations, a) }

// ReplaceRelationsLocked installs the supplied relation map.
func (s *Store) ReplaceRelationsLocked(relations map[ids.ObjectAddress]*Relation) {
	s.Relations = relations
}

// InstallSequenceLocked stores generator at a.
func (s *Store) InstallSequenceLocked(a ids.ObjectAddress, generator *SequenceGenerator) {
	s.Sequences[a] = generator
}

// RemoveSequenceLocked removes the generator at a.
func (s *Store) RemoveSequenceLocked(a ids.ObjectAddress) { delete(s.Sequences, a) }

// ReplaceSequencesLocked installs the supplied generator map.
func (s *Store) ReplaceSequencesLocked(sequences map[ids.ObjectAddress]*SequenceGenerator) {
	s.Sequences = sequences
}

// InstallIndexRootLocked stores root at a.
func (s *Store) InstallIndexRootLocked(a ids.ObjectAddress, root *IndexRoot) { s.IndexRoots[a] = root }

// RemoveIndexRootLocked removes the root at a.
func (s *Store) RemoveIndexRootLocked(a ids.ObjectAddress) { delete(s.IndexRoots, a) }

// ReplaceIndexRootsLocked installs the supplied index-root map.
func (s *Store) ReplaceIndexRootsLocked(roots map[ids.ObjectAddress]*IndexRoot) { s.IndexRoots = roots }
