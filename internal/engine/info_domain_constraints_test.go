package engine

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

type dcUnitFixture struct {
	f                      defaultUnitFixture
	state                  *catalog.State
	a, b, nn, child, plain *catalog.Type
}

func (f dcUnitFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoDomainConstraintsOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}}
}

func newDCUnitFixture(t *testing.T) dcUnitFixture {
	t.Helper()
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	view := f.e.Catalog.Read(state)
	integer, _ := view.LookupType(sqltype.Int4)
	boolean, _ := view.LookupType(sqltype.Bool)
	literal := plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}
	makeDomain := func(id sqltype.ID, name string, base sqltype.Usage, check, nn bool) *catalog.Type {
		typ, err := sqltype.Domain(id, name, "default_schema."+name, base, check, nn)
		if err != nil {
			t.Fatal(err)
		}
		d := &catalog.Type{SQLType: typ, ObjectNamespace: f.schema, ObjectRevision: 1, Owner: f.owner, ACL: authorization.ACLValue{Explicit: true}}
		if check {
			d.DomainChecks = []catalog.DomainCheckConstraint{{OID: ids.OID(id) + 100, Name: "same", Expression: literal}}
		}
		if nn {
			d.DomainNotNull = &catalog.DomainNotNullConstraint{OID: ids.OID(id) + 100, Name: "named_nn"}
		}
		state.SetDescriptor(d.Address(), d)
		return d
	}
	a := makeDomain(22000, "a", integer.Usage(), true, false)
	b := makeDomain(22001, "b", integer.Usage(), true, false)
	nn := makeDomain(22002, "nn", integer.Usage(), false, true)
	child := makeDomain(22003, "child", a.SQLType.Usage(), false, false)
	plain := makeDomain(22004, "plain", integer.Usage(), false, false)
	makeDomain(22005, "nn_child", nn.SQLType.Usage(), false, false)
	table := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 22200}, ObjectNamespace: f.schema, ObjectName: "table_check", ObjectRevision: 1, Owner: f.owner, RelationKind: catalog.TableRelation, Constraints: catalog.RelationConstraints{Check: []catalog.CheckConstraint{{OID: 22300, Name: "same", Expression: literal}}}}
	state.SetDescriptor(table.Address(), table)
	return dcUnitFixture{f: f, state: state, a: a, b: b, nn: nn, child: child, plain: plain}
}
