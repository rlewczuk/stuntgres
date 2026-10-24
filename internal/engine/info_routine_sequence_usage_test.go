package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestInfoRoutineSequenceUsageProducer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoRoutineSequenceUsageOID)
	if !ok {
		t.Fatal("missing producer")
	}
	state := catalog.CloneState(f.e.catalog.State)
	a := sequence.NewRelation(sequence.RelationSpec{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20300}, Namespace: f.schema, Name: "sequence_a", Revision: 1, Owner: f.owner, Columns: sequence.Columns(f.e.CatalogRead())})
	b := *a
	b.ObjectAddress.ObjectID++
	b.ObjectName = "sequence_b"
	ordinary := *a
	ordinary.ObjectAddress.ObjectID += 2
	ordinary.ObjectName = "not_a_sequence"
	ordinary.RelationKind = catalog.TableRelation
	for _, d := range []*catalog.Relation{a, &b, &ordinary} {
		state.SetDescriptor(d.Address(), d)
	}
	callee := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 20310}, ObjectNamespace: f.schema, ObjectName: "callee", ObjectRevision: 1, Owner: f.owner}
	state.SetDescriptor(callee.Address(), callee)
	dependency := func(address ids.ObjectAddress) catalog.ExpressionDependency {
		return catalog.ExpressionDependency{Ref: f.e.catalog.Domain.Ref(state.Epoch(), address), Revision: 1}
	}
	program := func(deps ...catalog.ExpressionDependency) *stored.RoutineProgram {
		return stored.NewRoutineProgram([]stored.RuleAction{stored.NewRuleAction(stored.RuleActionParts{Binding: analyze.Binding{Dependencies: deps}})})
	}
	routine := *callee
	routine.ObjectAddress.ObjectID++
	routine.ObjectName = "routine"
	routine.SQL = &proc.Definition{Standard: true}
	// Use the real stored constructor and its dependency deduplication, not a
	// stand-in RoutineProgram implementation or a producer-specific edge list.
	routine.SQLBody = program(dependency(a.Address()), dependency(a.Address()), dependency(b.Address()), dependency(ordinary.Address()), dependency(callee.Address()))
	state.SetDescriptor(routine.Address(), &routine)
	view := f.e.catalog.Read(state)
	auth := dtpUnitAuthority(t, f, view)
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoRoutineSequenceUsageOID})
	input := func(v catalog.Read, a AuthorizationView) pgcatalog.Input {
		return pgcatalog.Input{Context: t.Context(), Catalog: v, Roles: a.Roles, Owner: v.Owner(), Ref: v.Owner().Domain.Ref(v.Epoch(), source.Address()), Source: v.MustRelation(source.Address()), Runtime: engineCatalogRuntime{auth: a, database: "captured_database"}}
	}
	check := func(in pgcatalog.Input, names ...string) [][]sqltype.Value {
		t.Helper()
		captured, err := captureCatalogSourceRows(in, produce)
		if err != nil {
			t.Fatal(err)
		}
		defer captured.Close()
		if captured.Ref != in.Ref || captured.View != nil || captured.Anonymous == nil {
			t.Fatal("anonymous capture identity", captured)
		}
		got := map[string]int{}
		for _, row := range captured.Anonymous {
			if len(row) != 9 {
				t.Fatal("projection width", row)
			}
			for i, value := range row {
				column := in.Source.Columns[i]
				if value.Type() != column.Usage.Type || value.Type().ID() != 13248 || value.IsNull() || !value.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(pgcatalog.InfoRoutineSequenceUsageOID) || column.OriginAttribute != int16(i+1) {
					t.Fatal("identifier domain/provenance", i, value, column)
				}
			}
			for i, want := range []string{"captured_database", "default_schema", "routine_20311", "captured_database", "default_schema", "routine", "captured_database", "default_schema"} {
				if text := infoUserMappingsUnitText(t, row[i]); text != want {
					t.Fatalf("column %d=%q want %q", i, text, want)
				}
			}
			got[infoUserMappingsUnitText(t, row[8])]++
		}
		want := map[string]int{}
		for _, name := range names {
			want[name]++
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("sequence pair bag", got, want)
		}
		return captured.Anonymous
	}
	attribute := func(sub int32) catalog.ExpressionDependency {
		address := a.Address()
		address.SubID = sub
		return dependency(address)
	}
	for _, tc := range []struct {
		name string
		deps []catalog.ExpressionDependency
	}{
		{"single-attribute", []catalog.ExpressionDependency{attribute(1)}},
		{"multiple-attributes", []catalog.ExpressionDependency{attribute(1), attribute(3)}},
		{"whole-and-attributes", []catalog.ExpressionDependency{dependency(a.Address()), attribute(1), attribute(3)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := catalog.CloneState(state)
			copy := routine
			copy.SQLBody = program(tc.deps...)
			next.SetDescriptor(copy.Address(), &copy)
			nextView := f.e.catalog.Read(next)
			nextAuth := auth
			nextAuth.Recapture(nextView)
			check(input(nextView, nextAuth), "sequence_a")
		})
	}
	for _, actor := range []authorization.PrincipalRef{auth.State.CurrentUser, f.owner, f.member} {
		auth.State.CurrentUser = actor
		check(input(view, auth), "sequence_a", "sequence_b")
	}
	auth.State.CurrentUser = f.reader
	check(input(view, auth))
	// Sequence ACL alone does not reveal any dependency identities.
	granted := catalog.CloneState(state)
	allowed := *a
	allowed.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.UsagePrivilege|authorization.SelectPrivilege, 0)}}
	granted.SetDescriptor(allowed.Address(), &allowed)
	grantedView := f.e.catalog.Read(granted)
	grantedAuth := auth
	grantedAuth.Recapture(grantedView)
	check(input(grantedView, grantedAuth))
	auth.State.CurrentUser = f.owner
	retained := check(input(view, auth), "sequence_a", "sequence_b")
	changed := catalog.CloneState(state)
	renamed := *a
	renamed.ObjectName = "renamed_a"
	renamed.Owner = f.reader
	changed.SetDescriptor(renamed.Address(), &renamed)
	changedView := f.e.catalog.Read(changed)
	changedAuth := auth
	changedAuth.Recapture(changedView)
	check(input(changedView, changedAuth), "sequence_b")
	changedAuth.State.CurrentUser = f.reader
	check(input(changedView, changedAuth), "renamed_a")
	check(input(view, auth), "sequence_a", "sequence_b")
	oldNames := map[string]int{}
	for _, row := range retained {
		oldNames[infoUserMappingsUnitText(t, row[8])]++
	}
	if !reflect.DeepEqual(oldNames, map[string]int{"sequence_a": 1, "sequence_b": 1}) {
		t.Fatal("retained rows changed", oldNames)
	}
	// A separately advanced, paired role root is valid; mixing role frontiers
	// with the old authority is not. Descriptor replacement leaves old roots.
	role, ok := auth.Roles.Find(f.owner)
	if !ok {
		t.Fatal("missing owner")
	}
	role.Name = "renamed_owner"
	nextRoot, err := auth.RoleRecord.Root.PutDescriptor(role)
	if err != nil {
		t.Fatal(err)
	}
	nextAuth := auth
	nextAuth.RoleRecord = &access.RoleRecord{Root: nextRoot, Revision: auth.RoleRecord.Revision + 1}
	nextAuth.Recapture(view)
	check(input(view, nextAuth), "sequence_a", "sequence_b")
	for _, ctx := range []context.Context{&dtpCancelContext{Context: context.Background(), remaining: 1}, &dtpCancelContext{Context: context.Background(), remaining: 6}} {
		in := input(view, auth)
		in.Context = ctx
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation exposed prefix", rows, err)
		}
	}
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &New().catalog.Owner },
		func(in *pgcatalog.Input) { in.Roles = nextAuth.Roles },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref = new(ids.Domain).Ref(view.Epoch(), source.Address()) },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) { a := auth; a.RoleRecord = nil; in.Runtime = engineCatalogRuntime{auth: a} },
		func(in *pgcatalog.Input) {
			a := auth
			a.State.CurrentUser = f.e.roles.domain.Principal(99999)
			in.Runtime = engineCatalogRuntime{auth: a}
		},
	} {
		in := input(view, auth)
		mutate(&in)
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("invalid capture accepted", rows, err)
		}
	}
	for _, malformed := range []string{"width", "domain", "missing-sequence", "nonexistent-attribute", "dependency-domain", "dependency-epoch", "namespace", "invalid-after-valid-attribute", "foreign-after-valid-attribute", "stale-after-valid-attribute"} {
		bad := catalog.CloneState(state)
		switch malformed {
		case "width", "domain":
			copy := *source
			copy.Columns = append([]catalog.Column(nil), source.Columns...)
			if malformed == "width" {
				copy.Columns = copy.Columns[:8]
			} else {
				copy.Columns[0].Usage.Type, _ = view.LookupType(sqltype.Name)
			}
			bad.SetDescriptor(copy.Address(), &copy)
		case "namespace":
			copy := *a
			copy.ObjectNamespace.ObjectID++
			bad.SetDescriptor(copy.Address(), &copy)
		default:
			copy := routine
			dep := dependency(a.Address())
			switch malformed {
			case "missing-sequence":
				dep.Ref.Address.ObjectID = 999999
			case "nonexistent-attribute", "invalid-after-valid-attribute":
				dep.Ref.Address.SubID = 99
			case "dependency-domain":
				dep.Ref = new(ids.Domain).Ref(view.Epoch(), a.Address())
			case "dependency-epoch":
				dep.Ref.Epoch++
			case "foreign-after-valid-attribute":
				dep = attribute(3)
				dep.Ref = new(ids.Domain).Ref(view.Epoch(), dep.Ref.Address)
			case "stale-after-valid-attribute":
				dep = attribute(3)
				dep.Ref.Epoch++
			}
			copy.SQLBody = program(dep)
			if malformed == "invalid-after-valid-attribute" || malformed == "foreign-after-valid-attribute" || malformed == "stale-after-valid-attribute" {
				// Dedup must never mask a later invalid ref to an emitted sequence.
				copy.SQLBody = program(attribute(1), dep)
			}
			bad.SetDescriptor(copy.Address(), &copy)
		}
		badView := f.e.catalog.Read(bad)
		badAuth := auth
		badAuth.Recapture(badView)
		if rows, err := produce(input(badView, badAuth)); rows != nil || err == nil {
			t.Fatal("malformed metadata accepted", malformed, rows, err)
		}
	}
	emptyView := f.e.catalog.Read(catalog.CloneState(f.e.catalog.State))
	emptyAuth := auth
	emptyAuth.Recapture(emptyView)
	check(input(emptyView, emptyAuth))
}
