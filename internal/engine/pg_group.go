package engine

import (
	"context"
	"math"
	"stuntgres/internal/sqltype"
)

// Project only current NOLOGIN identities and their direct membership records
// from the statement's immutable role root. Options and credentials are not read.
func capturePGGroup(ctx context.Context, roles RoleRead, ref ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type group struct {
		name        string
		count, next int
		elements    []sqltype.Value
	}
	groups := make(map[PrincipalRef]group)
	for principal, role := range roles.state.roles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if role.Login || principal.owner != roles.owner || principal.id == 0 || principal.id > math.MaxUint32 || role.Ref != principal {
			continue
		}
		groups[principal] = group{name: role.Name}
	}
	validMember := func(member PrincipalRef) bool {
		if member.owner != roles.owner || member.id == 0 || member.id > math.MaxUint32 {
			return false
		}
		role, ok := roles.state.roles[member]
		return ok && role.Ref == member
	}
	for _, edge := range roles.state.edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		g, ok := groups[edge.Role]
		if !ok || !validMember(edge.Member) {
			continue
		}
		// Lower bound one also needs a representable int32 upper bound.
		if g.count == math.MaxInt32-1 {
			return nil, sqlError("54000", "array size exceeds the maximum allowed")
		}
		g.count++
		groups[edge.Role] = g
	}
	for principal, g := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if g.count != 0 {
			g.elements = make([]sqltype.Value, g.count)
			groups[principal] = g
		}
	}
	for _, edge := range roles.state.edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		g, ok := groups[edge.Role]
		if !ok || !validMember(edge.Member) {
			continue
		}
		element, err := sqltype.OIDValue(source.columns[2].Usage.Type.Element(), uint32(edge.Member.id))
		if err != nil {
			return nil, err
		}
		g.elements[g.next] = element
		g.next++
		groups[edge.Role] = g
	}
	rows := make([][]sqltype.Value, 0, len(groups))
	for principal, g := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id, err := sqltype.OIDValue(source.columns[1].Usage.Type, uint32(principal.id))
		if err != nil {
			return nil, err
		}
		var dimensions []sqltype.ArrayDimension
		if g.count != 0 {
			dimensions = []sqltype.ArrayDimension{{Lower: 1, Length: int32(g.count)}}
		}
		members, err := sqltype.OwnedArrayValue(source.columns[2].Usage.Type, dimensions, g.elements)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{sqltype.String(source.columns[0].Usage.Type, g.name), id, members})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
