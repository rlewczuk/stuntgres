package pgcatalog

import (
	"cmp"
	"fmt"
	"math"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Caller holds e.mu. Policy inventory is public catalog data, not an effective
// privilege expansion or a schema-USAGE-filtered list of the actor's grants.
func capturePGDefaultACL(in Input) ([][]sqltype.Value, error) {
	ctx := in.Context
	view := in.Catalog
	roles := in.Roles
	ref := in.Ref
	source := in.Source
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if view.Owner() != in.Owner || view.State() == nil || source == nil || source.Address() != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 826}) || ref.Domain() != in.Owner.Domain || ref.Epoch != view.Epoch() || ref.Address != source.Address() || view.Get(source.Address()) != source {
		return nil, fmt.Errorf("stuntgres: foreign default ACL source authority")
	}
	types := [...]sqltype.ID{26, 26, 26, 18, 1034}
	if len(source.Columns) != len(types) {
		return nil, fmt.Errorf("stuntgres: invalid default ACL source shape")
	}
	for i, id := range types {
		owned, ok := view.LookupType(id)
		if !ok || owned != source.Columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign default ACL source type")
		}
	}
	database, ok := view.Database(catalog.CurrentDatabaseAddress)
	if !ok || database == nil || !roles.BelongsTo(in.Owner.Principals) || roles.DatabaseOwner() != database.Owner {
		return nil, fmt.Errorf("stuntgres: foreign default ACL owner authority")
	}
	if err := catalog.ValidateDefaultACLs(roles, view); err != nil {
		return nil, err
	}
	codes := [...]string{
		authorization.DefaultTables:       "r",
		authorization.DefaultSequences:    "S",
		authorization.DefaultRoutines:     "f",
		authorization.DefaultTypes:        "T",
		authorization.DefaultSchemas:      "n",
		authorization.DefaultLargeObjects: "L",
	}
	rows := make([][]sqltype.Value, 0, view.State().DefaultACLCount())
	for key, record := range view.State().DefaultACLs() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if key.Creator.ID() == 0 || key.Creator.ID() > math.MaxUint32 {
			return nil, fmt.Errorf("stuntgres: unrepresentable default ACL creator")
		}
		namespace := uint32(0)
		if key.Schema != (ids.ObjectAddress{}) {
			if key.Schema.ObjectID == 0 {
				return nil, fmt.Errorf("stuntgres: invalid default ACL namespace")
			}
			namespace = uint32(key.Schema.ObjectID)
		}
		// PostgreSQL stores aclitemsort order, not utility operand order. Normalize
		// only this projection, copying only when shared entries need sorting.
		acl := record.ACL
		if !slices.IsSortedFunc(acl.Entries, defaultACLEntryCompare) {
			acl.Entries = slices.Clone(acl.Entries)
			slices.SortFunc(acl.Entries, defaultACLEntryCompare)
		}
		value, err := ClassACL(in.Owner, roles, source.Columns[4].Usage.Type, acl)
		if err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, 5)
		for i, n := range [...]uint32{uint32(record.OID), uint32(key.Creator.ID()), namespace} {
			row[i], err = sqltype.OIDValue(source.Columns[i].Usage.Type, n)
			if err != nil {
				return nil, err
			}
		}
		row[3] = sqltype.String(source.Columns[3].Usage.Type, codes[key.Class])
		row[4] = value
		rows = append(rows, row)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func defaultACLEntryCompare(a, b authorization.ACLEntry) int {
	grantee := func(e authorization.ACLEntry) uint64 {
		if e.Grantee.Public {
			return 0
		}
		return uint64(e.Grantee.Role.ID())
	}
	if order := cmp.Compare(grantee(a), grantee(b)); order != 0 {
		return order
	}
	if order := cmp.Compare(a.Grantor.ID(), b.Grantor.ID()); order != 0 {
		return order
	}
	// Validated masks are representable in PostgreSQL's released AclMode bits.
	raw := func(e authorization.ACLEntry) uint64 {
		rights, _ := authorization.ACLItemBits(e.Privileges)
		options, _ := authorization.ACLItemBits(e.GrantOptions)
		return uint64(rights) | uint64(options)<<32
	}
	return cmp.Compare(raw(a), raw(b))
}
