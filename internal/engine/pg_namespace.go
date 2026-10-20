package engine

import (
	"fmt"
	"math"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Caller owns e.mu. Catalog and paired roles are immutable captured authority;
// inventory visibility is independent of the actor's schema USAGE privileges.
func capturePGNamespace(in Input) ([][]sqltype.Value, error) {
	ctx := in.Context
	view := in.Catalog
	roles := in.Roles
	ref := in.Ref
	source := in.Source
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if view.Owner() != in.Owner || view.State() == nil || source == nil || source.Address() != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.NamespaceClassID}) || ref.Domain() != in.Owner.Domain || ref.Epoch != view.Epoch() || ref.Address != source.Address() || view.Get(source.Address()) != source {
		return nil, fmt.Errorf("stuntgres: foreign namespace source authority")
	}
	ids := [...]sqltype.ID{26, 19, 26, 1034}
	if len(source.Columns) != len(ids) {
		return nil, fmt.Errorf("stuntgres: invalid namespace source shape")
	}
	for i, id := range ids {
		owned, ok := view.LookupType(id)
		if !ok || owned != source.Columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign namespace source type")
		}
	}
	database, ok := view.Database(catalog.CurrentDatabaseAddress)
	if !ok || database == nil || !roles.BelongsTo(in.Owner.Principals) || roles.DatabaseOwner() != database.Owner {
		return nil, fmt.Errorf("stuntgres: foreign namespace owner authority")
	}
	count := 0
	for _, d := range view.Descriptors() {
		if _, ok := d.(*catalog.Namespace); ok {
			count++
		}
	}
	rows := make([][]sqltype.Value, 0, count)
	for address, d := range view.Descriptors() {
		ns, ok := d.(*catalog.Namespace)
		if !ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := (catalog.NamespaceProvider{}).Validate(view, ns); err != nil {
			return nil, err
		}
		if address != ns.Address() {
			return nil, fmt.Errorf("stuntgres: invalid namespace address authority")
		}
		if _, err := roles.Descriptor(ns.Owner); err != nil {
			return nil, err
		}
		if ns.Owner.ID() > math.MaxUint32 {
			return nil, fmt.Errorf("stuntgres: unrepresentable namespace owner")
		}
		oid, err := sqltype.OIDValue(source.Columns[0].Usage.Type, uint32(ns.Address().ObjectID))
		if err != nil {
			return nil, err
		}
		owner, err := sqltype.OIDValue(source.Columns[2].Usage.Type, uint32(ns.Owner.ID()))
		if err != nil {
			return nil, err
		}
		acl, err := ClassACL(in.Owner, roles, source.Columns[3].Usage.Type, ns.ACL)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{oid, sqltype.String(source.Columns[1].Usage.Type, ns.Name()), owner, acl})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
