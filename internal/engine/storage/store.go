// Package storage holds the engine's physical data layer: committed relation
// storage and row versions, deletion roots, logical B-tree and hash index
// roots, sequence generator state and the store that owns them. See
// README.md for the sharing and locking contract.
package storage

import (
	"errors"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Store owns the engine's physical data state: committed relation
// storage, sequence generators, published index roots and the row and
// version identity allocators. It has no mutex. Engine.mu guards every field,
// and mutating methods carry the Locked suffix.
//
// Engine holds the store by value and never replaces it whole; publication
// swaps its maps under Engine.mu together with the catalog state. Published
// storage and index roots are immutable and shared copy-on-write, so
// snapshots retain them by pointer and never copy row values. Generator
// state here is distinct from transactional sequence metadata in the
// catalog.
type Store struct {
	Relations map[ids.ObjectAddress]*Relation
	// RelationMetadata contains immutable ANALYZE records for all relation
	// kinds, including partitioned parents that have no physical Relation.
	// Engine.mu guards replacement of records and map lifecycle.
	RelationMetadata map[ids.ObjectAddress]*RelationMetadata
	Sequences        map[ids.ObjectAddress]*SequenceGenerator
	IndexRoots       map[ids.ObjectAddress]*IndexRoot
	nextRow          uint64
	nextVersion      uint64
}

// AllocateRowVersionsLocked reserves one new row and version identity per
// row, in order. Ordinary writes and initial catalog data both reserve
// globally nonreused identities; failed work may leave gaps.
func (s *Store) AllocateRowVersionsLocked(rows [][]sqltype.Value, admission *catalog.State) ([]RowVersion, error) {
	n := uint64(len(rows))
	if n > ^uint64(0)-s.nextRow {
		return nil, errors.New("stuntgres: row identity exhausted")
	}
	if n > ^uint64(0)-s.nextVersion {
		return nil, errors.New("stuntgres: version identity exhausted")
	}
	versions := make([]RowVersion, len(rows))
	for i, row := range rows {
		s.nextRow++
		s.nextVersion++
		versions[i] = RowVersion{Row: ids.RowID(s.nextRow), Version: ids.VersionID(s.nextVersion), Values: row, IndexAdmission: admission}
	}
	return versions, nil
}

// IndexRootsCurrent reports whether every captured root is still the
// published index root at its address. The caller holds Engine.mu.
func (s *Store) IndexRootsCurrent(roots map[ids.ObjectAddress]*IndexRoot) bool {
	for address, root := range roots {
		if s.IndexRoots[address] != root {
			return false
		}
	}
	return true
}

// RowFrontier is the last allocated row identity.
func (s *Store) RowFrontier() uint64 { return s.nextRow }

// VersionFrontier is the last allocated version identity.
func (s *Store) VersionFrontier() uint64 { return s.nextVersion }

// AllocateRowLocked reserves the next row identity. The caller holds
// Engine.mu and has already checked exhaustion with its own diagnostic.
func (s *Store) AllocateRowLocked() ids.RowID {
	s.nextRow++
	return ids.RowID(s.nextRow)
}

// AllocateVersionLocked reserves the next version identity. The caller holds
// Engine.mu and has already checked exhaustion with its own diagnostic.
func (s *Store) AllocateVersionLocked() ids.VersionID {
	s.nextVersion++
	return ids.VersionID(s.nextVersion)
}

// SetFrontierLocked installs both allocator frontiers, for Restore and
// foreign replacement after reminting succeeded. The caller holds Engine.mu.
func (s *Store) SetFrontierLocked(row, version uint64) {
	s.nextRow, s.nextVersion = row, version
}
