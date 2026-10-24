package engine

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPLpgSQLRaiseNoticeContext(t *testing.T) {
	e := New()
	creator := sessionStart(t,e,nil)
	var routines []*catalog.Procedure
	func() {
		op, err := creator.Acquire(t.Context())
		if err != nil { t.Fatal(err) }
		defer op.Done()
		setup, err := e.StartStatement(op)
		if err != nil { t.Fatal(err) }
		defer setup.Finish(nil)
		stage := e.BeginCatalogStage(e.CatalogRead())
		defer e.DiscardCatalogStage(stage)
		owner, ok := e.Roles().Lookup("postgres")
		if !ok { t.Fatal("missing fixture owner") }
		for i, tc := range []struct{ name, body string }{
			{"notice_unit_ok","BEGIN RAISE NOTICE 'hello' USING DETAIL='detail',HINT='hint',SCHEMA='sch',TABLE='tab',COLUMN='col',DATATYPE='dtype',CONSTRAINT='con'; RETURN 7; END"},
			{"notice_unit_bad","BEGIN RAISE NOTICE 'hello' USING DETAIL='detail',HINT='hint',SCHEMA='sch',TABLE='tab',COLUMN='col',DATATYPE='dtype',CONSTRAINT='con'; RAISE EXCEPTION 'failed'; END"},
			{"notice_unit_outer_ok","BEGIN RETURN notice_unit_ok(); END"},
			{"notice_unit_outer_bad","BEGIN RETURN notice_unit_bad(); END"},
		} {
			d := cursorOwnerRoutine(t,e,setup,tc.body)
			d.ObjectAddress = ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:ids.OID(920001+i)}
			d.ObjectNamespace = ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:ids.PublicNamespaceOID}
			d.ObjectName, d.SQL.Name, d.ObjectRevision, d.Owner = tc.name,tc.name,1,owner.Ref
			d.Result, d.RoutineKind, d.Volatility = ids.OID(sqltype.Int4),catalog.FunctionRoutine,expr.Volatile
			if err := stage.Create(d); err != nil { t.Fatal(err) }
			routines = append(routines,d)
		}
		op.Definition = stageDefinitions(stage)
		defer func() { op.Definition = nil }()
		e.mu.Lock()
		auth, err := e.authorizationLocked(op)
		if err == nil {
			auth.Recapture(e.stageRead(stage))
			auth.definition = op.Definition
			for _, d := range routines {
				if err = e.compilePLRoutineLocked(op,auth,d); err != nil { break }
			}
		}
		e.mu.Unlock()
		if err != nil { t.Fatal(err) }
		collationUnitPublish(t,e,stage)
	}()
	op, err := creator.Acquire(t.Context())
	if err != nil { t.Fatal(err) }
	t.Cleanup(op.Done)
	s, err := e.StartStatement(op)
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { s.Finish(nil) })
	if err := s.gate(&parser.Select{}); err != nil { t.Fatal(err) }
	s.worked = true
	input := proc.NewFrame(nil)
	defer input.Close()
	prior := sqlstate.Error{Severity:"INFO",SeverityUnlocalized:"INFO",Code:"00000",Message:"earlier",Context:"unchanged"}
	op.Notices = append(op.Notices,prior)
	original, depth := s.sqlRoutine, s.activeSQLRoutineDepth
	for _, tc := range []struct{ routine int; inner, outer string; failing bool }{
		{2,"notice_unit_ok()","notice_unit_outer_ok()",false},
		{3,"notice_unit_bad()","notice_unit_outer_bad()",true},
		{0,"notice_unit_ok()","",false},
	} {
		start := len(op.Notices)
		value, err := s.invokePLRoutine(routines[tc.routine],input,e.CatalogRead(),sqltype.CollationRef{})
		context := "PL/pgSQL function "+tc.inner+" line 1 at RAISE"
		if tc.outer != "" { context += "\nPL/pgSQL function "+tc.outer+" line 1 at RETURN" }
		if tc.failing {
			var diagnostic *sqlstate.Error
			if !errors.As(err,&diagnostic) || diagnostic.Code != "P0001" || diagnostic.Message != "failed" || diagnostic.Context != context { t.Fatalf("failed callee context: %#v",err) }
		} else {
			if err != nil { t.Fatal(err) }
			if n, err := value.Integer(); err != nil || n != 7 { t.Fatal("successful callee",n,err) }
		}
		want := sqlstate.Error{Severity:"NOTICE",SeverityUnlocalized:"NOTICE",Code:"00000",Message:"hello",Detail:"detail",Hint:"hint",Schema:"sch",Table:"tab",Column:"col",DataType:"dtype",Constraint:"con",Context:context}
		if !reflect.DeepEqual(op.Notices[start:],[]sqlstate.Error{want}) { t.Fatalf("new notice range: got %#v want %#v",op.Notices[start:],want) }
		if !reflect.DeepEqual(op.Notices[0],prior) { t.Fatal("caller decorated an earlier buffered record") }
		if s.sqlRoutine != original || s.activeSQLRoutineDepth != depth { t.Fatal("callee leaked execution scratch") }
	}
	// Invalid offsets are deliberately harmless for an empty range: no source
	// slicing, rendering or allocation may occur when execution emitted nothing.
	origin := plNoticeOrigin{position:parser.Position{Offset:1},action:"at RETURN"}
	if n := testing.AllocsPerRun(100,func() { s.appendPLNoticeContext(len(op.Notices),origin) }); n != 0 { t.Fatalf("empty notice range allocated %v",n) }
}
