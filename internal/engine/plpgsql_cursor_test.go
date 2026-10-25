package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func cursorOwnerRoutine(t *testing.T, e *Engine, s *StatementScope, source string) *catalog.Procedure {
	t.Helper()
	read := e.CatalogRead()
	integer, _ := read.LookupType(sqltype.Int4)
	language, err := catalog.LookupLanguage(read, "plpgsql")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := parser.ParsePLpgSQL(source, proc.PLReturnPolicy(integer.Usage(), 0, false))
	if err != nil {
		t.Fatal(err)
	}
	d := &catalog.Procedure{ObjectName: "cursor_owner_unit", Language: language.Address(), SQL: &proc.Definition{Kind: proc.PLpgSQLBody, Name: "cursor_owner_unit", Source: source, Result: integer.Usage(), PLSyntax: raw}}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err == nil {
		err = e.CompilePLRoutineLocked(s.Op, auth, d)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func cursorUnitScope(t *testing.T) (*Engine, *StatementScope) {
	t.Helper()
	e, s := plUnitScope(t)
	if err := s.Gate(&parser.Select{}); err != nil {
		t.Fatal(err)
	}
	s.worked = true
	return e, s
}

func TestPLpgSQLCursorOwnership(t *testing.T) {
	t.Run("lower_primitives_and_frame_lifetime", func(t *testing.T) {
		e, s := cursorUnitScope(t)
		read := e.CatalogRead()
		integer, _ := read.LookupType(sqltype.Int4)
		seven, _ := sqltype.Integer(integer, 7)
		column := catalog.Column{Name: "i", Usage: integer.Usage()}
		root := &plan.ProjectNode{QuerySchema: []catalog.Column{column}, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: seven}}}
		outer := &proc.PLFrame{}
		s.sqlRoutine.lexical = outer
		parentAuthority := s.authority
		restore := s.activateSQLRoutine(nil, read)
		s.authority = &exec.Authority{Host: HostOf(s)}
		s.Inquiry.CommentCatalog = read
		source := &exec.Source{Singleton: true, Locks: &exec.LockContext{}}
		declaration := &parser.DeclareCursor{Name: parser.Identifier{Text: "unit_owned"}}
		cursor, _, err := s.installCursor(declaration, &declaration.Name.Text, root, source, nil, true)
		err = restore(err)
		if err != nil {
			t.Fatal(err)
		}
		if s.Completed || s.sessionCompleted || s.sqlRoutine.lexical != outer || s.authority != parentAuthority || cursor.stage != nil || cursor.authority.Host != nil {
			t.Fatal("PL installation changed root flags/scratch or eagerly opened producer")
		}
		frame := &proc.PLFrame{Values: []sqltype.Value{seven}}
		frame.Close()
		if PayloadOf(s.Op.Session).Cursors.Entries["unit_owned"] != cursor {
			t.Fatal("ordinary portal attached to frame lifetime")
		}
		calls := 0
		cursor.stage = &exec.Stage{Finish: func() error {
			calls++
			if !e.Mu.TryLock() {
				t.Error("callback under engine lock")
			} else {
				e.Mu.Unlock()
			}
			if !s.Owner.Mu.TryLock() {
				t.Error("callback under transaction lock")
			} else {
				s.Owner.Mu.Unlock()
			}
			if !s.Op.Session.Mu.TryLock() {
				t.Error("callback under session lock")
			} else {
				s.Op.Session.Mu.Unlock()
			}
			if PayloadOf(s.Op.Session).Cursors.Entries["unit_owned"] != nil {
				t.Error("callback before registry detach")
			}
			return nil
		}}
		if _, err = s.closeCursor(&parser.CloseCursor{Name: parser.Identifier{Text: "unit_owned"}}); err != nil {
			t.Fatal(err)
		}
		if err = cursor.dispose(context.Background()); err != nil {
			t.Fatal(err)
		}
		if calls != 1 || s.Completed || s.sessionCompleted || cursor.Source != nil || cursor.authority.Host != nil {
			t.Fatal("lower close completion/once-only disposal", calls)
		}
	})
	for _, cancelOpen := range []bool{false, true} {
		name := "invalid_projection"
		if cancelOpen {
			name = "canceled_publication"
		}
		t.Run(name, func(t *testing.T) {
			e, s := cursorUnitScope(t)
			s.Inquiry.CommentCatalog = e.CatalogRead()
			s.authority = &exec.Authority{Host: HostOf(s)}
			calls := 0
			source := &exec.Source{NestedStage: &exec.Stage{Finish: func() error {
				calls++
				if !e.Mu.TryLock() {
					t.Error("open failure cleanup under lock")
				} else {
					e.Mu.Unlock()
				}
				return nil
			}}}
			var root plan.Node = &plan.SingletonNode{}
			if cancelOpen {
				root = &plan.ProjectNode{Child: &plan.SingletonNode{}}
				original := s.Op.Ctx
				ctx, cancel := context.WithCancel(original)
				cancel()
				s.Op.Ctx = ctx
				defer func() { s.Op.Ctx = original }()
			}
			declaration := &parser.DeclareCursor{Name: parser.Identifier{Text: "failed_owner"}}
			_, _, err := s.installCursor(declaration, &declaration.Name.Text, root, source, nil, false)
			if err == nil || cancelOpen && !errors.Is(err, context.Canceled) {
				t.Fatal("missing primary open failure", err)
			}
			source.Close()
			if calls != 1 || PayloadOf(s.Op.Session).Cursors.Entries["failed_owner"] != nil || s.Completed || s.sessionCompleted {
				t.Fatal("partial open ownership", calls)
			}
		})
	}
	for _, exit := range []string{"normal", "return", "error"} {
		t.Run("loop_"+exit, func(t *testing.T) {
			e, s := cursorUnitScope(t)
			body := "n:=r.i;"
			if exit == "return" {
				body = "RETURN r.i;"
			}
			if exit == "error" {
				body = "n:=1/(r.i-7);"
			}
			d := cursorOwnerRoutine(t, e, s, "DECLARE c CURSOR FOR SELECT 7 AS i; n integer:=0; BEGIN c:='unit_loop'; FOR r IN c LOOP "+body+" END LOOP; RETURN n; END")
			frame := proc.NewFrame(nil)
			defer frame.Close()
			before := PayloadOf(s.Op.Session).Cursors.ordinal
			_, err := s.invokePLRoutine(d, frame, e.CatalogRead(), sqltype.CollationRef{})
			if exit == "error" {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			registry := PayloadOf(s.Op.Session).Cursors
			if registry.Entries["unit_loop"] != nil || registry.ordinal != before+1 || s.Completed || s.sessionCompleted {
				t.Fatal("loop ownership did not unwind before transaction finish")
			}
		})
	}
	t.Run("admitted_loop_cancellation", func(t *testing.T) {
		e, s := cursorUnitScope(t)
		holder, provenance := advisoryUnitOwner(t, e)
		key := lock.Resource{Database: e.Catalog.Domain, Kind: lock.AdvisoryBigintResource, Low: 987654321}
		advisoryUnitGrant(t, holder, provenance, key, lock.AdvisoryExclusive)
		defer advisoryUnitUnlock(t, holder, key, lock.AdvisoryExclusive, true)
		d := cursorOwnerRoutine(t, e, s, "DECLARE c CURSOR FOR SELECT 7 AS i; f boolean; BEGIN c:='unit_cancel_loop'; FOR r IN c LOOP f:=pg_advisory_lock(987654321::bigint) IS NULL; END LOOP; RETURN 0; END")
		original := s.Op.Ctx
		ctx, cancel := context.WithCancel(original)
		s.Op.Ctx = ctx
		done := make(chan error, 1)
		joined := false
		t.Cleanup(func() {
			cancel()
			if !joined {
				<-done
				joined = true
			}
			s.Op.Ctx = original
		})
		go func() {
			frame := proc.NewFrame(nil)
			defer frame.Close()
			_, err := s.invokePLRoutine(d, frame, e.CatalogRead(), sqltype.CollationRef{})
			done <- err
		}()
		// This real lock queue proves admission into the loop body, after the
		// automatic portal was installed; no timer/sleep guesses its phase.
		lockPending(t, e, 1)
		active := PayloadOf(s.Op.Session).Cursors.Entries["unit_cancel_loop"]
		if active == nil || !active.pinned {
			t.Fatal("admitted loop lacks portal protection")
		}
		cancel()
		select {
		case err := <-done:
			joined = true
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("admitted cancellation did not finish")
		}
		if active.pinned || PayloadOf(s.Op.Session).Cursors.Entries["unit_cancel_loop"] != nil || s.Completed || s.sessionCompleted {
			t.Fatal("canceled loop retained portal/root completion")
		}
	})
	t.Run("transaction_and_session_detach", func(t *testing.T) {
		_, s := cursorUnitScope(t)
		r := PayloadOf(s.Op.Session).Cursors
		for _, end := range []string{"transaction", "session"} {
			calls := 0
			c := &sqlCursor{tx: s.TxRef, pinned: true, stage: &exec.Stage{Finish: func() error { calls++; return nil }}}
			r.Entries["unit_detach"] = c
			var err error
			if end == "transaction" {
				err = disposeSQLCursors(context.Background(), r.endTransaction(s.TxRef))
			} else {
				err = r.EndSession(r.token, session.CleanClose)
			}
			if err != nil || c.pinned || calls != 1 || r.Entries["unit_detach"] != nil {
				t.Fatal("end owner", end, calls, err)
			}
			if err = c.dispose(context.Background()); err != nil || calls != 1 {
				t.Fatal("duplicate teardown", err, calls)
			}
		}
	})
}

func TestPLpgSQLCursorTargetAtomicity(t *testing.T) {
	e, s := plUnitScope(t)
	read := e.CatalogRead()
	integer, _ := read.LookupType(sqltype.Int4)
	boolean, _ := read.LookupType(sqltype.Bool)
	text, _ := read.LookupType(sqltype.Text)
	seven, _ := sqltype.Integer(integer, 7)
	eight, _ := sqltype.Integer(integer, 8)
	f := &proc.PLFrame{Program: &proc.PLProgram{FoundSlot: 0, Variables: []proc.PLVariable{{Name: "found", Usage: boolean.Usage(), Role: proc.PLFoundSlot}, {Name: "first", Usage: integer.Usage()}, {Name: "second", Usage: integer.Usage(), NotNull: true}, {Name: "r", Record: true}}}, Values: []sqltype.Value{sqltype.Boolean(boolean, false), seven, seven, {}}}
	defer f.Close()
	cursor := &sqlCursor{schema: []catalog.Column{{Name: "i", Usage: integer.Usage()}, {Name: "t", Usage: text.Usage()}}}
	err := s.assignPLCursorTargets(s.newPLRun(nil, f, read), f, []proc.PLTarget{{Slot: 1}, {Slot: 2}}, cursor, []sqltype.Value{eight, sqltype.Null(integer)}, read)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22004" || f.Values[1] != seven || f.Values[2] != seven {
		t.Fatal("failed target list partially published", err)
	}
	if err = s.assignPLCursorTargets(s.newPLRun(nil, f, read), f, []proc.PLTarget{{Slot: 3}}, cursor, []sqltype.Value{sqltype.Null(integer), sqltype.Null(text)}, read); err != nil {
		t.Fatal(err)
	}
	row := f.Values[3]
	if row.IsNull() || row.Type() == nil || row.CompositeLen() != 2 || !row.CompositeField(0).IsNull() || row.CompositeField(0).Type() != integer || !row.CompositeField(1).IsNull() {
		t.Fatal("empty FETCH erased RECORD shape")
	}
	shape := row.Type()
	if err = s.assignPLCursorTargets(s.newPLRun(nil, f, read), f, []proc.PLTarget{{Slot: 3}}, cursor, []sqltype.Value{seven, sqltype.String(text, "seven")}, read); err != nil || f.Values[3].Type() != shape || !row.CompositeField(0).IsNull() {
		t.Fatal("refill mutated prior value or rebuilt descriptor", err)
	}
	field := func(name string) proc.PLTarget {
		return proc.PLTarget{Slot: 3, Steps: []parser.TargetStep{{Field: &parser.Identifier{Text: name}}}}
	}
	if err = s.assignPLCursorTargets(s.newPLRun(nil, f, read), f, []proc.PLTarget{field("i"), field("t")}, cursor, []sqltype.Value{eight, sqltype.String(text, "eight")}, read); err != nil {
		t.Fatal(err)
	}
	updated := f.Values[3]
	if !sqltype.SameDatum(updated.CompositeField(0), eight) || !sqltype.SameDatum(updated.CompositeField(1), sqltype.String(text, "eight")) {
		t.Fatal("detached writes to the same RECORD lost a sibling")
	}
	err = s.assignPLCursorTargets(s.newPLRun(nil, f, read), f, []proc.PLTarget{field("t"), field("i")}, cursor, []sqltype.Value{sqltype.String(text, "changed"), sqltype.String(text, "bad")}, read)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22P02" || f.Values[3] != updated || !sqltype.SameDatum(updated.CompositeField(1), sqltype.String(text, "eight")) {
		t.Fatal("failed field transfer published or mutated its detached ancestor", err)
	}
}

