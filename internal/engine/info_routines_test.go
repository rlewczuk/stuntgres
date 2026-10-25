package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func routinesUnitInput(t *testing.T, f defaultUnitFixture, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoRoutinesOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: EngineCatalogRuntime{Auth: auth, Database: "captured_database"}, TypeNamePath: []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}}}
}

func TestInfoRoutinesProducer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	// Use a genuine released declaration, not an execution stand-in.
	original := f.e.CatalogRead().MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1397})
	owned := *original
	owned.Owner = f.owner
	owned.ACL = authorization.ACLValue{Explicit: true}
	state.SetDescriptor(owned.Address(), &owned)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoRoutinesOID)
	if !ok {
		t.Fatal("missing routines producer")
	}
	read := func(actor authorization.PrincipalRef, s *catalog.State) ([]sqltype.Value, [][]sqltype.Value) {
		t.Helper()
		in := routinesUnitInput(t, f, s, actor)
		rows, err := produce(in)
		if err != nil {
			t.Fatal(err)
		}
		var selected []sqltype.Value
		for _, row := range rows {
			if len(row) != 82 {
				t.Fatal("projection width", len(row))
			}
			for i, value := range row {
				column := in.Source.Columns[i]
				if value.Type() != column.Usage.Type || !value.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(pgcatalog.InfoRoutinesOID) || column.OriginAttribute != int16(i+1) {
					t.Fatal("foreign cell authority", i, value, column)
				}
			}
			if columnsUnitCell(t, row[2]) == "abs_1397" {
				selected = row
			}
		}
		return selected, rows
	}
	values := func(row []sqltype.Value) []any {
		out := make([]any, len(row))
		for i, value := range row {
			out[i] = columnsUnitCell(t, value)
		}
		return out
	}
	want := make([]any, 82)
	for i, value := range map[int]any{0: "captured_database", 1: "pg_catalog", 2: "abs_1397", 3: "captured_database", 4: "pg_catalog", 5: "abs", 6: "FUNCTION", 13: "integer", 28: "captured_database", 29: "pg_catalog", 30: "int4", 35: "0", 36: "EXTERNAL", 37: "int4abs", 39: "INTERNAL", 40: "GENERAL", 41: "YES", 42: "MODIFIES", 43: "YES", 45: "YES", 46: int64(0), 49: "INVOKER", 53: "NO", 57: "NO"} {
		want[i] = value
	}
	retained, _ := read(f.owner, state)
	if got := values(retained); !reflect.DeepEqual(got, want) {
		t.Fatal("exact82 owner row", got, want)
	}
	if row, _ := read(f.reader, state); row != nil {
		t.Fatal("explicit empty ACL exposed routine")
	}
	if row, _ := read(f.member, state); !reflect.DeepEqual(values(row), want) {
		t.Fatal("enabled owner lost definition")
	}
	granted := catalog.CloneState(state)
	allowed := owned
	allowed.ACL.Entries = []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.ExecutePrivilege, 0)}
	granted.SetDescriptor(allowed.Address(), &allowed)
	row, _ := read(f.reader, granted)
	redacted := append([]any(nil), want...)
	redacted[37] = nil
	if !reflect.DeepEqual(values(row), redacted) {
		t.Fatal("Execute redaction", values(row), redacted)
	}
	if !reflect.DeepEqual(values(retained), want) {
		t.Fatal("old row changed")
	}
	// All-hidden success is nonnil, not a canceled or absent source.
	hidden := catalog.CloneState(state)
	for _, descriptor := range f.e.Catalog.Read(state).Descriptors() {
		if p, ok := descriptor.(*catalog.Procedure); ok {
			copy := *p
			copy.Owner, copy.ACL = f.owner, authorization.ACLValue{Explicit: true}
			hidden.SetDescriptor(copy.Address(), &copy)
		}
	}
	if _, rows := read(f.reader, hidden); rows == nil || len(rows) != 0 {
		t.Fatal("all-hidden success", rows)
	}
	// Immutable display participates in the execution declaration fence, while
	// ordinary identity/ownership publication retains it by value.
	copy := owned
	copy.ObjectName, copy.Owner = "renamed", f.reader
	if copy.Display != owned.Display || !exec.SameRoutineDeclaration(&copy, &owned) {
		t.Fatal("COW declaration facts changed")
	}
	copy.Display.Source = "forged"
	if exec.SameRoutineDeclaration(&copy, &owned) {
		t.Fatal("forged source accepted by declaration fence")
	}
}

func TestInfoRoutinesCapture(t *testing.T) {
	f := newDefaultUnitFixture(t)
	old := routinesUnitInput(t, f, f.e.Catalog.State, f.owner)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoRoutinesOID)
	foreign := New()
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &foreign.Catalog.Owner },
		func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
		func(in *pgcatalog.Input) { in.Roles = foreign.Roles() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { in.Ref = new(ids.Domain).Ref(in.Ref.Epoch, in.Ref.Address) },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) { in.Runtime = nil },
		func(in *pgcatalog.Input) { in.TypeNamePath = nil },
		func(in *pgcatalog.Input) {
			a := in.Runtime.(EngineCatalogRuntime)
			a.Auth.RoleRecord = nil
			in.Runtime = a
		},
		func(in *pgcatalog.Input) {
			a := in.Runtime.(EngineCatalogRuntime)
			a.Auth.State.CurrentUser = old.Owner.Principals.Principal(99999)
			in.Runtime = a
		},
	} {
		in := old
		mutate(&in)
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("foreign capture accepted", rows, err)
		}
	}
	// Same principal domain, separately published role frontier: valid when
	// paired with its authority, invalid when mixed into the old capture.
	auth := old.Runtime.(EngineCatalogRuntime).Auth
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
		t.Fatal("mixed role frontier accepted", rows, err)
	}
	mixed.Runtime = EngineCatalogRuntime{Auth: next, Database: "captured_database"}
	if rows, err := produce(mixed); rows == nil || err != nil {
		t.Fatal("paired advanced frontier rejected", rows, err)
	}
	for _, kind := range []string{"width", "domain-generation", "base-generation", "origin", "column-name", "missing-display", "invalid-language", "namespace", "result"} {
		t.Run(kind, func(t *testing.T) {
			state := catalog.CloneState(f.e.Catalog.State)
			source := *old.Source
			source.Columns = append([]catalog.Column(nil), source.Columns...)
			p := *old.Catalog.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1397})
			switch kind {
			case "width":
				source.Columns = source.Columns[:81]
			case "domain-generation":
				source.Columns[0].Usage.Type, _ = foreign.CatalogRead().LookupType(13248)
			case "base-generation":
				typ := *old.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Name)})
				typ.SQLType, _ = foreign.CatalogRead().LookupType(sqltype.Name)
				state.SetDescriptor(typ.Address(), &typ)
			case "origin":
				source.Columns[0].OriginAttribute = 2
			case "column-name":
				source.Columns[0].Name = "invented"
			case "missing-display":
				p.Display = catalog.RoutineDisplay{}
			case "invalid-language":
				p.Display.Language = 255
			case "namespace":
				p.ObjectNamespace.ObjectID = 99999
			case "result":
				p.Result = 99999
			}
			state.SetDescriptor(source.Address(), &source)
			state.SetDescriptor(p.Address(), &p)
			if rows, err := produce(routinesUnitInput(t, f, state, f.owner)); rows != nil || err == nil {
				t.Fatal("malformed source accepted", rows, err)
			}
		})
	}
	for _, remaining := range []int{1, 6, 100} {
		in := old
		in.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled capture exposed prefix", rows, err)
		}
	}
}
