package storage

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// IndexEntry is one exact B-tree or logical membership/provenance entry.
type IndexEntry struct {
	Keys     []sqltype.Value
	Row      ids.RowID
	Version  ids.VersionID
	Position int // Physical slot in the simultaneously published immutable storage.
}

// IndexRoot is one published logical index root. Hash keeps buckets in Hash,
// GIN keeps inverted postings, BRIN summaries, and bloom per-entry signatures.
type IndexRoot struct {
	// Construction is the immutable recipe of the last complete build.
	// Incremental writes and transport must preserve it, including recipe debt.
	Construction any          // Opaque index-owner provenance; never executable authority.
	Entries      []IndexEntry // Ordered B-tree keys or unordered logical memberships.
	Hash         *HashRoot
	Brin         *BrinRoot
	Bloom        *BloomRoot
	Gin          *GinRoot
	Membership   map[ids.RowID]ids.VersionID // Immutable; partial roots only.
	RowEntries   map[ids.RowID]int           // Entry ordinals, only checked unique/exclusion expression keys.
	// Collisions retain additional genuine versions without replacing the
	// ordinary one-version-per-row fast paths above.
	MembershipVersions map[ids.VersionID]ids.RowID
	RowEntryVersions   map[ids.VersionID]int
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
