package pgcatalog

import (
	"fmt"
	"math"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// These are the attached database's native semantics, not host locale discovery.
const nativeDatabaseProvider = "c"
const nativeDatabaseLocale = "C"
const nativeDatabaseEncodingID = 6
const nativeDatabaseEncodingName = "UTF8"

func DatabaseCollationActualVersion() (*string, error) {
	locale := nativeDatabaseLocale
	return CollationActualVersion(&catalog.Collation{
		Provider: nativeDatabaseProvider, Collate: &locale, CType: &locale,
	})
}

// Caller owns e.mu. Only the supplied catalog and paired role read participate;
// neither session identity nor the live engine catalog replaces captured facts.
func capturePGDatabase(in Input) ([][]sqltype.Value, error) {
	ctx := in.Context
	view := in.Catalog
	roles := in.Roles
	ref := in.Ref
	source := in.Source
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if view.Owner() != in.Owner || view.State() == nil || source.Address() != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1262}) || ref.Domain() != in.Owner.Domain || ref.Epoch != view.State().Epoch() || ref.Address != source.Address() {
		return nil, fmt.Errorf("stuntgres: foreign database source authority")
	}
	typeIDs := [...]sqltype.ID{26, 19, 26, 23, 18, 16, 16, 16, 23, 28, 28, 26, 25, 25, 25, 25, 25, 1034}
	if len(source.Columns) != len(typeIDs) {
		return nil, fmt.Errorf("stuntgres: invalid database source shape")
	}
	for i, id := range typeIDs {
		owned, ok := view.LookupType(id)
		if !ok || owned != source.Columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign database source type")
		}
	}
	if _, ok := view.Database(in.Owner.DatabaseAddress()); !ok {
		return nil, fmt.Errorf("stuntgres: missing current database descriptor")
	}
	var rows [][]sqltype.Value
	for address, descriptor := range view.State().Descriptors() {
		if address.ClassID != ids.DatabaseClassID {
			continue
		}
		database, ok := descriptor.(*catalog.Database)
		if !ok {
			return nil, fmt.Errorf("stuntgres: invalid database descriptor")
		}
		if err := (catalog.DatabaseProvider{}).Validate(view, database); err != nil {
			return nil, err
		}
		if !roles.BelongsTo(in.Owner.Principals) {
			return nil, fmt.Errorf("stuntgres: foreign database owner authority")
		}
		if _, err := roles.Descriptor(database.Owner); err != nil {
			return nil, err
		}
		if database.Owner.ID() > math.MaxUint32 {
			return nil, fmt.Errorf("stuntgres: unrepresentable database owner")
		}
		acl, err := ClassACL(in.Owner, roles, source.Columns[17].Usage.Type, database.ACL)
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
			oid(0, uint32(database.Address().ObjectID)), text(1, database.Name()), oid(2, uint32(database.Owner.ID())),
			checked(sqltype.Integer(source.Columns[3].Usage.Type, nativeDatabaseEncodingID)), text(4, nativeDatabaseProvider), sqltype.Boolean(source.Columns[5].Usage.Type, false), sqltype.Boolean(source.Columns[6].Usage.Type, true),
			sqltype.Boolean(source.Columns[7].Usage.Type, false), checked(sqltype.Integer(source.Columns[8].Usage.Type, -1)), null(9), null(10), null(11),
			text(12, nativeDatabaseLocale), text(13, nativeDatabaseLocale), null(14), null(15), null(16), acl,
		}
		frozen := database.FrozenXID
		if database.Address() == in.Owner.DatabaseAddress() && in.Runtime != nil {
			frozen = in.Runtime.DatabaseFrozenXID()
		}
		if frozen != 0 {
			row[9] = checked(sqltype.XIDValue(source.Columns[9].Usage.Type, uint32(frozen)))
		}
		if failure != nil {
			return nil, failure
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

var builtinCollationVersion = "1"

func CollationActualVersion(d *catalog.Collation) (*string, error) {
	if d.Provider == "d" {
		return DatabaseCollationActualVersion()
	}
	// Version inquiry does not require an executable locale. This build has no
	// ICU version data; admitting ICU execution remains CollationSemantics' job.
	if d.Provider == "i" {
		return nil, nil
	}
	if _, err := catalog.CollationSemantics(d); err != nil {
		return nil, err
	}
	if d.Provider == "b" {
		return &builtinCollationVersion, nil
	}
	return nil, nil
}
