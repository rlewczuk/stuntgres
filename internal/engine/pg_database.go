package engine

import (
	"context"
	"fmt"
	"math"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// These are the attached database's native semantics, not host locale discovery.
const nativeDatabaseProvider = "c"
const nativeDatabaseLocale = "C"

func databaseCollationActualVersion() (*string, error) {
	locale := nativeDatabaseLocale
	return collationActualVersion(&collationDescriptor{
		provider: nativeDatabaseProvider, collate: &locale, ctype: &locale,
	})
}

// Caller owns e.mu. Only the supplied catalog and paired role read participate;
// neither session identity nor the live engine catalog replaces captured facts.
func (e *Engine) capturePGDatabase(ctx context.Context, catalog CatalogRead, roles authorization.Read, ref ids.ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if catalog.owner != e.catalog.owner() || catalog.state == nil || source.address != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1262}) || ref.Domain() != e.catalog.domain || ref.Epoch != catalog.state.epoch || ref.Address != source.address {
		return nil, fmt.Errorf("stuntgres: foreign database source authority")
	}
	ids := [...]sqltype.ID{26, 19, 26, 23, 18, 16, 16, 16, 23, 28, 28, 26, 25, 25, 25, 25, 25, 1034}
	if len(source.Columns) != len(ids) {
		return nil, fmt.Errorf("stuntgres: invalid database source shape")
	}
	for i, id := range ids {
		owned, ok := catalog.LookupType(id)
		if !ok || owned != source.Columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign database source type")
		}
	}
	database, ok := catalog.Database(currentDatabaseAddress)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing current database descriptor")
	}
	if err := (databaseProvider{}).Validate(catalog, database); err != nil {
		return nil, err
	}
	if !roles.BelongsTo(&e.authorizationDomain) {
		return nil, fmt.Errorf("stuntgres: foreign database owner authority")
	}
	if _, err := roles.Descriptor(database.owner); err != nil {
		return nil, err
	}
	if database.owner.ID() > math.MaxUint32 {
		return nil, fmt.Errorf("stuntgres: unrepresentable database owner")
	}
	acl, err := e.pgClassACL(roles, source.Columns[17].Usage.Type, database.acl)
	if err != nil {
		return nil, err
	}
	var failure error
	checked := func(value sqltype.Value, err error) sqltype.Value {
		if failure == nil {
			failure = err
		}
		return value
	}
	oid := func(i int, n uint32) sqltype.Value { return checked(sqltype.OIDValue(source.Columns[i].Usage.Type, n)) }
	text := func(i int, s string) sqltype.Value { return sqltype.String(source.Columns[i].Usage.Type, s) }
	null := func(i int) sqltype.Value { return sqltype.Null(source.Columns[i].Usage.Type) }
	// Unknown database-wide properties remain concrete typed NULLs. UTF8/C is
	// the native semantic declaration, not a host locale or physical horizon.
	row := []sqltype.Value{
		oid(0, uint32(database.address.ObjectID)), text(1, database.name), oid(2, uint32(database.owner.ID())),
		checked(sqltype.Integer(source.Columns[3].Usage.Type, 6)), text(4, nativeDatabaseProvider), null(5), null(6),
		sqltype.Boolean(source.Columns[7].Usage.Type, false), null(8), null(9), null(10), null(11),
		text(12, nativeDatabaseLocale), text(13, nativeDatabaseLocale), null(14), null(15), null(16), acl,
	}
	if failure != nil {
		return nil, failure
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &querySource{ref: ref, anonymous: [][]sqltype.Value{row}}, nil
}
