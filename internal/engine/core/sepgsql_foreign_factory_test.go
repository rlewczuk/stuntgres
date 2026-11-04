package core_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/sqlstate"
)

type sepgsqlForeignMethods struct {
	fdwUnitWrapper
	observe func()
	events  []sepgsql.AuditEvent
}

func (p *sepgsqlForeignMethods) GetForeignRelSize(context.Context, catalog.ForeignRequest) (catalog.ForeignRelSize, error) {
	if p.observe != nil {
		p.observe()
	}
	return catalog.ForeignRelSize{Rows: 0}, nil
}

func sepgsqlForeignFactoryEngine(t *testing.T) (*core.Engine, *sepgsqlForeignMethods, exec.ForeignHandlerSelection) {
	t.Helper()
	e := engine.New()
	provider := &sepgsqlForeignMethods{fdwUnitWrapper: fdwUnitWrapper{paths: []catalog.ForeignPath{{}}, scan: &fdwUnitScan{position: 3}}}
	definition, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "sepgsql_foreign_handler"}, Wrapper: provider})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.InstallForeignDefinitions([]fdw.WrapperDefinition{definition}, nil, fdwIntegerImport, fdwIntegerExport); err != nil {
		t.Fatal(err)
	}
	runtime, err := sepgsql.Compile(sepgsql.SimulationConfig{InstallSQL: true, AuditAllowed: true, Audit: func(event sepgsql.AuditEvent) {
		provider.events = append(provider.events, event)
	}, Policy: sepgsql.Policy{
		ID: "foreign-factory-owner", Version: "1", Contexts: []string{"A", "R", "T", "P", "S", "U"}, InitialContext: "A", UnlabeledContext: "U",
		DefaultSpec: "db_database * P\ndb_schema * P\ndb_table * P\ndb_column * P\ndb_procedure * P\n",
		Rules: []sepgsql.Rule{
			{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}},
			{Source: "R", Target: "*", Class: "*", Allow: []string{"*"}},
			{Source: "R", Target: "S", Class: "db_procedure", Deny: []string{"execute"}},
			{Source: "R", Target: "U", Class: "db_procedure", Deny: []string{"entrypoint"}},
		},
		TrustedTransitions: []sepgsql.TrustedTransition{{Source: "R", Procedure: "S", Result: "T"}, {Source: "R", Procedure: "U", Result: "T"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	err = e.InstallSepgsqlSimulation(runtime, true)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION sepgsql_foreign_handler() IS 'S'")
	enginetest.Run(t, e, "CREATE FOREIGN DATA WRAPPER sepgsql_factory HANDLER sepgsql_foreign_handler")
	enginetest.Run(t, e, "CREATE SERVER sepgsql_factory FOREIGN DATA WRAPPER sepgsql_factory")
	enginetest.Run(t, e, "CREATE USER MAPPING FOR PUBLIC SERVER sepgsql_factory")
	enginetest.Run(t, e, "CREATE FOREIGN TABLE sepgsql_factory() SERVER sepgsql_factory")
	read := e.CatalogRead()
	address := read.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "sepgsql_foreign_handler", Signature: string(catalog.ForeignHandlerSignature)})
	return e, provider, exec.SelectForeignHandler(read, address)
}

func TestSepgsqlSimulationForeignFactory(t *testing.T) {
	t.Run("no_importer_factory_before_capability", func(t *testing.T) {
		e, provider, _ := sepgsqlForeignFactoryEngine(t)
		if _, ok := any(provider).(fdw.SchemaImporter); ok {
			t.Fatal("fixture unexpectedly implements import")
		}
		physical := enginetest.StartSession(t, e, nil)
		for _, query := range []string{
			`CREATE FOREIGN DATA WRAPPER "SQL import alias" HANDLER sepgsql_foreign_handler`,
			`CREATE SERVER sepgsql_no_import FOREIGN DATA WRAPPER "SQL import alias"`,
			"CREATE SCHEMA sepgsql_import",
			"CREATE FOREIGN DATA WRAPPER sepgsql_no_handler NO HANDLER",
			"CREATE SERVER sepgsql_no_handler FOREIGN DATA WRAPPER sepgsql_no_handler",
			"CREATE ROLE sepgsql_import_reader",
			"GRANT USAGE ON FOREIGN SERVER sepgsql_no_import TO sepgsql_import_reader",
			"GRANT USAGE,CREATE ON SCHEMA sepgsql_import TO sepgsql_import_reader",
			"REVOKE ALL ON FUNCTION sepgsql_foreign_handler() FROM PUBLIC",
			"SELECT sepgsql_setcon('R')",
		} {
			enginetest.SessionSQL(t, physical, query)
		}
		const query = "IMPORT FOREIGN SCHEMA remote FROM SERVER sepgsql_no_import INTO sepgsql_import"
		check := func(sql, code, message, target string) {
			t.Helper()
			before := e.CatalogRead().State()
			provider.events = nil
			// Host audit delivery belongs to Operation.Done, not scope Finish.
			// Isolate this IMPORT in its own real operation on the SAME session;
			// deferred release delivers its detached audit before we inspect it.
			func() {
				op, err := physical.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				enginetest.RoleError(t, e, op, sql, code, message, "")
				if e.CatalogRead().State() != before || op.Session.Sepgsql.Current() != "R" {
					t.Fatal("failed import published catalog/labels or changed subject")
				}
				if provider.scan.position != 3 || provider.scan.ends != 0 || provider.chosen.Attributes != nil {
					t.Fatal("unsupported import entered scan methods")
				}
			}()
			entry, transition := false, false
			for _, event := range provider.events {
				if event.Class == "db_procedure" {
					if target == "" || event.Target != target || event.Subject != "R" {
						t.Fatal("unexpected factory audit", event)
					}
					if slices.Contains(event.Required, "execute") {
						t.Fatal("direct import factory fabricated execute initialization", event)
					}
					if slices.Contains(event.Required, "entrypoint") {
						entry = true
						if (code == "42501") != slices.Contains(event.Denied, "entrypoint") {
							t.Fatal("wrong factory entry audit", event)
						}
					}
				}
				if event.Class == "process" && slices.Contains(event.Required, "transition") {
					transition = true
				}
			}
			if target != "" && (!entry || code == "HV00P" && !transition) {
				t.Fatal("missing genuine factory entry/transition audit", provider.events)
			}
			if target == "" && (entry || transition) {
				t.Fatal("ordinary metadata error reached factory", provider.events)
			}
		}
		enginetest.SessionSQL(t, physical, "SET ROLE sepgsql_import_reader")
		check(query, "HV00P", `foreign-data wrapper "SQL import alias" does not support IMPORT FOREIGN SCHEMA`, "S")
		enginetest.SessionSQL(t, physical, "RESET ROLE")
		enginetest.SessionSQL(t, physical, "SELECT sepgsql_setcon(NULL)")
		enginetest.SessionSQL(t, physical, `ALTER FOREIGN DATA WRAPPER "SQL import alias" RENAME TO "Renamed SQL alias"`)
		enginetest.SessionSQL(t, physical, "SECURITY LABEL ON FUNCTION sepgsql_foreign_handler() IS 'U'")
		enginetest.SessionSQL(t, physical, "SELECT sepgsql_setcon('R')")
		check(query, "42501", "SELinux: security policy violation", "U")
		check("IMPORT FOREIGN SCHEMA remote FROM SERVER absent_import_server INTO sepgsql_import", "42704", `server "absent_import_server" does not exist`, "")
		check("IMPORT FOREIGN SCHEMA remote FROM SERVER sepgsql_no_import INTO absent_import_schema", "3F000", `schema "absent_import_schema" does not exist`, "")
		check("IMPORT FOREIGN SCHEMA remote FROM SERVER sepgsql_no_handler INTO sepgsql_import", "55000", `foreign-data wrapper "sepgsql_no_handler" has no handler`, "")
		enginetest.SessionSQL(t, physical, "SELECT sepgsql_setcon(NULL)")
		enginetest.SessionSQL(t, physical, "REVOKE CREATE ON SCHEMA sepgsql_import FROM sepgsql_import_reader")
		enginetest.SessionSQL(t, physical, "SELECT sepgsql_setcon('R')")
		enginetest.SessionSQL(t, physical, "SET ROLE sepgsql_import_reader")
		check(query, "42501", "permission denied for schema sepgsql_import", "")
		enginetest.SessionSQL(t, physical, "RESET ROLE")
		enginetest.SessionSQL(t, physical, "SELECT sepgsql_setcon(NULL)")
		enginetest.SessionSQL(t, physical, "REVOKE USAGE ON FOREIGN SERVER sepgsql_no_import FROM sepgsql_import_reader")
		enginetest.SessionSQL(t, physical, "SELECT sepgsql_setcon('R')")
		enginetest.SessionSQL(t, physical, "SET ROLE sepgsql_import_reader")
		check(query, "42501", "permission denied for foreign server sepgsql_no_import", "")
		enginetest.SessionSQL(t, physical, "RESET ROLE")
		enginetest.SessionSQL(t, physical, "SELECT sepgsql_setcon(NULL)")
		enginetest.SessionSQL(t, physical, "SECURITY LABEL ON FUNCTION sepgsql_foreign_handler() IS 'S'")
		enginetest.SessionSQL(t, physical, "SELECT sepgsql_setcon('R')")
		check(query, "HV00P", `foreign-data wrapper "Renamed SQL alias" does not support IMPORT FOREIGN SCHEMA`, "S")
	})
	t.Run("actual_table_restores_before_methods_and_unused_capture", func(t *testing.T) {
		e, provider, selected := sepgsqlForeignFactoryEngine(t)
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		defer scope.Finish(nil)
		if err := scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		auth, err := scope.RoutineAuthorizationLocked()
		if err == nil {
			err = scope.CaptureIdentityLocked(auth)
		}
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		provider.observe = func() {
			if got := op.Session.Sepgsql.Current(); got != "R" {
				t.Errorf("raw method inherited handler subject %q", got)
			}
		}
		methods, err := selected.Methods(core.HostOf(scope))
		if err != nil || methods != provider {
			t.Fatal("selected table not returned", methods, err)
		}
		if _, err := methods.GetForeignRelSize(op.Context(), catalog.ForeignRequest{}); err != nil {
			t.Fatal(err)
		}
		if op.Session.Sepgsql.Current() != "R" {
			t.Fatal("factory leaked lexical subject")
		}
		if _, err := selected.Methods(nil); err == nil {
			t.Fatal("missing borrowed owner accepted")
		}
		foreign := selected
		foreign.Reference = ids.ObjectRef{}
		if _, err := foreign.Methods(core.HostOf(scope)); err == nil {
			t.Fatal("foreign reference accepted")
		}
		scope.Finish(nil)
		if _, err := selected.Methods(core.HostOf(scope)); err == nil {
			t.Fatal("closed owner accepted")
		}
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon(NULL)")
		enginetest.RoleExecute(t, e, op, "SECURITY LABEL ON FUNCTION sepgsql_foreign_handler() IS 'U'")
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
		// Metadata capture for a rejected predicate is not a handler invocation.
		enginetest.RoleExecute(t, e, op, "SELECT count(*) FROM sepgsql_factory WHERE false")
		enginetest.RoleError(t, e, op, "SELECT count(*) FROM sepgsql_factory", "42501", "SELinux: security policy violation", "")
		if op.Session.Sepgsql.Current() != "R" {
			t.Fatal("denied factory changed caller")
		}
	})
	t.Run("original_selection_stale_and_cancel", func(t *testing.T) {
		e, _, selected := sepgsqlForeignFactoryEngine(t)
		enginetest.Run(t, e, "ALTER FUNCTION sepgsql_foreign_handler() RENAME TO sepgsql_foreign_renamed")
		s := enginetest.StartSession(t, e, nil)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		op, err := s.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		defer scope.Finish(nil)
		if err := scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		auth, err := scope.RoutineAuthorizationLocked()
		if err == nil {
			err = scope.CaptureIdentityLocked(auth)
		}
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := selected.Methods(core.HostOf(scope)); err != nil {
			t.Fatal("rename lost original capability", err)
		}
		changed := *selected.Procedure
		changed.Foreign = &catalog.ForeignProcedure{}
		stale := selected
		stale.Procedure = &changed
		if _, err := stale.Methods(core.HostOf(scope)); err == nil {
			t.Fatal("changed installation adopted original authority")
		}
		cancel()
		_, err = selected.Methods(core.HostOf(scope))
		var diagnostic *sqlstate.Error
		if !errors.Is(err, context.Canceled) && (!errors.As(err, &diagnostic) || diagnostic.Code != "57014") {
			t.Fatal("canceled factory accepted", err)
		}
		if op.Session.Sepgsql.Current() != "R" {
			t.Fatal("cancel leaked handler context")
		}
	})
}
