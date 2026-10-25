package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPLpgSQLRaiseDiagnosticCache(t *testing.T) {
	e := New()
	creator := sessionStart(t,e,nil)
	setting, err := e.Settings.Lookup("search_path")
	if err != nil { t.Fatal(err) }
	setPath := func(scope *StatementScope, path string) {
		t.Helper()
		if _, err := e.applySetting(scope.Op,scope.Op.Session.Settings,setting,&path,false); err != nil { t.Fatal(err) }
	}
	var d *catalog.Procedure
	// Publish real validated descriptors before admitting the operation that
	// executes the query-loop producer. A sparse candidate read alone is not
	// the live catalog/role authority that producer admission requires.
	func() {
		op, err := creator.Acquire(t.Context())
		if err != nil { t.Fatal(err) }
		defer op.Done()
		setup, err := e.StartStatement(op)
		if err != nil { t.Fatal(err) }
		defer setup.Finish(nil)
		base := e.CatalogRead()
		stage := e.BeginCatalogStage(base)
		defer e.DiscardCatalogStage(stage)
		public, _ := base.Namespace(ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:ids.PublicNamespaceOID})
		owner, ok := e.Roles().Lookup("postgres")
		if !ok { t.Fatal("missing fixture owner") }
		namespace := catalog.Namespace{ObjectAddress:ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:910001},ObjectName:"pl_cache_schema",ObjectRevision:1,Owner:owner.Ref}
		if err := stage.Create(&namespace); err != nil { t.Fatal(err) }
		d = cursorOwnerRoutine(t,e,setup,"DECLARE r record; BEGIN FOR r IN SELECT 1 AS v LOOP RAISE EXCEPTION 'cached'; END LOOP; RETURN 7; END")
		d.ObjectAddress = ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:910002}
		d.ObjectNamespace, d.ObjectName, d.SQL.Name = namespace.Address(), "cache_fn", "cache_fn"
		d.ObjectRevision, d.Owner = 1,owner.Ref
		d.Result, d.RoutineKind, d.Volatility = ids.OID(sqltype.Int4),catalog.FunctionRoutine,expr.Volatile
		if err := stage.Create(d); err != nil { t.Fatal(err) }
		shadow := *d
		shadow.ObjectAddress = ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:910003}
		shadow.ObjectNamespace = public.Address()
		if err := stage.Create(&shadow); err != nil { t.Fatal(err) }
		op.Definition = StageDefinitions(stage)
		defer func() { op.Definition = nil }()
		// The target namespace is searchable, but its exact signature is masked.
		setPath(setup,"public,pl_cache_schema")
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err == nil {
			auth.Recapture(e.StageRead(stage))
			auth.Definition = op.Definition
			err = e.CompilePLRoutineLocked(op,auth,d)
		}
		e.Mu.Unlock()
		if err != nil { t.Fatal(err) }
		collationUnitPublish(t,e,stage)
	}()
	op, err := creator.Acquire(t.Context())
	if err != nil { t.Fatal(err) }
	t.Cleanup(op.Done)
	s, err := e.StartStatement(op)
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { s.Finish(nil) })
	if err := s.Gate(&parser.Select{}); err != nil { t.Fatal(err) }
	s.worked = true
	read := e.CatalogRead()
	program := d.SQL.PL
	input := proc.NewFrame(nil)
	defer input.Close()
	invoke := func(scope *StatementScope, routine *catalog.Procedure, identity string) {
		t.Helper()
		before := scope.sqlRoutine.plDiagnostic
		_, err := scope.invokePLRoutine(routine,input,read,sqltype.CollationRef{})
		var diagnostic *sqlstate.Error
		if !errors.As(err,&diagnostic) || diagnostic.Code != "P0001" || diagnostic.Message != "cached" || diagnostic.Context != "PL/pgSQL function "+identity+" line 1 at RAISE" { t.Fatalf("cached identity diagnostic: %#v",err) }
		if scope.sqlRoutine.plDiagnostic != before { t.Fatal("failed nested invocation leaked identity") }
	}
	s.sqlRoutine.plDiagnostic = "outer_identity()"
	invoke(s,d,"pl_cache_schema.cache_fn()")
	setPath(s,"pl_cache_schema,public")
	invoke(s,d,"pl_cache_schema.cache_fn()")
	if d.SQL.PL != program { t.Fatal("session signature mutated persisted program") }
	// An absent session entry captures this session's first-compile visibility,
	// independently of the creating session's validator cache.
	freshOp, done, err := testOperation(e,t.Context())
	if err != nil { t.Fatal(err) }
	t.Cleanup(done)
	fresh, err := e.StartStatement(freshOp)
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { fresh.Finish(nil) })
	if err := fresh.Gate(&parser.Select{}); err != nil { t.Fatal(err) }
	fresh.worked = true
	setPath(fresh,"pl_cache_schema,public")
	invoke(fresh,d,"cache_fn()")
	setPath(fresh,"public,pl_cache_schema")
	invoke(fresh,d,"cache_fn()")
	// A replacement immutable carrier invalidates its predecessor's signature;
	// unchanged source/typed layout alone cannot authorize stale presentation.
	replacement := *d
	invoke(s,&replacement,"cache_fn()")
	e.Mu.Lock()
	retained := PayloadOf(s.Op.Session).sqlRoutines[d.Address()]
	e.Mu.Unlock()
	if retained.plProcedure != &replacement || retained.plDiagnostic != "cache_fn()" { t.Fatal("replacement carrier did not own recaptured signature") }
	restore := s.activateSQLRoutine(nil,read)
	if s.sqlRoutine.plDiagnostic != "" { t.Fatal("nested SQL activation inherited PL identity") }
	s.sqlRoutine.plDiagnostic = "nested_identity()"
	if err := restore(nil); err != nil { t.Fatal(err) }
	if s.sqlRoutine.plDiagnostic != "outer_identity()" { t.Fatal("lexical scratch failed to restore PL identity") }
}
