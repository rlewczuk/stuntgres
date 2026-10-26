package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func parametersUnitInput(t *testing.T, f defaultUnitFixture, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f, view)
	auth.State.CurrentUser = actor
	var source *catalog.Relation
	for _, d := range view.Descriptors() {
		if r, ok := d.(*catalog.Relation); ok && r.ObjectName == "parameters" {
			source = r
			break
		}
	}
	if source == nil {
		t.Fatal("missing information_schema.parameters")
	}
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}, TypeNamePath: []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}}}
}

func TestInfoParametersProducer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	original := f.e.CatalogRead().MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1397})
	owned := *original
	owned.Owner = f.owner
	owned.ACL = authorization.ACLValue{Explicit: true}
	state.SetDescriptor(owned.Address(), &owned)
	in := parametersUnitInput(t, f, state, f.owner)
	produce, ok := pgcatalog.ProducerFor(in.Source.Address().ObjectID)
	if !ok {
		t.Fatal("missing producer")
	}
	rows, err := produce(in)
	if err != nil {
		t.Fatal(err)
	}
	var retained []sqltype.Value
	for _, row := range rows {
		if len(row) != 32 {
			t.Fatal("width", len(row))
		}
		for i, v := range row {
			column := in.Source.Columns[i]
			if v.Type() != column.Usage.Type || !v.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(in.Source.Address().ObjectID) || column.OriginAttribute != int16(i+1) {
				t.Fatal("foreign cell authority", i)
			}
		}
		if columnsUnitCell(t, row[2]) == "abs_1397" {
			retained = row
		}
	}
	values := func(row []sqltype.Value) []any {
		out := make([]any, len(row))
		for i, v := range row {
			out[i] = columnsUnitCell(t, v)
		}
		return out
	}
	want := make([]any, 32)
	for i, v := range map[int]any{0: "captured_database", 1: "pg_catalog", 2: "abs_1397", 3: int64(1), 4: "IN", 5: "NO", 6: "NO", 8: "integer", 23: "captured_database", 24: "pg_catalog", 25: "int4", 30: "1"} {
		want[i] = v
	}
	if !reflect.DeepEqual(values(retained), want) {
		t.Fatal("exact32", values(retained), want)
	}
	hidden := catalog.CloneState(state)
	for _, d := range f.e.Catalog.Read(state).Descriptors() {
		if p, ok := d.(*catalog.Procedure); ok {
			copy := *p
			copy.Owner = f.owner
			copy.ACL = authorization.ACLValue{Explicit: true}
			hidden.SetDescriptor(copy.Address(), &copy)
		}
	}
	if rows, err := produce(parametersUnitInput(t, f, hidden, f.reader)); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("hidden", rows, err)
	}
	if !reflect.DeepEqual(values(retained), want) {
		t.Fatal("old backing changed")
	}
}

