package schemacmds

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqlstate"
)

// Composite uses the same allocator and reciprocal row-type construction as
// CREATE TYPE, without SQL reentry while the extension stage mutex is held.
func (h *extensionHost) Composite(ns ids.ObjectAddress, name string, owner authorization.PrincipalRef, columns []catalog.Column, membership ids.ObjectAddress) (*catalog.Type, *catalog.Type, error) {
	available, err := h.DisplaceAssociatedArray(ns, name)
	if err != nil {
		return nil, nil, err
	}
	if !available {
		return nil, nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: fmt.Sprintf("type %q already exists", name)}
	}
	if h.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: name}) != (ids.ObjectAddress{}) {
		return nil, nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P07", Message: fmt.Sprintf("relation %q already exists", name)}
	}
	index := make(map[string]int, len(columns))
	for i, column := range columns {
		index[column.Name] = i
	}
	relation, err := table.NewRelation(h, ns, name, owner, columns, index)
	if err != nil {
		return nil, nil, err
	}
	relation.RelationKind = catalog.CompositeRelation
	if err := h.Engine.StageRowTypeLocked(h.Stage, relation); err != nil {
		return nil, nil, err
	}
	if err := h.Create(relation); err != nil {
		return nil, nil, err
	}
	view := h.Engine.StageRead(h.Stage)
	typ := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)})
	array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.Array)})
	// These descriptors were allocated in this private stage, not borrowed from
	// its base. Set their membership before any catalog publication.
	typ.Extension, array.Extension = membership, membership
	return typ, array, nil
}
