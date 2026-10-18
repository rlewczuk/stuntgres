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
	if catalog.owner != e.catalog.owner() || catalog.state == nil || source == nil || source.Address() != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.NamespaceClassID}) || ref.Domain() != e.catalog.Domain || ref.Epoch != catalog.Epoch() || ref.Address != source.Address() || catalog.Get(source.Address()) != source {
		return nil, fmt.Errorf("stuntgres: foreign namespace source authority")
	}
	ids := [...]sqltype.ID{26, 19, 26, 1034}
	if len(source.Columns) != len(ids) {
		return nil, fmt.Errorf("stuntgres: invalid namespace source shape")
	}
	for i, id := range ids {
		owned, ok := catalog.LookupType(id)
		if !ok || owned != source.Columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign namespace source type")
		}
	}
	database, ok := catalog.Database(currentDatabaseAddress)
	if !ok || database == nil || !roles.BelongsTo(&e.authorizationDomain) || roles.DatabaseOwner() != database.Owner {
		return nil, fmt.Errorf("stuntgres: foreign namespace owner authority")
	}
	count := 0
	for _, d := range catalog.Descriptors() {
		if _, ok := d.(*namespaceDescriptor); ok {
			count++
		}
	}
	rows := make([][]sqltype.Value, 0, count)
	for address, d := range catalog.Descriptors() {
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
		acl, err := e.pgClassACL(roles, source.Columns[3].Usage.Type, ns.ACL)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{oid, sqltype.String(source.Columns[1].Usage.Type, ns.Name()), owner, acl})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
