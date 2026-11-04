package schemacmds_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func loadUnitDiagnostic(t *testing.T, err error, code, message string) {
	t.Helper()
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("LOAD diagnostic: got %#v (%v), want %#v", got, err, want)
	}
}

func TestLoadValidation(t *testing.T) {
	e := engine.New()
	admin, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	if _, err := e.Execute(admin, &parser.CreateRole{Name: parser.Identifier{Text: "load_reader"}, DefaultLogin: true}, 0, nil); err != nil {
		t.Fatal(err)
	}
	reader, closeReader := enginetest.RoleOperation(t, e, "load_reader")
	defer closeReader()
	for _, tc := range []struct {
		filename      string
		ordinary      bool
		code, message string
	}{
		{"", false, "0A000", ddl.LoadUnsupported},
		{"$libdir", false, "0A000", ddl.LoadUnsupported},
		{"$unknown", false, "0A000", ddl.LoadUnsupported},
		{"/an/absolute/name", false, "0A000", ddl.LoadUnsupported},
		{"relative/name", false, "0A000", ddl.LoadUnsupported},
		{"$libdir/sepgsql", false, "0A000", ddl.LoadUnsupported},
		{"$bad/name", false, "42602", "invalid macro name in path: $bad/name"},
		{"$LIBDIR/name", false, "42602", "invalid macro name in path: $LIBDIR/name"},
		{"$libdir/plugins/", true, "0A000", ddl.LoadUnsupported},
		{"$libdir/plugins/..", true, "0A000", ddl.LoadUnsupported},
		{"$libdir/plugins/\\name", true, "0A000", ddl.LoadUnsupported},
		{"$libdir/plugins/name", true, "0A000", ddl.LoadUnsupported},
		{"$bad/name", true, "42501", "access to library \"$bad/name\" is not allowed"},
		{"$libdir/plugins/../name", true, "42501", "access to library \"$libdir/plugins/../name\" is not allowed"},
		{"$libdir/plugins//name", true, "42501", "access to library \"$libdir/plugins//name\" is not allowed"},
		{"$LIBDIR/plugins/name", true, "42501", "access to library \"$LIBDIR/plugins/name\" is not allowed"},
		{"quo\"te\nname", true, "42501", "access to library \"quo\"te\nname\" is not allowed"},
	} {
		var module ddl.LoadModule
		op := admin
		if tc.ordinary {
			op = reader
		}
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err == nil {
			module, err = ddl.Load(op.Context(), auth.View, &parser.Load{Filename: tc.filename})
		}
		e.Mu.Unlock()
		if module != 0 {
			t.Fatal("failed LOAD selected a module", module)
		}
		loadUnitDiagnostic(t, err, tc.code, tc.message)
	}
	for _, filename := range []string{"pg_overexplain", "pg_overexplain.so", "$libdir/pg_overexplain", "$libdir/pg_overexplain.so"} {
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(admin)
		var module ddl.LoadModule
		if err == nil {
			module, err = ddl.Load(admin.Context(), auth.View, &parser.Load{Filename: filename})
		}
		e.Mu.Unlock()
		if err != nil || module != ddl.LoadOverexplain || admin.Session.Settings.OverexplainLoaded() {
			t.Fatal("ordinary LOAD selection activated a module or lacked a concrete selection", filename, module, err)
		}
	}
}

