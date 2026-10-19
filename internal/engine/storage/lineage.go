package storage

// TruncateLineage is a nonzero-size token that identifies only uninterrupted
// TRUNCATE incarnations. Engine.mu protects predecessor stamping and all
// token comparisons. Tokens retain neither old storage nor row vectors;
// ordinary replacements break them. Allocate one with new or &TruncateLineage{}.
type TruncateLineage struct{ marker byte }

// SameTruncateLineage reports whether a and b are incarnations of one
// uninterrupted TRUNCATE lineage. Either may be nil.
func (a *Relation) SameTruncateLineage(b *Relation) bool {
	return a != nil && b != nil && a.TruncateLineage != nil && a.TruncateLineage == b.TruncateLineage
}

// MaterializedLineage is the concurrent materialized-view refresh token.
// Tokens are accessed only under Engine.mu. They retain no storage or row
// vector; a nonconcurrent rewrite/control replacement breaks provenance.
type MaterializedLineage struct{ marker byte }