func TestInfoParametersCapture(t *testing.T) {
	f := newDefaultUnitFixture(t)
	old := parametersUnitInput(t, f, f.e.Catalog.State, f.owner)
	produce, _ := pgcatalog.ProducerFor(old.Source.Address().ObjectID)
	foreign := engine.New()
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &foreign.Catalog.Owner },
		func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
		func(in *pgcatalog.Input) { in.Roles = foreign.Roles() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) { in.Runtime = nil },
		func(in *pgcatalog.Input) { in.TypeNamePath = nil },
		func(in *pgcatalog.Input) { in.Ref = new(ids.Domain).Ref(in.Ref.Epoch, in.Ref.Address) },
		func(in *pgcatalog.Input) {
			r := in.Runtime.(core.EngineCatalogRuntime)
			r.Auth.RoleRecord = nil
			in.Runtime = r
		},
		func(in *pgcatalog.Input) {
			r := in.Runtime.(core.EngineCatalogRuntime)
			r.Auth.State.CurrentUser = in.Owner.Principals.Principal(99999)
			in.Runtime = r
		},
	} {
		in := old
		mutate(&in)
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("foreign capture accepted", rows, err)
		}
	}
	auth := old.Runtime.(core.EngineCatalogRuntime).Auth
	role, ok := auth.Roles.Find(f.owner)
	if !ok {
		t.Fatal("missing owner role")
	}
	role.Name = "renamed_owner"
	root, err := auth.RoleRecord.Root.PutDescriptor(role)
	if err != nil {
		t.Fatal(err)
	}
	next := auth
	next.RoleRecord = &access.RoleRecord{Root: root, Revision: auth.RoleRecord.Revision + 1}
	next.Recapture(old.Catalog)
	mixed := old
	mixed.Roles = next.Roles
	if rows, err := produce(mixed); rows != nil || err == nil {
		t.Fatal("mixed role frontier accepted")
	}
	mixed.Runtime = core.EngineCatalogRuntime{Auth: next, Database: "captured_database"}
	if rows, err := produce(mixed); rows == nil || err != nil {
		t.Fatal("paired role frontier rejected", err)
	}
	for _, kind := range []string{"width", "name", "origin", "domain", "base", "namespace", "formal-type", "default-map", "missing-default"} {
		t.Run(kind, func(t *testing.T) {
			state, d := parametersMixedDeclaration(t, f)
			original := parametersUnitInput(t, f, state, f.owner)
			source := *original.Source
			source.Columns = append([]catalog.Column(nil), source.Columns...)
			copy := *d
			switch kind {
			case "width":
				source.Columns = source.Columns[:31]
			case "name":
				source.Columns[0].Name = "invented"
			case "origin":
				source.Columns[0].OriginAttribute = 2
			case "domain":
				source.Columns[0].Usage.Type, _ = foreign.CatalogRead().LookupType(13248)
			case "base":
				typ := *original.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 19})
				typ.SQLType, _ = foreign.CatalogRead().LookupType(sqltype.Name)
				state.SetDescriptor(typ.Address(), &typ)
			case "namespace":
				copy.ObjectNamespace.ObjectID = 99999
			case "formal-type":
				definition := *d.SQL
				definition.Formals = append([]proc.Formal(nil), definition.Formals...)
				definition.Formals[0].Usage.Type, _ = foreign.CatalogRead().LookupType(sqltype.Int4)
				copy.SQL = &definition
			case "default-map":
				definition := *d.SQL
				definition.FormalInputs = append([]int(nil), definition.FormalInputs...)
				definition.FormalInputs[2] = 0
				copy.SQL = &definition
			case "missing-default":
				copy.SQLDefaults = nil
			}
			state.SetDescriptor(source.Address(), &source)
			state.SetDescriptor(copy.Address(), &copy)
			if rows, err := produce(parametersUnitInput(t, f, state, f.owner)); rows != nil || err == nil {
				t.Fatal("malformed capture accepted", kind, err)
			}
		})
	}
	for _, remaining := range []int{1, 6, 100} {
		in := old
		in.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled prefix", rows, err)
		}
	}
}

func parametersMixedDeclaration(t *testing.T, f defaultUnitFixture) (*catalog.State, *catalog.Procedure) {
	t.Helper()
	state := catalog.CloneState(f.e.Catalog.State)
	view := f.e.Catalog.Read(state)
	integer, _ := view.LookupType(sqltype.Int4)
	text, _ := view.LookupType(sqltype.Text)
	definition := &proc.Definition{}
	if err := definition.SetFormals([]proc.Formal{{Mode: "i", Name: "a", Usage: integer.Usage()}, {Mode: "o", Name: "b", Usage: text.Usage()}, {Mode: "b", Name: "c", Usage: integer.Usage(), HasDefault: true}}); err != nil {
		t.Fatal(err)
	}
	value, _ := sqltype.Integer(integer, 9)
	defaults, err := stored.NewRoutineDefaults(t.Context(), view, nil, []expr.Expr{nil, plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: value}})
	if err != nil {
		t.Fatal(err)
	}
	d := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 48000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, ObjectName: "mixed_unit", Signature: "23,23", SQL: definition, SQLDefaults: defaults, Owner: f.owner, ACL: authorization.ACLValue{Explicit: true}}
	state.SetDescriptor(d.Address(), d)
	return state, d
}

