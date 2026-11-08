package enginetest

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

// Comment reads immutable comment metadata from r, a view e owns, without
// applying an ACL to its text. It follows the root fixture Engine.Comment.
// Trigger comments live in relation-owned metadata (there is no descriptor at
// the pg_trigger address), so they bypass object-reference validation.
func Comment(e *core.Engine, r catalog.Read, ref ids.ObjectRef) (string, bool, error) {
	if r.Owner() == nil {
		return "", false, fmt.Errorf("stuntgres: foreign catalog view")
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if ref.Address.ClassID == ids.TriggerClassID {
		text, ok := catalog.TriggerCommentText(r, ids.OID(ref.Address.ObjectID))
		return text, ok, nil
	}
	if err := e.Catalog.ValidateObjectRefLocked(r, ref); err != nil {
		return "", false, err
	}
	a := ref.Address
	sub := a.SubID
	a.SubID = 0
	switch r.Get(a).(type) {
	case *catalog.Namespace, *catalog.Type, *catalog.Relation, *catalog.Procedure:
	default:
		return "", false, fmt.Errorf("stuntgres: object class has no comment metadata")
	}
	text := pgcatalog.DescriptorComment(r.Get(a), sub)
	return text, text != "", nil
}
