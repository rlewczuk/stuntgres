package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Catalog publication already fences descriptor/name predecessors. These
// additional cross-object reads keep method retirement and new consumers atomic
// when another utility publishes between private validation and publication.
// Engine.mu is held; no name resolution, operation callback or allocation occurs.
func accessMethodStageCurrent(stage *CatalogStage, current catalog.Read) bool {
	dependency := func(d catalog.Descriptor) ids.ObjectAddress {
		switch d := d.(type) {
		case *catalog.AccessMethod:
			return ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: d.Handler}
		case *catalog.OperatorFamily:
			return ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: d.Method}
		case *catalog.OperatorClass:
			return ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: d.Method}
		case *catalog.Relation:
			if d.SQLIndex != nil {
				return ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: d.SQLIndex.MethodIdentity()}
			}
		}
		return ids.ObjectAddress{}
	}
	for _, d := range stage.Descriptors() {
		a := dependency(d)
		if a != (ids.ObjectAddress{}) && stage.Get(a) == nil && current.Get(a) != stage.Base.Get(a) {
			return false
		}
	}
	for a := range stage.Removals() {
		if a.ClassID != ids.AccessMethodClassID && (a.ClassID != ids.ProcedureClassID || catalog.NativeHandler(a.ObjectID) == catalog.NoAccessMethod) {
			continue
		}
		for address, d := range current.Descriptors() {
			if d == stage.Base.Get(address) {
				continue
			}
			if dependency(d) == a {
				return false
			}
		}
	}
	return true
}
