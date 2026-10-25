package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
)

func availableUnitSource(e *core.Engine, oid ids.OID) (*catalog.Relation, ids.ObjectRef) {
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
	return e.Catalog.State.MustRelation(a), e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), a)
}
