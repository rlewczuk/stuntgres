package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Capture only metadata while locked. Planning, source admission, conversion,
// restart and teardown all run from internal demand after the mutex is released.
func (e *Engine) captureForeignSourceLocked(scope *StatementScope, ref ids.ObjectRef, relation *catalog.Relation, invoker authorization.PrincipalRef, uses []int, mark plan.RowMark) (*exec.Source, error) {
	if mark.Mode != 0 {
		return nil, sqlError("0A000", "foreign row locking is unsupported in embedded mode")
	}
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return nil, err
	}
	if invoker != (authorization.PrincipalRef{}) {
		auth.State.CurrentUser = invoker
	}
	server, ok := auth.Catalog.Foreign(relation.Foreign.Server)
	if !ok {
		return nil, ids.ErrCatalogReplaced
	}
	wrapper, ok := auth.Catalog.Foreign(server.Wrapper)
	if !ok {
		return nil, ids.ErrCatalogReplaced
	}
	handler, err := foreignHandlerFor(auth.Catalog, wrapper)
	if err != nil {
		return nil, err
	}
	mapping, err := foreignMappingFor(auth.View, server)
	if err != nil {
		return nil, err
	}
	role, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return nil, err
	}
	request := catalog.ForeignRequest{Schema: auth.Catalog.Get(relation.Namespace()).Name(), Table: relation.Name(), Role: role.Name, TableOID: uint32(relation.Address().ObjectID), RoleOID: uint32(role.Ref.ID()), WrapperOptions: slices.Clone(wrapper.Options), ServerOptions: slices.Clone(server.Options), TableOptions: slices.Clone(relation.Foreign.Options), MappingOptions: slices.Clone(mapping.Options)}
	request.ServerOID = uint32(server.Address().ObjectID)
	request.MappingOID = uint32(mapping.Address().ObjectID)
	request.HandlerOID = uint32(wrapper.Handler.ObjectID)
	for i, column := range relation.Columns {
		request.Columns = append(request.Columns, catalog.ForeignColumn{Attribute: relation.Attributes[i], Name: column.Name, TypeName: column.Usage.Type.Name(), TypeOID: uint32(column.Usage.Type.ID()), TypeModifier: column.Usage.Typmod, CollationOID: column.Usage.Collation.ID, Nullable: column.Nullable, Options: slices.Clone(relation.Foreign.Columns[relation.Attributes[i]])})
	}
	request.RequiredAttributes, err = foreignRequiredAttributes(relation, uses)
	if err != nil {
		return nil, err
	}
	importer, exporter, ok := e.foreign.codecs()
	if !ok {
		return nil, fmt.Errorf("stuntgres: foreign driver value codecs are not installed")
	}
	f := &exec.ForeignQuerySource{Wrapper: handler.Wrapper, Request: request, Columns: slices.Clone(relation.Columns), ImportValue: importer, ExportValue: exporter}
	scope.ownForeignSource(f)
	return &exec.Source{Ref: ref, Anonymous: [][]sqltype.Value{}, Foreign: f}, nil
}

// Logical virtual-column ACL uses remain unchanged. Only their existing bound
// expression's base inputs belong in the external row layout.
func foreignRequiredAttributes(relation *catalog.Relation, uses []int) ([]int32, error) {
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
