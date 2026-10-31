package storage

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// IndexEntry is one logical B-tree or GiST membership entry.
type IndexEntry struct {
	Keys     []sqltype.Value
	Row      ids.RowID
	Version  ids.VersionID
	Position int // Physical slot in the simultaneously published immutable storage.
}

// IndexRoot is one published logical index root. Hash indexes keep buckets in
// Hash, BRIN keeps range summaries in Brin, and B-tree/GiST use Entries.
type IndexRoot struct {
	Entries    []IndexEntry // Ordered B-tree keys or unordered logical GiST membership.
	Hash       *HashRoot
	Brin       *BrinRoot
	Membership map[ids.RowID]ids.VersionID // Immutable; partial roots only.
	RowEntries map[ids.RowID]int           // Entry ordinals, only checked unique/exclusion expression keys.
}

// IndexOutput is an already produced index membership/key, not a CHECK
// result. Only immutable descriptor authority and physical index data cross
// publication.
type IndexOutput struct {
	Index  *catalog.Relation
	Member bool
	Keys   []sqltype.Value
	Key    sqltype.Value // Hash indexes retain one scalar, not a one-element slice.
}

