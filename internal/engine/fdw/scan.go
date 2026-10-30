package fdw

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/settings"
)

// ScanRequest resolves relation's server, wrapper, handler and user
// mapping, and builds the scan request. A non-zero invoker replaces the
// view's current user. It reads only the captured view and calls no wrapper.
func ScanRequest(view access.View, invoker authorization.PrincipalRef, relation *catalog.Relation, uses []int, restricted settings.RestrictedRelationKinds) (*catalog.ForeignProcedure, catalog.ForeignRequest, error) {
	if invoker != (authorization.PrincipalRef{}) {
		view.State.CurrentUser = invoker
	}
	server, ok := view.Catalog.Foreign(relation.Foreign.Server)
	if !ok {
		return nil, catalog.ForeignRequest{}, ids.ErrCatalogReplaced
	}
	wrapper, ok := view.Catalog.Foreign(server.Wrapper)
	if !ok {
		return nil, catalog.ForeignRequest{}, ids.ErrCatalogReplaced
	}
	handler, err := HandlerFor(view.Catalog, wrapper, restricted)
	if err != nil {
		return nil, catalog.ForeignRequest{}, err
	}
	mapping, err := MappingFor(view, server)
	if err != nil {
		return nil, catalog.ForeignRequest{}, err
	}
	role, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return nil, catalog.ForeignRequest{}, err
	}
	request := catalog.ForeignRequest{Schema: view.Catalog.Get(relation.Namespace()).Name(), Table: relation.Name(), Role: role.Name, TableOID: uint32(relation.Address().ObjectID), RoleOID: uint32(role.Ref.ID()), WrapperOptions: slices.Clone(wrapper.Options), ServerOptions: slices.Clone(server.Options), TableOptions: slices.Clone(relation.Foreign.Options), MappingOptions: slices.Clone(mapping.Options)}
	request.ServerOID = uint32(server.Address().ObjectID)
	request.MappingOID = uint32(mapping.Address().ObjectID)
	request.HandlerOID = uint32(wrapper.Handler.ObjectID)
	for i, column := range relation.Columns {
		request.Columns = append(request.Columns, catalog.ForeignColumn{Attribute: relation.Attributes[i], Name: column.Name, TypeName: column.Usage.Type.Name(), TypeOID: uint32(column.Usage.Type.ID()), TypeModifier: column.Usage.Typmod, CollationOID: column.Usage.Collation.ID, Nullable: column.Nullable, Options: slices.Clone(relation.Foreign.Columns[relation.Attributes[i]])})
	}
	request.RequiredAttributes, err = RequiredAttributes(relation, uses)
	if err != nil {
		return nil, catalog.ForeignRequest{}, err
	}
	return handler, request, nil
}

// Logical virtual-column ACL uses remain unchanged. Only their existing bound
// expression's base inputs belong in the external row layout.
func RequiredAttributes(relation *catalog.Relation, uses []int) ([]int32, error) {
	count := len(uses)
	if uses == nil {
		count = len(relation.Columns)
	}
	required := make([]int32, 0, count)
	add := func(slot int) {
		attribute := relation.Attributes[slot]
		if !slices.Contains(required, attribute) {
			required = append(required, attribute)
		}
	}
	for i := range count {
		slot := i
		if uses != nil {
			slot = uses[i]
		}
		if slot < 0 || slot >= len(relation.Attributes) {
			return nil, fmt.Errorf("stuntgres: invalid foreign required attribute")
		}
		if generation, ok := relation.ColumnMeta.Generations[relation.Attributes[slot]]; ok && generation.Kind == catalog.GenerationVirtual {
			plan.WalkExpression(generation.Planned, func(node expr.Expr) {
				if column, ok := node.(plan.ColumnExpr); ok {
					add(column.Slot)
				}
			})
		} else {
			add(slot)
		}
	}
	return required, nil
}
