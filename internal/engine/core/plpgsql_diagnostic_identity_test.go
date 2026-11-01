package core_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPLpgSQLRaiseDiagnosticCache(t *testing.T) {
	e := engine.New()
	creator := enginetest.StartSession(t, e, nil)
	setting, err := e.Settings.Lookup("search_path")
	if err != nil {
		t.Fatal(err)
	}
	setPath := func(scope *core.StatementScope, path string) {
		t.Helper()
		if _, err := core.EngineApplySetting(e, scope.Op, scope.Op.Session.Settings, setting, &path, false); err != nil {
			t.Fatal(err)
		}
	}
	var d *catalog.Procedure
	// Publish real validated descriptors before admitting the operation that
	// executes the query-loop producer. A sparse candidate read alone is not
	// the live catalog/role authority that producer admission requires.
	func() {
		op, err := creator.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		setup, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		defer setup.Finish(nil)
		base := e.CatalogRead()
		stage := e.BeginCatalogStage(base)
		defer e.DiscardCatalogStage(stage)
		public, _ := base.Namespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
		owner, ok := e.Roles().Lookup("postgres")
		if !ok {
			t.Fatal("missing fixture owner")
		}
		namespace := catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 910001}, ObjectName: "pl_cache_schema", ObjectRevision: 1, Owner: owner.Ref}
		if err := stage.Create(&namespace); err != nil {
			t.Fatal(err)
		}
		d = cursorOwnerRoutine(t, e, setup, "DECLARE r record; BEGIN FOR r IN SELECT 1 AS v LOOP RAISE EXCEPTION 'cached'; END LOOP; RETURN 7; END")
		d.ObjectAddress = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 910002}
		d.ObjectNamespace, d.ObjectName, d.SQL.Name = namespace.Address(), "cache_fn", "cache_fn"
		d.ObjectRevision, d.Owner = 1, owner.Ref
		d.Result, d.RoutineKind, d.Volatility = ids.OID(sqltype.Int4), catalog.FunctionRoutine, expr.Volatile
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
		shadow := *d
		shadow.ObjectAddress = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 910003}
		shadow.ObjectNamespace = public.Address()
		if err := stage.Create(&shadow); err != nil {
			t.Fatal(err)
		}
		op.Definition = core.StageDefinitions(stage)
		defer func() { op.Definition = nil }()
		// The target namespace is searchable, but its exact signature is masked.
		setPath(setup, "public,pl_cache_schema")
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err == nil {
			auth.Recapture(e.StageRead(stage))
			auth.Definition = op.Definition
			err = e.CompilePLRoutineLocked(op, auth, d)
		}
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		collationUnitPublish(t, e, stage)
	}()
	enginetest.SessionSQL(t, creator, "CREATE TABLE pl_cache_parent(id integer)")
	op, err := creator.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	s, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Finish(nil) })
	if err := s.Gate(&parser.Select{}); err != nil {
		t.Fatal(err)
	}
	core.SetScopeWorked(s, true)
	read := e.CatalogRead()
	program := d.SQL.PL
	input := proc.NewFrame(nil)
	defer input.Close()
	invoke := func(scope *core.StatementScope, routine *catalog.Procedure, identity string) {
		t.Helper()
		before := core.RoutineDemandPLDiagnostic(core.ScopeSQLRoutineRef(scope))
		_, err := core.ScopeInvokePLRoutine(scope, routine, input, read, sqltype.CollationRef{})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "P0001" || diagnostic.Message != "cached" || diagnostic.Context != "PL/pgSQL function "+identity+" line 1 at RAISE" {
			t.Fatalf("cached identity diagnostic: %#v", err)
		}
		if core.RoutineDemandPLDiagnostic(core.ScopeSQLRoutineRef(scope)) != before {
			t.Fatal("failed nested invocation leaked identity")
		}
	}
	assertCache := func(scope *core.StatementScope, routine *catalog.Procedure, identity string) {
		t.Helper()
		e.Mu.Lock()
		retained := core.PayloadSQLRoutines(core.PayloadOf(scope.Op.Session))[d.Address()]
		e.Mu.Unlock()
		if core.RoutineAnalysisPLProcedure(&retained) != routine || core.RoutineAnalysisPLDiagnostic(&retained) != identity {
			t.Fatalf("first-compile cache owner/identity: got %p %q, want %p %q",
				core.RoutineAnalysisPLProcedure(&retained), core.RoutineAnalysisPLDiagnostic(&retained), routine, identity)
		}
	}
	core.SetRoutineDemandPLDiagnostic(core.ScopeSQLRoutineRef(s), "outer_identity()")
	invoke(s, d, "pl_cache_schema.cache_fn()")
	setPath(s, "pl_cache_schema,public")
	invoke(s, d, "pl_cache_schema.cache_fn()")
	assertCache(s, d, "pl_cache_schema.cache_fn()")
	// Each invocation owner starts without the creating validator's cache.
	newScope := func() *core.StatementScope {
		t.Helper()
		op, done, err := enginetest.Operation(e, t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(done)
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { scope.Finish(nil) })
		if err := scope.Gate(&parser.Select{}); err != nil {
			t.Fatal(err)
		}
		core.SetScopeWorked(scope, true)
		core.SetRoutineDemandPLDiagnostic(core.ScopeSQLRoutineRef(scope), "independent_outer()")
		assertCache(scope, nil, "")
		return scope
	}
	fresh := newScope()
	setPath(fresh, "pl_cache_schema,public")
	invoke(fresh, d, "cache_fn()")
	setPath(fresh, "public,pl_cache_schema")
	invoke(fresh, d, "cache_fn()")
	parent := read.MustRelation(enginetest.CatalogRef(t, e, read, "pl_cache_parent").Address)
	maintenanceInvoke := func(scope *core.StatementScope, routine *catalog.Procedure, identity string) {
		t.Helper()
		previous := scope.Op.Restricted
		if err := scope.WithMaintenanceOwner(parent, func(*exec.Context) error {
			invoke(scope, routine, identity)
			assertCache(scope, routine, identity)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if scope.Op.Restricted != previous {
			t.Fatal("maintenance invocation leaked restriction")
		}
	}
	// Warm first-compile presentation wins even when the function is no longer
	// visible on the ordinary path or on maintenance's safe path.
	maintenanceInvoke(fresh, d, "cache_fn()")
	assertCache(fresh, d, "cache_fn()")
	invoke(fresh, d, "cache_fn()")
	maintenanceInvoke(s, d, "pl_cache_schema.cache_fn()")
	assertCache(s, d, "pl_cache_schema.cache_fn()")

	// A genuinely cold executor compiles under maintenance's safe path. That
	// qualified signature lives in the same session cache after owner unwind,
	// rather than being a transient frame-only override.
	cold := newScope()
	setPath(cold, "pl_cache_schema,public")
	maintenanceInvoke(cold, d, "pl_cache_schema.cache_fn()")
	invoke(cold, d, "pl_cache_schema.cache_fn()")
	assertCache(cold, d, "pl_cache_schema.cache_fn()")
	setPath(cold, "public,pl_cache_schema")
	invoke(cold, d, "pl_cache_schema.cache_fn()")
	assertCache(fresh, d, "cache_fn()")
	assertCache(s, d, "pl_cache_schema.cache_fn()")

	// The same address/source/typed layout cannot lend an old declaration's
	// signature to a replacement. Its first environment owns the new spelling.
	replacement := *d
	maintenanceInvoke(fresh, &replacement, "pl_cache_schema.cache_fn()")
	setPath(fresh, "pl_cache_schema,public")
	invoke(fresh, &replacement, "pl_cache_schema.cache_fn()")
	assertCache(fresh, &replacement, "pl_cache_schema.cache_fn()")
	assertCache(s, d, "pl_cache_schema.cache_fn()")
	assertCache(cold, d, "pl_cache_schema.cache_fn()")
	invoke(s, &replacement, "cache_fn()")
	assertCache(s, &replacement, "cache_fn()")
	maintenanceInvoke(s, &replacement, "cache_fn()")
	invoke(s, &replacement, "cache_fn()")
	assertCache(fresh, &replacement, "pl_cache_schema.cache_fn()")
	if d.SQL.PL != program || replacement.SQL.PL != program {
		t.Fatal("session signature mutated persisted program")
	}
	restore := s.ActivateSQLRoutine(nil, read)
	if core.RoutineDemandPLDiagnostic(core.ScopeSQLRoutineRef(s)) != "" {
		t.Fatal("nested SQL activation inherited PL identity")
	}
	core.SetRoutineDemandPLDiagnostic(core.ScopeSQLRoutineRef(s), "nested_identity()")
	if err := restore(nil); err != nil {
		t.Fatal(err)
	}
	if core.RoutineDemandPLDiagnostic(core.ScopeSQLRoutineRef(s)) != "outer_identity()" {
		t.Fatal("lexical scratch failed to restore PL identity")
	}
}
