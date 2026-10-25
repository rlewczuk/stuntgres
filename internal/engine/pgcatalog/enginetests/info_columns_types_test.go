package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestInfoColumnsTypeFacts(t *testing.T) {
	f := newColumnsUnitFixture(t)
	view := f.dc.f.e.Catalog.Read(f.state)
	// Dimension order: max characters, max octets, numeric precision/radix/
	// scale, datetime precision. -1 is inapplicable, never zero/MAXINT.
	cases := []struct {
		id         sqltype.ID
		mods       []int32
		name       string
		dimensions [6]int64
	}{
		{sqltype.Int2, nil, "smallint", [6]int64{-1, -1, 16, 2, 0, -1}},
		{sqltype.Int4, nil, "integer", [6]int64{-1, -1, 32, 2, 0, -1}},
		{sqltype.Int8, nil, "bigint", [6]int64{-1, -1, 64, 2, 0, -1}},
		{sqltype.Float4, nil, "real", [6]int64{-1, -1, 24, 2, -1, -1}},
		{sqltype.Float8, nil, "double precision", [6]int64{-1, -1, 53, 2, -1, -1}},
		{sqltype.Numeric, nil, "numeric", [6]int64{-1, -1, -1, 10, -1, -1}},
		{sqltype.Numeric, []int32{12, 3}, "numeric", [6]int64{-1, -1, 12, 10, 3, -1}},
		{sqltype.Numeric, []int32{5, -2}, "numeric", [6]int64{-1, -1, 5, 10, 2046, -1}},
		{sqltype.Numeric, []int32{3, 5}, "numeric", [6]int64{-1, -1, 3, 10, 5, -1}},
		{sqltype.BPChar, []int32{3}, "character", [6]int64{3, 12, -1, -1, -1, -1}},
		{sqltype.BPChar, nil, "character", [6]int64{-1, 1073741824, -1, -1, -1, -1}},
		{sqltype.VarChar, []int32{7}, "character varying", [6]int64{7, 28, -1, -1, -1, -1}},
		{sqltype.VarChar, nil, "character varying", [6]int64{-1, 1073741824, -1, -1, -1, -1}},
		{sqltype.Text, nil, "text", [6]int64{-1, 1073741824, -1, -1, -1, -1}},
		{sqltype.Bit, []int32{5}, "bit", [6]int64{5, -1, -1, -1, -1, -1}},
		{sqltype.VarBit, []int32{9}, "bit varying", [6]int64{9, -1, -1, -1, -1, -1}},
		{sqltype.VarBit, nil, "bit varying", [6]int64{-1, -1, -1, -1, -1, -1}},
		{sqltype.Date, nil, "date", [6]int64{-1, -1, -1, -1, -1, 0}},
		{sqltype.Time, nil, "time without time zone", [6]int64{-1, -1, -1, -1, -1, 6}},
		{sqltype.Timetz, nil, "time with time zone", [6]int64{-1, -1, -1, -1, -1, 6}},
		{sqltype.Timestamp, []int32{2}, "timestamp without time zone", [6]int64{-1, -1, -1, -1, -1, 2}},
		{sqltype.Timestamptz, nil, "timestamp with time zone", [6]int64{-1, -1, -1, -1, -1, 6}},
		{sqltype.Interval, nil, "interval", [6]int64{-1, -1, -1, -1, -1, 6}},
		{sqltype.Bool, nil, "boolean", [6]int64{-1, -1, -1, -1, -1, -1}},
		{sqltype.Name, nil, "name", [6]int64{-1, -1, -1, -1, -1, -1}},
		{sqltype.Bytea, nil, "bytea", [6]int64{-1, -1, -1, -1, -1, -1}},
		{sqltype.JSON, nil, "json", [6]int64{-1, -1, -1, -1, -1, -1}},
		{sqltype.UUID, nil, "uuid", [6]int64{-1, -1, -1, -1, -1, -1}},
		{1007, nil, "ARRAY", [6]int64{-1, -1, -1, -1, -1, -1}},
		{22002, nil, "integer", [6]int64{-1, -1, 32, 2, 0, -1}},
		{22005, nil, "USER-DEFINED", [6]int64{-1, -1, -1, -1, -1, -1}},
	}
	for _, tc := range cases {
		typ, ok := view.LookupType(tc.id)
		if !ok {
			t.Fatal("missing type", tc.id)
		}
		u := typ.Usage()
		if len(tc.mods) != 0 {
			var err error
			u, _, err = typ.ResolveModifiers(tc.mods...)
			if err != nil {
				t.Fatal(err)
			}
		}
		facts, err := pgcatalog.InfoColumnTypeFacts(view, u)
		got := [6]int64{facts.CharacterMaximumLength, facts.CharacterOctetLength, facts.NumericPrecision, facts.NumericPrecisionRadix, facts.NumericScale, facts.DatetimePrecision}
		if err != nil || facts.DataType != tc.name || got != tc.dimensions {
			t.Fatal("type facts", tc.id, tc.mods, facts, got, tc.dimensions, err)
		}
		if tc.id == 22005 && (facts.Domain.Name() != "nn_child" || facts.UDT.Name() != "nn" || facts.Domain.SQLType.OwnDomainNotNull() || !facts.Domain.SQLType.DomainNotNull()) {
			t.Fatal("one-level metadata vs effective enforcement", facts)
		}
	}
	interval, _ := view.LookupType(sqltype.Interval)
	for _, tc := range []struct {
		mask int32
		name string
	}{
		{sqltype.IntervalYear, "YEAR"}, {sqltype.IntervalMonth, "MONTH"}, {sqltype.IntervalDay, "DAY"}, {sqltype.IntervalHour, "HOUR"}, {sqltype.IntervalMinute, "MINUTE"}, {sqltype.IntervalSecond, "SECOND"},
		{sqltype.IntervalYear | sqltype.IntervalMonth, "YEAR TO MONTH"},
		{sqltype.IntervalDay | sqltype.IntervalHour, "DAY TO HOUR"},
		{sqltype.IntervalDay | sqltype.IntervalHour | sqltype.IntervalMinute, "DAY TO MINUTE"},
		{sqltype.IntervalDay | sqltype.IntervalHour | sqltype.IntervalMinute | sqltype.IntervalSecond, "DAY TO SECOND"},
		{sqltype.IntervalHour | sqltype.IntervalMinute, "HOUR TO MINUTE"},
		{sqltype.IntervalHour | sqltype.IntervalMinute | sqltype.IntervalSecond, "HOUR TO SECOND"},
		{sqltype.IntervalMinute | sqltype.IntervalSecond, "MINUTE TO SECOND"},
	} {
		u, _, err := interval.ResolveModifiers(tc.mask)
		if err != nil {
			t.Fatal(err)
		}
		facts, err := pgcatalog.InfoColumnTypeFacts(view, u)
		if err != nil || facts.IntervalType != tc.name || facts.DatetimePrecision != 6 {
			t.Fatal(tc, facts, err)
		}
		if tc.mask&sqltype.IntervalSecond != 0 {
			u, _, err = interval.ResolveModifiers(tc.mask, 3)
			if err != nil {
				t.Fatal(err)
			}
			facts, err = pgcatalog.InfoColumnTypeFacts(view, u)
			if err != nil || facts.IntervalType != tc.name+"(3)" || facts.DatetimePrecision != 3 {
				t.Fatal(tc, facts, err)
			}
		}
	}
	foreign, _ := engine.New().CatalogRead().LookupType(sqltype.Int4)
	if _, err := pgcatalog.InfoColumnTypeFacts(view, foreign.Usage()); err == nil {
		t.Fatal("foreign type generation accepted")
	}
	state := catalog.CloneState(f.state)
	integer := *view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)})
	integer.SQLType = foreign
	state.SetDescriptor(integer.Address(), &integer)
	usage := f.dc.nn.SQLType.Usage()
	if _, err := pgcatalog.InfoColumnTypeFacts(f.dc.f.e.Catalog.Read(state), usage); err == nil {
		t.Fatal("stale immediate domain base accepted")
	}
}