func TestPLpgSQLCursorExpressionProvenance(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		trace        bool
	}{
		{"preparation", "BEGIN RETURN 1/0; END", true},
		{"simple_runtime", "DECLARE x integer:=0; BEGIN RETURN 1/x; END", false},
		{"complex_runtime", "BEGIN RETURN (SELECT 1/zero FROM (VALUES(0)) AS t(zero)); END", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, s := cursorUnitScope(t)
			d := cursorOwnerRoutine(t, e, s, tc.source)
			frame := proc.NewFrame(nil)
			defer frame.Close()
			_, err := s.invokePLRoutine(d, frame, e.CatalogRead(), sqltype.CollationRef{})
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
				t.Fatal(err)
			}
			want := "PL/pgSQL function cursor_owner_unit() line 1 at RETURN"
			if tc.trace {
				raw := d.SQL.PLSyntax.Statements[0]
				want = "PL/pgSQL expression \"" + parser.PLExpressionSource(d.SQL.Source, raw.ExpressionStart, raw.ExpressionEnd) + "\"\n" + want
			}
			if diagnostic.Context != want {
				t.Fatalf("context got %q want %q", diagnostic.Context, want)
			}
		})
	}
	cause := context.Canceled
	if !errors.Is(&proc.PLExpressionExecutionError{Err: cause, Simple: true}, cause) {
		t.Fatal("execution provenance hid cancellation")
	}
}

func TestPLpgSQLCursorPositionedDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, source, line string
	}{
		{"assignment", "DECLARE x integer; BEGIN x := 'bad'::integer; RETURN x; END", "1"},
		{"rethrow", "DECLARE x integer; BEGIN x := 'bad'::integer; RETURN x; EXCEPTION WHEN OTHERS THEN RAISE; END", "1"},
		{"utf8_rethrow", "DECLARE s text := 'é中'; x integer;\nBEGIN\nx := 'bad'::integer;\nRETURN x;\nEXCEPTION WHEN OTHERS THEN RAISE;\nEND", "3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, s := cursorUnitScope(t)
			d := cursorOwnerRoutine(t, e, s, tc.source)
			frame := proc.NewFrame(nil)
			defer frame.Close()
			_, err := s.invokePLRoutine(d, frame, e.CatalogRead(), sqltype.CollationRef{})
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "22P02" {
				t.Fatal(err)
			}
			wantContext := "PL/pgSQL function cursor_owner_unit() line " + tc.line + " at assignment"
			if diagnostic.Position != "" || diagnostic.InternalPosition != "6" || diagnostic.InternalQuery != "x := 'bad'::integer" || diagnostic.Context != wantContext {
				t.Fatalf("positioned preparation diagnostic: %+v", diagnostic)
			}
			if tc.name == "assignment" {
				var planning *proc.PLExpressionPlanningError
				if !errors.As(err, &planning) || !errors.Is(err, planning.Err) {
					t.Fatal("assignment decoration lost preparation cause", err)
				}
			}
		})
	}
}

