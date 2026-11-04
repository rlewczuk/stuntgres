package catalog

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type GroupingRoutineCallback struct {
	Type             *sqltype.Descriptor
	Procedure        *Procedure
	Reference, Class ids.ObjectRef
	Support          sqltype.IndexSupport
	Hash             bool
	Occurrence       uint32
	ExpressionSource expr.FoldSourceOccurrence
	IndexEquality    *IndexEqualityCallback
}

func (c GroupingRoutineCallback) GroupingCallbackType() *sqltype.Descriptor { return c.Type }
func (c GroupingRoutineCallback) Validate(view Read) error {
	if c.Type == nil || c.Procedure == nil || view.Owner() == nil || c.Reference.Domain() != view.Domain() || c.Reference.Epoch != view.Epoch() || c.Reference.Address != c.Procedure.Address() {
		return ids.ErrCatalogReplaced
	}
	typ, ok := view.LookupType(c.Type.ID())
	if !ok || typ != c.Type {
		return ids.ErrCatalogReplaced
	}
	if c.IndexEquality != nil {
		if c.Class.Domain() != view.Domain() || c.Class.Epoch != view.Epoch() {
			return ids.ErrCatalogReplaced
		}
		return c.IndexEquality.validate(view, c)
	}
	if c.Hash {
		if c.Class.Domain() != view.Domain() || c.Class.Epoch != view.Epoch() {
			return ids.ErrCatalogReplaced
		}
		class, ok := view.IndexClass(c.Class.Address.ObjectID)
		if !ok || !class.IsHash() || c.Support.Class != uint32(c.Class.Address.ObjectID) || c.Support.Routine != uint32(c.Procedure.Address().ObjectID) {
			return fmt.Errorf("stuntgres: stale grouping hash support")
		}
		if _, err := CaptureIndexRoutineCallback(view, c.Support, 2); err != nil {
			return err
		}
	}
	return nil
}