func TestInfoParametersRetainedAuthority(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state, d := parametersMixedDeclaration(t, f)
	old := parametersUnitInput(t, f, state, f.owner)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoParametersOID)
	read := func(in pgcatalog.Input) [][]sqltype.Value {
		t.Helper()
		rows, err := produce(in)
		if err != nil {
			t.Fatal(err)
		}
		var selected [][]sqltype.Value
		for _, row := range rows {
			if columnsUnitCell(t, row[2]) == "mixed_unit_48000" {
				selected = append(selected, row)
			}
		}
		return selected
	}
	retained := read(old)
	if len(retained) != 3 {
		t.Fatal("mixed width", retained)
	}
	var defaults int
	var third []sqltype.Value
	for _, row := range retained {
		ordinal := columnsUnitCell(t, row[3]).(int64)
		if ordinal == 3 {
			third = row
			if columnsUnitCell(t, row[4]) != "INOUT" || columnsUnitCell(t, row[31]) != "9" {
				t.Fatal("input slot mapping", row)
			}
		}
		if !row[31].IsNull() {
			defaults++
		}
	}
	if defaults != 1 || third == nil {
		t.Fatal("default ownership", defaults)
	}
	inherited := read(parametersUnitInput(t, f, state, f.member))
	if len(inherited) != 3 {
		t.Fatal("inherited owner rows")
	}
	for _, row := range inherited {
		if columnsUnitCell(t, row[3]) == int64(3) && columnsUnitCell(t, row[31]) != "9" {
			t.Fatal("inherited owner lost default")
		}
	}
	retainedNode, _ := d.SQLDefaults.Default(1)
	retainedDefaults := d.SQLDefaults
	replacement := *d
	value, _ := sqltype.Integer(d.SQL.Inputs[1].Type, 11)
	payload, err := stored.NewRoutineDefaults(t.Context(), old.Catalog, nil, []expr.Expr{nil, plan.LiteralExpr{Info: expr.Info{Usage: d.SQL.Inputs[1]}, Value: value}})
	replacement.SQLDefaults = payload
	if err != nil {
		t.Fatal(err)
	}
	updated := catalog.CloneState(state)
	updated.SetDescriptor(replacement.Address(), &replacement)
	for _, row := range read(parametersUnitInput(t, f, updated, f.owner)) {
		if columnsUnitCell(t, row[3]) == int64(3) && columnsUnitCell(t, row[31]) != "11" {
			t.Fatal("fresh default publication")
		}
	}
	currentNode, _ := d.SQLDefaults.Default(1)
	if retainedDefaults != d.SQLDefaults || !reflect.DeepEqual(retainedNode, currentNode) || currentNode.ResultUsage().Type != old.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23}).SQLType {
		t.Fatal("old default ownership changed")
	}
	granted := catalog.CloneState(state)
	allowed := *d
	allowed.ACL.Entries = []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.ExecutePrivilege, 0)}
	granted.SetDescriptor(allowed.Address(), &allowed)
	redacted := read(parametersUnitInput(t, f, granted, f.reader))
	if len(redacted) != 3 {
		t.Fatal("Execute rows", redacted)
	}
	for _, row := range redacted {
		if !row[31].IsNull() {
			t.Fatal("default disclosure to Execute")
		}
	}
	if columnsUnitCell(t, third[31]) != "9" || third[31].Type() != old.Source.Columns[31].Usage.Type {
		t.Fatal("old row lost domain/backing")
	}
	copy := allowed
	if !exec.SameRoutineDeclaration(&copy, &allowed) {
		t.Fatal("immutable COW rejected")
	}
	copy.LiteralDefaults = catalog.NewRoutineLiteralDefaults(catalog.RoutineLiteral{SourceType: 23, Text: "11"})
	if exec.SameRoutineDeclaration(&copy, &allowed) {
		t.Fatal("foreign recipe authority accepted")
	}
	foreign := engine.New()
	bad := catalog.CloneState(state)
	changed := *old.Source
	changed.Columns = append([]catalog.Column(nil), changed.Columns...)
	changed.Columns[31].Usage.Type, _ = foreign.CatalogRead().LookupType(13246)
	bad.SetDescriptor(changed.Address(), &changed)
	if rows, err := produce(parametersUnitInput(t, f, bad, f.owner)); rows != nil || err == nil {
		t.Fatal("same-OID foreign domain accepted")
	}
	if columnsUnitCell(t, third[31]) != "9" {
		t.Fatal("retained backing mutated")
	}
}
