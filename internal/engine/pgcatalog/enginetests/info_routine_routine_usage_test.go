package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestInfoRoutineRoutineUsageProducer(t *testing.T) {
	// The independent native relation identity is reserved in the living plan.
	const sourceOID ids.OID = 16232
	if pgcatalog.InfoRoutineRoutineUsageOID != sourceOID {
		t.Fatal("native source identity", pgcatalog.InfoRoutineRoutineUsageOID)
	}
	f := newDefaultUnitFixture(t)
	produce, ok := pgcatalog.ProducerFor(sourceOID)
	if !ok {
		t.Fatal("missing routine_routine_usage producer")
	}
	state := catalog.CloneState(f.e.Catalog.State)
	// Declaration defaults validate the callee's real language authority and
	// compare the call's properties with its declared volatility.
	language, err := catalog.LookupLanguage(f.e.Catalog.Read(state), "sql")
	if err != nil {
		t.Fatal(err)
	}
	leaf := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 20400}, ObjectNamespace: f.schema, ObjectName: "leaf", ObjectRevision: 1, Owner: f.owner, RoutineKind: 'f', Result: 23, Language: language.Address(), Volatility: expr.Volatile}
	integer, _ := f.e.CatalogRead().LookupType(sqltype.Int4)
	leaf.SQL = &proc.Definition{Result: integer.Usage()}
	state.SetDescriptor(leaf.Address(), leaf)
	dep := func(address ids.ObjectAddress) catalog.ExpressionDependency {
		return catalog.ExpressionDependency{Ref: f.e.Catalog.Domain.Ref(state.Epoch(), address), Revision: 1}
	}
	program := func(addresses ...ids.ObjectAddress) *stored.RoutineProgram {
		var deps []catalog.ExpressionDependency
		for _, address := range addresses {
			deps = append(deps, dep(address))
		}
		return stored.NewRoutineProgram([]stored.RuleAction{stored.NewRuleAction(stored.RuleActionParts{Binding: analyze.Binding{Dependencies: deps}})})
	}
	caller := *leaf
	caller.ObjectAddress.ObjectID++
	caller.ObjectName = "caller"
	caller.Owner = f.reader
	caller.SQL = &proc.Definition{Standard: true}
	caller.SQLBody = program(leaf.Address(), leaf.Address())
	state.SetDescriptor(caller.Address(), &caller)
	view := f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f, view)
	auth.State.CurrentUser = f.owner
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: sourceOID})
	input := func(v catalog.Read, a core.AuthorizationView) pgcatalog.Input {
		return pgcatalog.Input{Context: t.Context(), Catalog: v, Roles: a.Roles, Owner: v.Owner(), Ref: v.Owner().Domain.Ref(v.Epoch(), source.Address()), Source: v.MustRelation(source.Address()), Runtime: core.EngineCatalogRuntime{Auth: a, Database: "captured_database"}}
	}
	check := func(in pgcatalog.Input, want [][]string) [][]sqltype.Value {
		t.Helper()
		captured, err := core.CaptureCatalogSourceRows(in, produce)
		if err != nil {
			t.Fatal(err)
		}
		defer captured.Close()
		if captured.Ref != in.Ref || captured.View != nil || captured.Anonymous == nil {
			t.Fatal("anonymous capture identity", captured)
		}
		var got [][]string
		for _, row := range captured.Anonymous {
			if len(row) != 6 {
				t.Fatal("projection width", row)
			}
			var text []string
			for i, value := range row {
				column := in.Source.Columns[i]
				if value.Type() != column.Usage.Type || value.Type().ID() != 13248 || value.Type().BaseType().ID() != sqltype.Name || value.IsNull() || !value.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(sourceOID) || column.OriginAttribute != int16(i+1) || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.DeclaredDimensions != 0 {
					t.Fatal("domain/provenance", i, value, column)
				}
				text = append(text, infoUserMappingsUnitText(t, value))
			}
			got = append(got, text)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("routine pair bag", got, want)
		}
		return captured.Anonymous
	}
	want := [][]string{{"captured_database", "default_schema", "caller_20401", "captured_database", "default_schema", "leaf_20400"}}
	retained := check(input(view, auth), want)
	// Real declaration-default capture, independently constructed from a typed
	// public routine expression rather than a stand-in manifest implementation.
	call := plan.SQLRoutineExpr{Info: expr.Info{Usage: integer.Usage(), RoutineReference: leaf, Property: expr.Properties{Volatility: expr.Volatile}}, Procedure: leaf, Ref: view.Owner().Domain.Ref(view.Epoch(), leaf.Address())}
	defaults, err := stored.NewRoutineDefaults(t.Context(), view, nil, []expr.Expr{call})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []bool{false, true} {
		next := catalog.CloneState(state)
		copy := caller
		copy.SQLDefaults = defaults
		if !body {
			copy.SQLBody = nil
			copy.SQL = &proc.Definition{Result: integer.Usage()}
		}
		next.SetDescriptor(copy.Address(), &copy)
		nv := f.e.Catalog.Read(next)
		na := auth
		na.Recapture(nv)
		check(input(nv, na), want)
	}
	// pg_catalog.abs(integer) is a represented pinned builtin, not an OID
	// threshold approximation. A real unpinned descriptor above remains visible.
	pinned := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1397})
	if !stored.DependPinned(view, nil, pinned.Address()) {
		t.Fatal("expected represented pinned abs(integer)")
	}
	pinnedState := catalog.CloneState(state)
	pinnedCaller := caller
	pinnedCaller.SQLBody = program(pinned.Address())
	pinnedState.SetDescriptor(caller.Address(), &pinnedCaller)
	pv := f.e.Catalog.Read(pinnedState)
	pa := auth
	pa.Recapture(pv)
	check(input(pv, pa), nil)
	// A referenced non-SQL provider declaration remains a real routine identity;
	// eligibility is not conditional on a SQL implementation or source text.
	nativeState := catalog.CloneState(state)
	nativeLeaf := *leaf
	nativeLeaf.SQL = nil
	nativeState.SetDescriptor(leaf.Address(), &nativeLeaf)
	nv := f.e.Catalog.Read(nativeState)
	na := auth
	na.Recapture(nv)
	check(input(nv, na), want)
	// Quoted/PL declarations without defaults have no hard body dependencies.
	textState := catalog.CloneState(state)
	textCaller := caller
	textCaller.SQL = &proc.Definition{Result: integer.Usage()}
	textCaller.SQLBody = nil
	textState.SetDescriptor(caller.Address(), &textCaller)
	tv := f.e.Catalog.Read(textState)
	ta := auth
	ta.Recapture(tv)
	check(input(tv, ta), nil)
	for _, from := range []catalog.RoutineKind{'f', 'p', 'a', 'w'} {
		for _, to := range []catalog.RoutineKind{'f', 'p', 'a', 'w'} {
			next := catalog.CloneState(state)
			u, v := caller, *leaf
			u.RoutineKind = from
			v.RoutineKind = to
			next.SetDescriptor(u.Address(), &u)
			next.SetDescriptor(v.Address(), &v)
			nv := f.e.Catalog.Read(next)
			na := auth
			na.Recapture(nv)
			expected := want
			if from != 'f' && from != 'p' || to != 'f' && to != 'p' {
				expected = nil
			}
			check(input(nv, na), expected)
		}
	}
	// No caller ownership filter: owner sees the private reader-owned caller.
	auth.State.CurrentUser = f.reader
	check(input(view, auth), nil)
	auth.State.CurrentUser = f.member
	check(input(view, auth), want)
	auth.State.CurrentUser = f.owner
	changed := catalog.CloneState(state)
	renamed := *leaf
	renamed.ObjectName = "renamed"
	renamed.Owner = f.reader
	changed.SetDescriptor(renamed.Address(), &renamed)
	cv := f.e.Catalog.Read(changed)
	ca := auth
	ca.Recapture(cv)
	check(input(cv, ca), nil)
	ca.State.CurrentUser = f.reader
	check(input(cv, ca), [][]string{{"captured_database", "default_schema", "caller_20401", "captured_database", "default_schema", "renamed_20400"}})
	if infoUserMappingsUnitText(t, retained[0][5]) != "leaf_20400" {
		t.Fatal("old capture changed")
	}
	role, ok := auth.Roles.Find(f.owner)
	if !ok {
		t.Fatal("owner missing")
	}
	role.Name = "renamed_owner"
	root, err := auth.RoleRecord.Root.PutDescriptor(role)
	if err != nil {
		t.Fatal(err)
	}
	nextAuth := auth
	nextAuth.RoleRecord = &access.RoleRecord{Root: root, Revision: auth.RoleRecord.Revision + 1}
	nextAuth.Recapture(view)
	check(input(view, nextAuth), want)
	for _, ctx := range []context.Context{&dtpCancelContext{Context: context.Background(), remaining: 1}, &dtpCancelContext{Context: context.Background(), remaining: 6}} {
		in := input(view, auth)
		in.Context = ctx
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation exposed prefix", rows, err)
		}
	}
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &engine.New().Catalog.Owner }, func(in *pgcatalog.Input) { in.Roles = nextAuth.Roles }, func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref = new(ids.Domain).Ref(view.Epoch(), source.Address()) }, func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) {
			a := auth
			a.RoleRecord = nil
			in.Runtime = core.EngineCatalogRuntime{Auth: a}
		},
		func(in *pgcatalog.Input) {
			a := auth
			a.State.CurrentUser = f.e.RoleStore.Domain.Principal(99999)
			in.Runtime = core.EngineCatalogRuntime{Auth: a}
		},
	} {
		in := input(view, auth)
		mutate(&in)
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("invalid capture accepted", rows, err)
		}
	}
	for _, malformed := range []string{"width", "domain", "typmod", "collation", "missing-target", "invalid-address", "namespace"} {
		bad := catalog.CloneState(state)
		switch malformed {
		case "width", "domain", "typmod", "collation":
			copy := *source
			copy.Columns = append([]catalog.Column(nil), source.Columns...)
			switch malformed {
			case "width":
				copy.Columns = copy.Columns[:5]
			case "domain":
				copy.Columns[0].Usage.Type, _ = view.LookupType(sqltype.Name)
			case "typmod":
				copy.Columns[0].Usage.Typmod = 4
			case "collation":
				copy.Columns[0].Usage.Collation = sqltype.CollationRef{}
			}
			bad.SetDescriptor(copy.Address(), &copy)
		case "namespace":
			copy := *leaf
			copy.ObjectNamespace.ObjectID++
			bad.SetDescriptor(copy.Address(), &copy)
		default:
			copy := caller
			address := leaf.Address()
			if malformed == "missing-target" {
				address.ObjectID = 999999
			} else {
				address.SubID = 1
			}
			copy.SQLBody = program(leaf.Address(), address)
			bad.SetDescriptor(copy.Address(), &copy)
		}
		bv := f.e.Catalog.Read(bad)
		ba := auth
		ba.Recapture(bv)
		if rows, err := produce(input(bv, ba)); rows != nil || err == nil {
			t.Fatal("malformed metadata accepted", malformed, rows, err)
		}
	}
	empty := f.e.Catalog.Read(catalog.CloneState(f.e.Catalog.State))
	ea := auth
	ea.Recapture(empty)
	check(input(empty, ea), nil)
}