func newColumnsUnitFixture(t *testing.T) columnsUnitFixture {
	t.Helper()
	dc := newDCUnitFixture(t)
	state := catalog.CloneState(dc.state)
	view := dc.f.e.Catalog.Read(state)
	integer, _ := view.LookupType(sqltype.Int4)
	boolean, _ := view.LookupType(sqltype.Bool)
	nested, _ := view.LookupType(22005)
	literal, _ := sqltype.Integer(integer, 7)
	table := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 22400}, ObjectNamespace: dc.f.schema, ObjectName: "columns_unit", ObjectRevision: 1, Owner: dc.f.owner, RelationKind: catalog.TableRelation,
		Columns: []catalog.Column{{Name: "a", Usage: integer.Usage(), Nullable: false, NullabilityKnown: true}, {Name: "b", Usage: boolean.Usage(), Nullable: true, NullabilityKnown: true}, {Name: "nested", Usage: nested.Usage(), Nullable: true, NullabilityKnown: true}}, Attributes: []int32{1, 3, 4}, Index: map[string]int{"a": 0, "b": 1, "nested": 2},
		ColumnMeta: catalog.ColumnMetadata{HighWater: 4, Defaults: map[int32]catalog.ColumnDefault{1: {OID: 22500, Expression: plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: literal}}}}, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true}}}
	state.SetDescriptor(table.Address(), table)
	return columnsUnitFixture{dc: dc, state: state, table: table}
}

type columnsUnitFixture struct {
	dc    dcUnitFixture
	state *catalog.State
	table *catalog.Relation
}

func (f columnsUnitFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.dc.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.dc.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoColumnsOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: engine.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}}
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
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: engine.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}}
}