func TestPLpgSQLCursorTargetBoundsOrder(t *testing.T) {
	e, s := plUnitScope(t)
	read := e.CatalogRead()
	array, _ := read.LookupType(1007)
	text, _ := read.LookupType(sqltype.Text)
	boolean, _ := read.LookupType(sqltype.Bool)
	initial, err := array.Input(s.Op.Context(), "{1,2}", -1)
	if err != nil {
		t.Fatal(err)
	}
	f := &proc.PLFrame{Program: &proc.PLProgram{FoundSlot: 0, Variables: []proc.PLVariable{
		{Name: "found", Usage: boolean.Usage(), Role: proc.PLFoundSlot},
		{Name: "a", Usage: array.Usage()},
	}}, Values: []sqltype.Value{sqltype.Boolean(boolean, false), initial}}
	defer f.Close()
	for _, tc := range []struct{ path, code, message string }{
		{"a[NULL::integer]", "22004", "array subscript in assignment must not be null"},
		{"a[1]", "22P02", `invalid input syntax for type integer: "bad"`},
	} {
		target := plUnitAssignmentTarget(t, tc.path)
		target.Slot = 1
		_, err := s.preparePLTargetValue(s.newPLRun(nil, f, read), f, target, sqltype.String(text, "bad"), initial, read)
		domainUnitDiagnostic(t, err, tc.code, tc.message)
		if f.Values[1] != initial {
			t.Fatal("detached target preparation published its root")
		}
	}
}
