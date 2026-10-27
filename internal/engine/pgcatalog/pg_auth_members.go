package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"

	"stuntgres/internal/sqltype"
)

const AuthMembersOID = 1261

// Project the captured direct edges after validating the ordinary producer input.
func capturePGAuthMembers(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	view, source := in.Catalog, in.Source
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: AuthMembersOID}
	if in.Owner == nil || view.Owner() != in.Owner || view.State() == nil || source == nil ||
		source.Address() != address || source.RelationKind != catalog.CatalogRelation ||
		in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != view.Epoch() ||
		in.Ref.Address != address || view.Get(address) != source ||
		!in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: foreign membership source authority")
	}
	names := [...]string{"oid", "roleid", "member", "grantor", "admin_option", "inherit_option", "set_option"}
	if len(source.Columns) != len(names) {
		return nil, fmt.Errorf("stuntgres: invalid membership source shape")
	}
	for i, name := range names {
		id := sqltype.Oid
		if i >= 4 {
			id = sqltype.Bool
		}
		typ, ok := view.LookupType(id)
		if !ok || source.Columns[i].Usage.Type != typ || source.Columns[i].Name != name {
			return nil, fmt.Errorf("stuntgres: foreign membership source type")
		}
	}
	rows := make([][]sqltype.Value, 0)
	for edge := range in.Roles.Edges() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if edge.OID == 0 || !in.Roles.GroupMember(edge.Role) || !in.Roles.GroupMember(edge.Member) || !in.Roles.GroupMember(edge.Grantor) {
			return nil, fmt.Errorf("stuntgres: invalid stored membership identity")
		}
		row := make([]sqltype.Value, 7)
		for i, id := range [...]uint32{edge.OID, uint32(edge.Role.ID()), uint32(edge.Member.ID()), uint32(edge.Grantor.ID())} {
			value, err := sqltype.OIDValue(in.Source.Columns[i].Usage.Type, id)
			if err != nil {
				return nil, err
			}
			row[i] = value
		}
		row[4] = sqltype.Boolean(in.Source.Columns[4].Usage.Type, edge.Admin)
		row[5] = sqltype.Boolean(in.Source.Columns[5].Usage.Type, edge.Inherit)
		row[6] = sqltype.Boolean(in.Source.Columns[6].Usage.Type, edge.Set)
		rows = append(rows, row)
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