func TestLoadExecutorNoPublication(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	if _, err := e.Execute(op, &parser.CreateRole{Name: parser.Identifier{Text: "load_reader"}}, 0, nil); err != nil {
		t.Fatal(err)
	}
	for _, ordinary := range []bool{false, true} {
		if ordinary {
			if _, err := e.Execute(op, &parser.RoleSelection{Name: "load_reader"}, 0, nil); err != nil {
				t.Fatal(err)
			}
		}
		state, roles, oid, revision, principal := e.Catalog.State, e.RoleStore.Published, e.Catalog.NextOID(), e.Catalog.NextRevision(), e.RoleStore.NextPrincipal()
		identity, authorizationRevision := op.RoleState(), op.Session.Authorization.Revision
		unchanged := func() {
			t.Helper()
			if e.Catalog.State != state || e.Catalog.State.Epoch() != state.Epoch() || e.RoleStore.Published != roles || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || e.RoleStore.NextPrincipal() != principal || op.RoleState() != identity || op.Session.Authorization.Revision != authorizationRevision {
				t.Fatal("LOAD published catalog/role/allocator/identity changes")
			}
		}
		for _, filename := range []string{"library", "$bad/name", "$libdir/plugins/name"} {
			stmt := &parser.Load{Filename: filename}
			if _, err := e.Describe(op, stmt); err != nil {
				t.Fatal("effect-free LOAD Describe:", err)
			}
			unchanged()
			result, err := e.Execute(op, stmt, 0, nil)
			code, message := "0A000", ddl.LoadUnsupported
			if ordinary && filename != "$libdir/plugins/name" {
				code, message = "42501", "access to library \""+filename+"\" is not allowed"
			} else if filename == "$bad/name" {
				code, message = "42602", "invalid macro name in path: $bad/name"
			}
			loadUnitDiagnostic(t, err, code, message)
			if result.Rows != nil || result.CommandTag != "" {
				t.Fatal("LOAD error published a result", result)
			}
			unchanged()
		}
		original := op.Ctx
		ctx, cancel := context.WithCancel(original)
		cancel()
		op.Ctx = ctx
		_, describeErr := e.Describe(op, &parser.Load{Filename: "$libdir/plugins/name"})
		_, executeErr := e.Execute(op, &parser.Load{Filename: "$libdir/plugins/name"}, 0, nil)
		op.Ctx = original
		if !errors.Is(describeErr, context.Canceled) || !errors.Is(executeErr, context.Canceled) {
			t.Fatal("LOAD cancellation", describeErr, executeErr)
		}
		unchanged()
	}
}

