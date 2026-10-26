package stored

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
)

// PruneFoldCaptures drops typed folds whose stored origin (a column
// default or a view definition) was retired in view. Definition tokens, not
// unrelated catalog revisions, retire stored origins. The stable case
// borrows the entire published group without allocating.
func PruneFoldCaptures(g *exec.FoldCaptureCandidate, view catalog.Read) {
	valid := func(origin exec.FoldOrigin) bool {
		if origin.Owner == (ids.ObjectRef{}) {
			return true
		}
		if view.Owner() == nil || origin.Owner.Domain() != view.Owner().Domain || origin.Owner.Epoch != view.Epoch() {
			return false
		}
		d := view.Get(origin.Owner.Address)
		switch owner := d.(type) {
		case *catalog.Relation:
			if origin.Attribute < 0 {
				if owner.Rules == nil {
					return false
				}
				definition := owner.Rules.RoutineRecipe(origin.Definition, int(-origin.Attribute-2))
				return definition != nil && definition.DefinitionRevision() == origin.Revision
			}
			if origin.Attribute != 0 {
				return owner.ColumnMeta.Defaults[origin.Attribute].OID == origin.Definition
			}
			return owner.View != nil && owner.View.RuleOID() == origin.Definition && owner.View.DefinitionRevision() == origin.Revision
		case *catalog.Procedure, *catalog.Type:
			return d.Revision() == origin.Revision
		}
		return false
	}
	for i, entry := range g.Entries {
		if valid(entry.Origin) {
			continue
		}
		next := make([]exec.FoldConstant, 0, len(g.Entries)-1)
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
