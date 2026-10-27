package tablecmds

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
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

// TypeHost is the family host of the type, domain, collation and conversion
// decisions: the typedef.StageHost methods beyond what TableHost provides
// (ddl.TypeStaging, row types, type resolution) and the shared DDLHost.
type TypeHost struct {
	TableHost
}

var _ typedef.StageHost = TypeHost{}

func (h TypeHost) AllocateEnumMember() (int64, error) {
	return h.E.Catalog.AllocateEnumMemberLocked()
}

func (h TypeHost) ReplaceUnsynchronized(d catalog.Descriptor) error {
	if next, ok := d.(*catalog.Type); ok {
		if old, ok := h.CatalogStage.Base.Type(d.Address()); ok && old.DomainNotNull != nil && next.DomainNotNull == nil {
			p := core.StageDropProvider(nil, h.CatalogStage, nil, nil, nil)
			records, err := p.CaptureTypeProjection(old, next)
			if err != nil {
				return err
			}
			if err := h.CatalogStage.ReplaceDescriptor(d, false, false); err != nil {
				return err
			}
			for _, record := range records {
				h.CatalogStage.MarkDrop(record.Address, catalog.DropProvenance{Original: true})
			}
			h.CatalogStage.DropRecords = append(h.CatalogStage.DropRecords, records...)
			return nil
		}
	}
	return h.CatalogStage.ReplaceDescriptor(d, false, false)
}

func (h TypeHost) ReplaceDomainIdentity(d *catalog.Type) error {
	return h.CatalogStage.ReplaceDescriptor(d, false, true)
}

func (h TypeHost) RemoveDomainCheck(d *catalog.Type, oid ids.OID) error {
	h.CatalogStage.MarkDrop(ids.ObjectAddress{ClassID: 2606, ObjectID: oid}, catalog.DropProvenance{Original: true})
	return table.RemoveDomainCheck(core.StageDropProvider(nil, h.CatalogStage, nil, nil, nil), d, oid)
}

func (h TypeHost) FindCollation(a ddl.Authority, name parser.ObjectName) (*catalog.Collation, error) {
	return h.E.FindCollation(core.DDLAuthorityOf(a), name)
}

func (h TypeHost) ResolveDomainBase(raw parser.TypeName) (sqltype.Usage, error) {
	return h.E.ResolveAnnotationTypeMode(raw, h.Op, false, true)
}

func (h TypeHost) BindDomainDefault(a ddl.Authority, d *catalog.Type, raw parser.Expr, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) error {
	if restored, err := h.BindRestoredDomainDefault(a, d, raw, input, output); restored || err != nil {
		return err
	}
	return h.E.BindDomainDefault(h.Op.Context(), h.Op, core.DDLAuthorityOf(a), h.CatalogStage, d, raw, input, output)
}

func (h TypeHost) BindDomainConstraints(a ddl.Authority, d *catalog.Type, raw []parser.ColumnConstraint, input sqltype.InputEnvironment) error {
	return h.E.BindDomainConstraintDefinitions(h.Op.Context(), h.Op, core.DDLAuthorityOf(a), h.CatalogStage, d, raw, input)
}

func (h TypeHost) NewCompositeRelation(namespace ids.ObjectAddress, name string, owner authorization.PrincipalRef, columns []catalog.Column, index map[string]int) (*catalog.Relation, error) {
	relation, err := table.NewRelation(h, namespace, name, owner, columns, index)
	if err != nil {
		return nil, err
	}
	relation.RelationKind = catalog.CompositeRelation
	return relation, nil
}

func (h TypeHost) RangeClass(a ddl.Authority, u sqltype.Usage, name *parser.ObjectName) (sqltype.IndexClass, error) {
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
			return class, core.SQLError("42804", fmt.Sprintf(`operator class "%s" does not accept data type %s`, catalog.ObjectNameText(*name), u.Type.DiagnosticName()))
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
		d := core.SQLError("42704", fmt.Sprintf(`data type %s has no default operator class for access method "btree"`, u.Type.DiagnosticName())).(*sqlstate.Error)
		d.Hint = "You must specify an operator class for the range type or define a default operator class for the subtype."
		return class, d
	}
	return routine.BindClassSupport(view, class)
}