// Exercise the actual utility executor: permissive MAC is not SQL authority,
// and selecting the already registered provider is not overexplain activation.
func TestSepgsqlSimulationLoadDispatch(t *testing.T) {
	for _, mode := range []string{"enforcing", "permissive", "permissive_provider_only"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			installSQL := mode != "permissive_provider_only"
			policy, err := sepgsql.Compile(sepgsql.SimulationConfig{
				InstallSQL: installSQL, Permissive: mode != "enforcing",
				Policy: sepgsql.Policy{
					ID: "load-dispatch", Version: "1", Contexts: []string{"A", "P", "U"},
					InitialContext: "A", UnlabeledContext: "U",
					Rules:       []sepgsql.Rule{{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}}},
					DefaultSpec: "db_database * P\ndb_schema * P\ndb_procedure * P\n",
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			err = e.InstallSepgsqlSimulation(policy, installSQL)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			enginetest.RoleExecute(t, e, op, "CREATE ROLE load_dispatch_reader")
			enginetest.RoleExecute(t, e, op, "CREATE ROLE load_dispatch_super SUPERUSER")
			physical := op.Session.Settings
			aliases := []string{"pg_overexplain", "pg_overexplain.so", "$libdir/pg_overexplain", "$libdir/pg_overexplain.so"}
			filenames := append([]string{"$libdir/sepgsql"}, aliases...)

			// Each command must leave the complete published roots, allocators,
			// current identity, subject, policy and setting journal unchanged.
			execute := func(stmt *parser.Load, wantCode, wantMessage string, activated bool) {
				t.Helper()
				state, roles := e.Catalog.State, e.RoleStore.Published
				oid, revision, principal := e.Catalog.NextOID(), e.Catalog.NextRevision(), e.RoleStore.NextPrincipal()
				identity, authorizationRevision := op.RoleState(), op.Session.Authorization.Revision
				labelRoot, subject := state.SecurityLabelRoot(), op.Session.Sepgsql.Current()
				mark := physical.Savepoint()
				defaults, typed := maps.Clone(physical.Defaults), maps.Clone(physical.DefaultTyped)
				if _, err := e.Describe(op, stmt); err != nil {
					t.Fatal("LOAD Describe", err)
				}
				result, err := e.Execute(op, stmt, 0, nil)
				if wantCode != "" {
					loadUnitDiagnostic(t, err, wantCode, wantMessage)
				} else if err != nil {
					t.Fatal("LOAD execution", stmt.Filename, err)
				}
				if result.Rows != nil || wantCode != "" && result.CommandTag != "" {
					t.Fatal("unexpected LOAD result", result)
				}
				if e.Catalog.State != state || e.Catalog.State.Epoch() != state.Epoch() ||
					e.RoleStore.Published != roles || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision ||
					e.RoleStore.NextPrincipal() != principal || op.RoleState() != identity ||
					op.Session.Authorization.Revision != authorizationRevision || e.Sepgsql != policy ||
					e.Catalog.State.SecurityLabelRoot() != labelRoot || op.Session.Sepgsql.Current() != subject ||
					!reflect.DeepEqual(defaults, physical.Defaults) || !reflect.DeepEqual(typed, physical.DefaultTyped) ||
					!reflect.DeepEqual(mark, physical.Savepoint()) || physical.OverexplainLoaded() != activated {
					t.Fatal("LOAD changed publication, authority, policy, settings or wrong physical activation", stmt.Filename)
				}
			}

			// Hold parsed/prepared definitions while current SQL authority changes.
			held := make([]*parser.Load, len(filenames))
			for i, filename := range filenames {
				held[i] = &parser.Load{Filename: filename}
				if _, err := e.Describe(op, held[i]); err != nil {
					t.Fatal(err)
				}
			}
			if physical.OverexplainLoaded() {
				t.Fatal("Describe activated overexplain")
			}
			if mode == "enforcing" {
				for _, reader := range []bool{false, true} {
					if reader {
						enginetest.RoleExecute(t, e, op, "SET ROLE load_dispatch_reader")
					}
					for _, filename := range append(filenames, "$bad/name") {
						execute(&parser.Load{Filename: filename}, "42501", "SELinux: LOAD is not permitted", false)
					}
					for _, stmt := range held {
						execute(stmt, "42501", "SELinux: LOAD is not permitted", false)
					}
				}
				return
			}

			for _, role := range []string{"load_dispatch_reader", "load_dispatch_super"} {
				enginetest.RoleExecute(t, e, op, "SET ROLE "+role)
				if role == "load_dispatch_super" {
					for _, stmt := range held {
						if _, err := e.Describe(op, stmt); err != nil {
							t.Fatal("superuser LOAD Describe before role downgrade", err)
						}
					}
					peer, closePeer := enginetest.RoleOperation(t, e, "postgres")
					enginetest.RoleExecute(t, e, peer, "ALTER ROLE load_dispatch_super NOSUPERUSER")
					closePeer()
				}
				for _, stmt := range held {
					message := "access to library \"" + stmt.Filename + "\" is not allowed"
					execute(&parser.Load{Filename: stmt.Filename}, "42501", message, false)
					execute(stmt, "42501", message, false)
				}
				enginetest.RoleExecute(t, e, op, "RESET ROLE")
			}
			for range 2 {
				execute(&parser.Load{Filename: "$libdir/sepgsql"}, "", "", false)
				execute(held[0], "", "", false)
			}
			execute(&parser.Load{Filename: "$bad/name"}, "42602", "invalid macro name in path: $bad/name", false)
			for _, filename := range []string{"missing", "$libdir/plugins/missing", "sepgsql", "$libdir/sepgsql.so"} {
				execute(&parser.Load{Filename: filename}, "0A000", ddl.LoadUnsupported, false)
			}
			state, roles := e.Catalog.State, e.RoleStore.Published
			oid, revision, principal := e.Catalog.NextOID(), e.Catalog.NextRevision(), e.RoleStore.NextPrincipal()
			identity, authorizationRevision := op.RoleState(), op.Session.Authorization.Revision
			subject, labelRoot := op.Session.Sepgsql.Current(), state.SecurityLabelRoot()
			mark := physical.Savepoint()
			original := op.Ctx
			ctx, cancel := context.WithCancel(original)
			cancel()
			op.Ctx = ctx
			for _, filename := range filenames {
				_, err := e.Execute(op, &parser.Load{Filename: filename}, 0, nil)
				if !errors.Is(err, context.Canceled) || physical.OverexplainLoaded() {
					t.Fatal("canceled LOAD activated a module", filename, err)
				}
				result, err := schemacmds.ExecuteLoad(e, op, &parser.Load{Filename: filename}, 0)
				if !errors.Is(err, context.Canceled) || result.Rows != nil || result.CommandTag != "" ||
					physical.OverexplainLoaded() {
					t.Fatal("canceled real LOAD executor admitted a module", filename, result, err)
				}
			}
			op.Ctx = original
			if e.Catalog.State != state || e.RoleStore.Published != roles || e.Catalog.NextOID() != oid ||
				e.Catalog.NextRevision() != revision || e.RoleStore.NextPrincipal() != principal ||
				op.RoleState() != identity || op.Session.Authorization.Revision != authorizationRevision ||
				op.Session.Sepgsql.Current() != subject || e.Catalog.State.SecurityLabelRoot() != labelRoot ||
				e.Sepgsql != policy || !reflect.DeepEqual(mark, physical.Savepoint()) {
				t.Fatal("canceled LOAD changed publication or physical state")
			}
			for _, stmt := range held[1:] {
				for range 2 {
					execute(&parser.Load{Filename: stmt.Filename}, "", "", true)
					execute(stmt, "", "", true)
				}
			}
			// sepgsql cannot unload an already genuine overexplain selection either.
			execute(held[0], "", "", true)
			peer, closePeer := enginetest.RoleOperation(t, e, "postgres")
			if peer.Session.Settings.OverexplainLoaded() {
				t.Fatal("physical activation leaked to a fresh session")
			}
			closePeer()

			// The actual physical resource owner publishes terminal state. A
			// retained executor must reject both selected modules, not reactivate.
			if err := physical.EndSession(op.Session.SessionToken, session.CleanClose); err != nil {
				t.Fatal(err)
			}
			state, roles = e.Catalog.State, e.RoleStore.Published
			oid, revision, principal = e.Catalog.NextOID(), e.Catalog.NextRevision(), e.RoleStore.NextPrincipal()
			identity, authorizationRevision = op.RoleState(), op.Session.Authorization.Revision
			subject, labelRoot = op.Session.Sepgsql.Current(), state.SecurityLabelRoot()
			mark = physical.Savepoint()
			for _, filename := range filenames {
				result, err := schemacmds.ExecuteLoad(e, op, &parser.Load{Filename: filename}, 0)
				if err == nil || err.Error() != "stuntgres: session has ended" || result.Rows != nil ||
					result.CommandTag != "" || physical.OverexplainLoaded() || !physical.Ended {
					t.Fatal("ended physical owner admitted LOAD", filename, result, err)
				}
			}
			if e.Catalog.State != state || e.RoleStore.Published != roles || e.Catalog.NextOID() != oid ||
				e.Catalog.NextRevision() != revision || e.RoleStore.NextPrincipal() != principal ||
				op.RoleState() != identity || op.Session.Authorization.Revision != authorizationRevision ||
				op.Session.Sepgsql.Current() != subject || e.Catalog.State.SecurityLabelRoot() != labelRoot ||
				e.Sepgsql != policy || !reflect.DeepEqual(mark, physical.Savepoint()) ||
				physical.Defaults != nil || physical.Values != nil || physical.Typed != nil {
				t.Fatal("ended LOAD published catalog/role/allocator changes")
			}
		})
	}
}
