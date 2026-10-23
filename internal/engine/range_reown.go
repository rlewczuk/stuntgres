package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

// Called with Engine.mu held, before identity relocation. Select genuine live
// support from the candidate and rebuild its immutable capture with destination
// identities and subtype generations; no source comparison plan is transplanted.
func rangeTypeRebinder(next *catalog.State, mapping map[ids.ObjectAddress]ids.ObjectAddress) func(sqltype.RangeDefinition) (sqltype.RangeDefinition, error) {
	return func(definition sqltype.RangeDefinition) (sqltype.RangeDefinition, error) {
		view := catalog.NewRead(nil, next)
		class, ok := view.IndexClass(ids.OID(definition.Class.OID()))
		if !ok {
			return definition, fmt.Errorf("stuntgres: missing imported range class")
		}
		class, err := routine.BindClassSupport(view, class)
		if err != nil {
			return definition, err
		}
		mapped := func(class ids.OID, oid uint32) uint32 {
			if to, ok := mapping[ids.ObjectAddress{ClassID: class, ObjectID: ids.OID(oid)}]; ok {
				return uint32(to.ObjectID)
			}
			return oid
		}
		capture := sqltype.NewIndexClass(sqltype.IndexClassDefinition{OID: mapped(ids.OperatorClassCatalogID, class.OID()), Method: mapped(ids.AccessMethodClassID, class.MethodOID()), Family: mapped(ids.OperatorFamilyClassID, class.FamilyOID()), Input: mapped(ids.TypeClassID, class.InputOID()), Storage: mapped(ids.TypeClassID, class.StorageOID()), Name: class.Name, FamilyName: class.Family, Hash: class.IsHash(), GiST: class.IsGiST()})
		support := class.Support(1)
		support.Family = mapped(ids.OperatorFamilyClassID, support.Family)
		support.Member = mapped(ids.AccessMethodProcedureClassID, support.Member)
		support.Routine = mapped(ids.ProcedureClassID, support.Routine)
		support.Left = mapped(ids.TypeClassID, support.Left)
		support.Right = mapped(ids.TypeClassID, support.Right)
		capture = capture.WithSupport(1, support)
		definition.Class = capture
		definition.Ordering, err = sqltype.BindIndexOrdering(definition.Subtype, capture)
		definition.Diff = mapped(ids.ProcedureClassID, definition.Diff)
		return definition, err
	}
}

