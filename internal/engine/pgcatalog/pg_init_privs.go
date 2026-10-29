package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Initial privilege inventory is public; ordinary relation/column SELECT
// admission is enforced by the catalog source binder, not by filtering rows.
func capturePGInitPrivs(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	view, source, roles := in.Catalog, in.Source, in.Roles
	if in.Owner == nil || view.Owner() != in.Owner || view.State() == nil || source == nil || source.Address() != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 3394}) || in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != view.Epoch() || in.Ref.Address != source.Address() || view.Get(source.Address()) != source {
		return nil, fmt.Errorf("stuntgres: foreign initial privilege source authority")
	}
	types := [...]sqltype.ID{26, 26, 23, 18, 1034}
	if len(source.Columns) != len(types) {
		return nil, fmt.Errorf("stuntgres: invalid initial privilege source shape")
	}
	for i, id := range types {
		owned, ok := view.LookupType(id)
		if !ok || owned != source.Columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign initial privilege source type")
		}
	}
	database, ok := view.Database(in.Owner.DatabaseAddress())
	if !ok || database == nil || !roles.BelongsTo(in.Owner.Principals) || roles.DatabaseOwner() != database.Owner {
		return nil, fmt.Errorf("stuntgres: foreign initial privilege owner authority")
	}
	if err := catalog.ValidateInitialPrivileges(roles, view); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0, view.State().InitialPrivilegeCount())
	for address, record := range view.State().InitialPrivileges() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		acl, err := ClassACL(in.Owner, roles, source.Columns[4].Usage.Type, record.ACL)
		if err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, 5)
		row[0], err = sqltype.OIDValue(source.Columns[0].Usage.Type, uint32(address.ObjectID))
		if err != nil {
			return nil, err
		}
		row[1], err = sqltype.OIDValue(source.Columns[1].Usage.Type, uint32(address.ClassID))
		if err != nil {
			return nil, err
		}
		row[2], err = sqltype.Integer(source.Columns[2].Usage.Type, int64(address.SubID))
		if err != nil {
			return nil, err
		}
		row[3] = sqltype.String(source.Columns[3].Usage.Type, string(record.PrivType))
		row[4] = acl
		rows = append(rows, row)
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
