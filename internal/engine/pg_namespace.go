package engine

import (
	"context"
	"fmt"
	"math"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Caller owns e.mu. Catalog and paired roles are immutable captured authority;
// inventory visibility is independent of the actor's schema USAGE privileges.
func (e *Engine) capturePGNamespace(ctx context.Context, catalog CatalogRead, roles authorization.Read, ref ids.ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if catalog.owner != e || catalog.state == nil || source == nil || source.address != (ids.ObjectAddress{ClassID: RelationClassID, ObjectID: NamespaceClassID}) || ref.Domain() != e.domain || ref.Epoch != catalog.Epoch() || ref.Address != source.address || catalog.state.descriptors[source.address] != source {
		return nil, fmt.Errorf("stuntgres: foreign namespace source authority")
	}
	ids := [...]sqltype.ID{26, 19, 26, 1034}
	if len(source.columns) != len(ids) {
		return nil, fmt.Errorf("stuntgres: invalid namespace source shape")
	}
	for i, id := range ids {
		owned, ok := catalog.LookupType(id)
		if !ok || owned != source.columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign namespace source type")
		}
	}
	database, ok := catalog.state.descriptors[currentDatabaseAddress].(*databaseDescriptor)
	if !ok || database == nil || !roles.BelongsTo(&e.authorizationDomain) || roles.DatabaseOwner() != database.owner {
		return nil, fmt.Errorf("stuntgres: foreign namespace owner authority")
	}
	count := 0
	for _, d := range catalog.state.descriptors {
		if _, ok := d.(*namespaceDescriptor); ok {
			count++
		}
	}
	rows := make([][]sqltype.Value, 0, count)
	for address, d := range catalog.state.descriptors {
		ns, ok := d.(*namespaceDescriptor)
		if !ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := (namespaceProvider{}).Validate(catalog, ns); err != nil {
			return nil, err
		}
		if address != ns.address {
			return nil, fmt.Errorf("stuntgres: invalid namespace address authority")
		}
		if _, err := roles.Descriptor(ns.owner); err != nil {
			return nil, err
		}
		if ns.owner.ID() > math.MaxUint32 {
			return nil, fmt.Errorf("stuntgres: unrepresentable namespace owner")
		}
		oid, err := sqltype.OIDValue(source.columns[0].Usage.Type, uint32(ns.address.ObjectID))
		if err != nil {
			return nil, err
		}
		owner, err := sqltype.OIDValue(source.columns[2].Usage.Type, uint32(ns.owner.ID()))
		if err != nil {
			return nil, err
		}
		acl, err := e.pgClassACL(roles, source.columns[3].Usage.Type, ns.acl)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{oid, sqltype.String(source.columns[1].Usage.Type, ns.name), owner, acl})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
