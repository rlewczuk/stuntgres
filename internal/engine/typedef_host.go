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

var _ typedef.StageHost = DDLHost{}

func (h DDLHost) TrustedRestoreDestination(a ddl.Authority, what string) (bool, error) {
	if h.Op == nil || !OperationReplayOrZero(h.Op).TableDestination {
		return false, nil
	}
	auth := DDLAuthorityOf(a)
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	if err != nil || !bootstrap.Superuser || h.E.ImportTargets == nil {
		return true, fmt.Errorf("stuntgres: invalid trusted restore %s authority", what)
	}
	return true, nil
}

func (h DDLHost) AllocateEnumMember() (int64, error) {
	return h.E.Catalog.AllocateEnumMemberLocked()
}

func (h DDLHost) ReplaceUnsynchronized(d catalog.Descriptor) error {
	return h.CatalogStage.ReplaceDescriptor(d, false, false)
}

func (h DDLHost) ReplaceDomainIdentity(d *catalog.Type) error {
	return h.CatalogStage.ReplaceDescriptor(d, false, true)
}

func (h DDLHost) CreationTypeACL(owner authorization.PrincipalRef, schema ids.ObjectAddress) (authorization.ACLValue, bool) {
	return h.CatalogStage.CreationTypeACL(owner, schema)
}

func (h DDLHost) AddDomainBase(d catalog.Descriptor) {
	h.CatalogStage.DomainBases = append(h.CatalogStage.DomainBases, d)
}

func (h DDLHost) RemoveDomainCheck(d *catalog.Type, oid ids.OID) error {
	return table.RemoveDomainCheck(StageDropProvider(nil, h.CatalogStage, nil, nil, nil), d, oid)
}

func (h DDLHost) NamedType(a ddl.Authority, name parser.ObjectName) (*catalog.Type, error) {
	return h.E.namedType(DDLAuthorityOf(a), name)
}

func (h DDLHost) FindCollation(a ddl.Authority, name parser.ObjectName) (*catalog.Collation, error) {
	return h.E.findCollation(DDLAuthorityOf(a), name)
}

func (h DDLHost) ResolveType(raw parser.TypeName) (sqltype.Usage, error) {
	return h.E.resolveType(raw, h.Op)
}

func (h DDLHost) ResolveDomainBase(raw parser.TypeName) (sqltype.Usage, error) {
	return h.E.resolveAnnotationTypeMode(raw, h.Op, false, true)
}

func (h DDLHost) ResolveColumnCollation(u sqltype.Usage, raw *parser.ObjectName) (sqltype.Usage, error) {
	return h.E.resolveColumnCollation(u, raw, h.Op)
}

func (h DDLHost) BindDomainDefault(a ddl.Authority, d *catalog.Type, raw parser.Expr, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) error {
	if restored, err := h.bindRestoredDomainDefault(a, d, raw, input, output); restored || err != nil {
		return err
	}
	return h.E.BindDomainDefault(h.Op.Context(), h.Op, DDLAuthorityOf(a), h.CatalogStage, d, raw, input, output)
}

func (h DDLHost) BindDomainConstraints(a ddl.Authority, d *catalog.Type, raw []parser.ColumnConstraint, input sqltype.InputEnvironment) error {
	return h.E.BindDomainConstraintDefinitions(h.Op.Context(), h.Op, DDLAuthorityOf(a), h.CatalogStage, d, raw, input)
}

func (h DDLHost) DisplaceAssociatedArray(namespace ids.ObjectAddress, name string) (bool, error) {
	return h.E.DisplaceAssociatedArrayLocked(h.CatalogStage, namespace, name)
}

func (h DDLHost) AssociatedArrayName(namespace ids.ObjectAddress, name string) string {
	return AssociatedArrayName(h.CatalogStage, namespace, name)
}

func (h DDLHost) NewCompositeRelation(namespace ids.ObjectAddress, name string, owner authorization.PrincipalRef, columns []catalog.Column, index map[string]int) (*catalog.Relation, error) {
	relation, err := table.NewRelation(h, namespace, name, owner, columns, index)
	if err != nil {
		return nil, err
	}
	relation.RelationKind = catalog.CompositeRelation
	return relation, nil
}

func (h DDLHost) StageRowType(relation *catalog.Relation) error {
	return h.E.StageRowTypeLocked(h.CatalogStage, relation)
}

func (h DDLHost) RangeClass(a ddl.Authority, u sqltype.Usage, name *parser.ObjectName) (sqltype.IndexClass, error) {
	view := h.AccessView(a).Catalog
	var class sqltype.IndexClass
	var ok bool
	if name != nil {
		var err error
		class, err = index.LookupClass(h, a, *name, 403)
		if err != nil {
			return class, err
		}
		if !class.Accepts(u.Type) {
			return class, SQLError("42804", fmt.Sprintf(`operator class "%s" does not accept data type %s`, catalog.ObjectNameText(*name), u.Type.DiagnosticName()))
		}
		ok = true
	} else {
		var err error
		class, ok, err = view.DefaultIndexClass(403, u.Type)
		if err != nil {
			return class, index.DefaultClassDiagnostic(h, a, err)
		}
	}
	if !ok {
		d := SQLError("42704", fmt.Sprintf(`data type %s has no default operator class for access method "btree"`, u.Type.DiagnosticName())).(*sqlstate.Error)
		d.Hint = "You must specify an operator class for the range type or define a default operator class for the subtype."
		return class, d
	}
	return routine.BindClassSupport(view, class)
}
