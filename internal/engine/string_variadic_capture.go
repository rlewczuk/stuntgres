package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
)

// Definition tokens, not unrelated catalog revisions, retire stored origins.
// The stable case borrows the entire published group without allocating.
func pruneQuoteCaptures(g *exec.QuoteCaptureCandidate, view catalog.Read) {
	valid := func(origin exec.QuoteOrigin) bool {
		if origin.Owner == (ids.ObjectRef{}) {
			return true
		}
		if view.Owner() == nil || origin.Owner.Domain() != view.Owner().Domain || origin.Owner.Epoch != view.Epoch() {
			return false
		}
		d, ok := view.Relation(origin.Owner.Address)
		if !ok {
			return false
		}
		if origin.Attribute != 0 {
			return d.ColumnMeta.Defaults[origin.Attribute].OID == origin.Definition
		}
		return d.View != nil && viewOf(d).RuleOID() == origin.Definition && viewOf(d).QuoteRevision() == origin.Revision
	}
	for i, entry := range g.Entries {
		if valid(entry.Origin) {
			continue
		}
		next := make([]exec.QuoteConstant, 0, len(g.Entries)-1)
		next = append(next, g.Entries[:i]...)
		for _, remaining := range g.Entries[i+1:] {
			if valid(remaining.Origin) {
				next = append(next, remaining)
			}
		}
		g.Entries, g.Private = next, true
		return
	}
}
