package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The typedef.StageHost methods of ddlHost beyond the shared ddl.Host,
// including ddl.TypeStaging.

var _ typedef.StageHost = ddlHost{}

func (h ddlHost) TrustedRestoreDestination(a ddl.Authority, what string) (bool, error) {
	if h.op == nil || !operationReplayOrZero(h.op).tableDestination {
		return false, nil
	}
	auth := ddlAuthorityOf(a)
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	if err != nil || !bootstrap.Superuser || h.e.importTargets == nil {
		return true, fmt.Errorf("stuntgres: invalid trusted restore %s authority", what)
	}
	return true, nil
}

func (h ddlHost) AllocateEnumMember() (int64, error) {
	return h.e.catalog.AllocateEnumMemberLocked()
}

func (h ddlHost) ReplaceUnsynchronized(d catalog.Descriptor) error {
	return h.stage.replaceDescriptor(d, false, false)
}

func (h ddlHost) ReplaceDomainIdentity(d *catalog.Type) error {
	return h.stage.replaceDescriptor(d, false, true)
}

func (h ddlHost) CreationTypeACL(owner authorization.PrincipalRef, schema ids.ObjectAddress) (authorization.ACLValue, bool) {
	return h.stage.CreationTypeACL(owner, schema)
}

func (h ddlHost) AddDomainBase(d catalog.Descriptor) {
	h.stage.domainBases = append(h.stage.domainBases, d)
}

func (h ddlHost) RemoveDomainCheck(d *catalog.Type, oid ids.OID) error {
	return table.RemoveDomainCheck(stageDropProvider(nil, h.stage, nil, nil, nil), d, oid)
}

func (h ddlHost) NamedType(a ddl.Authority, name parser.ObjectName) (*catalog.Type, error) {
	return h.e.namedType(ddlAuthorityOf(a), name)
}

func (h ddlHost) FindCollation(a ddl.Authority, name parser.ObjectName) (*catalog.Collation, error) {
	return h.e.findCollation(ddlAuthorityOf(a), name)
}

func (h ddlHost) ResolveType(raw parser.TypeName) (sqltype.Usage, error) {
	return h.e.resolveType(raw, h.op)
}

func (h ddlHost) ResolveDomainBase(raw parser.TypeName) (sqltype.Usage, error) {
	return h.e.resolveAnnotationTypeMode(raw, h.op, false, true)
}

func (h ddlHost) ResolveColumnCollation(u sqltype.Usage, raw *parser.ObjectName) (sqltype.Usage, error) {
	return h.e.resolveColumnCollation(u, raw, h.op)
}

func (h ddlHost) BindDomainDefault(a ddl.Authority, d *catalog.Type, raw parser.Expr, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) error {
	if restored, err := h.bindRestoredDomainDefault(a, d, raw, input, output); restored || err != nil {
		return err
	}
	return h.e.bindDomainDefault(h.op.Context(), h.op, ddlAuthorityOf(a), h.stage, d, raw, input, output)
}

func (h ddlHost) BindDomainConstraints(a ddl.Authority, d *catalog.Type, raw []parser.ColumnConstraint, input sqltype.InputEnvironment) error {
	return h.e.bindDomainConstraintDefinitions(h.op.Context(), h.op, ddlAuthorityOf(a), h.stage, d, raw, input)
}

func (h ddlHost) DisplaceAssociatedArray(namespace ids.ObjectAddress, name string) (bool, error) {
	return h.e.displaceAssociatedArrayLocked(h.stage, namespace, name)
}

func (h ddlHost) AssociatedArrayName(namespace ids.ObjectAddress, name string) string {
	return associatedArrayName(h.stage, namespace, name)
}

func (h ddlHost) NewCompositeRelation(namespace ids.ObjectAddress, name string, owner authorization.PrincipalRef, columns []catalog.Column, index map[string]int) (*catalog.Relation, error) {
	relation, err := table.NewRelation(h, namespace, name, owner, columns, index)
	if err != nil {
		return nil, err
	}
	relation.RelationKind = catalog.CompositeRelation
	return relation, nil
}

func (h ddlHost) StageRowType(relation *catalog.Relation) error {
	return h.e.stageRowTypeLocked(h.stage, relation)
}

func (h ddlHost) RangeClass(a ddl.Authority, u sqltype.Usage, name *parser.ObjectName) (sqltype.IndexClass, error) {
	view := h.AccessView(a).Catalog
	class, ok := view.DefaultIndexClass(403, u.Type)
	if name != nil {
		var err error
		class, err = index.LookupClass(h, a, *name, 403)
		if err != nil {
			return class, err
		}
		if !class.Accepts(u.Type) {
			return class, sqlError("42804", fmt.Sprintf(`operator class "%s" does not accept data type %s`, catalog.ObjectNameText(*name), u.Type.DiagnosticName()))
		}
		ok = true
	}
	if !ok {
		d := sqlError("42704", fmt.Sprintf(`data type %s has no default operator class for access method "btree"`, u.Type.DiagnosticName())).(*sqlstate.Error)
		d.Hint = "You must specify an operator class for the range type or define a default operator class for the subtype."
		return class, d
	}
	return routine.BindClassSupport(view, class)
}
