package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPLpgSQLCursorSourceDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, body, code, message, query, position, context string }{
		{"static_query", "DECLARE c refcursor; BEGIN OPEN c FOR SELECT missing_input; RETURN 0; END", "42703", `column "missing_input" does not exist`, "SELECT missing_input", "8", "PL/pgSQL function cursor_owner_unit() line 1 at OPEN"},
		{"static_planning", "DECLARE c refcursor; BEGIN OPEN c FOR SELECT 1/0; RETURN 0; END", "22012", "division by zero", "", "", "SQL statement \"SELECT 1/0\"\nPL/pgSQL function cursor_owner_unit() line 1 at OPEN"},
		{"dynamic_planning", "DECLARE c refcursor; BEGIN OPEN c FOR EXECUTE 'SELECT 1/0'; RETURN 0; END", "22012", "division by zero", "", "", "SQL statement \"SELECT 1/0\"\nPL/pgSQL function cursor_owner_unit() line 1 at OPEN"},
		{"bound_action_origin", "DECLARE\n c CURSOR FOR\n SELECT /*é中*/ missing_input;\nBEGIN\n OPEN\n c;\nRETURN 0; END", "42703", `column "missing_input" does not exist`, "SELECT /*é中*/ missing_input", "15", "PL/pgSQL function cursor_owner_unit() line 5 at OPEN"},
		{"utf8_named_projection", "DECLARE c CURSOR(a text,\"Odd 名\" integer) FOR SELECT \"Odd 名\"; BEGIN OPEN c(\"Odd 名\"=>missing_input,a=>'é中'); RETURN 0; END", "42703", `column "missing_input" does not exist`, `'é中' AS a, missing_input AS "Odd 名"`, "12", "PL/pgSQL function cursor_owner_unit() line 1 at OPEN"},
		{"bound_simple_uses_spi", "DECLARE c CURSOR(a integer) FOR SELECT a; n integer:=0; BEGIN OPEN c(1/n); RETURN 0; END", "22012", "division by zero", "", "", "PL/pgSQL expression \"1/n\"\nPL/pgSQL function cursor_owner_unit() line 1 at OPEN"},
		{"count_simple", "DECLARE c refcursor; x integer; n integer:=0; BEGIN OPEN c SCROLL FOR SELECT 7; FETCH ABSOLUTE 1/n FROM c INTO x; RETURN 0; END", "22012", "division by zero", "", "", "PL/pgSQL function cursor_owner_unit() line 1 at FETCH"},
		{"count_conversion", "DECLARE c refcursor; x integer; BEGIN OPEN c SCROLL FOR SELECT 7; FETCH ABSOLUTE 'bad'::text FROM c INTO x; RETURN 0; END", "22P02", `invalid input syntax for type integer: "bad"`, "", "", "PL/pgSQL function cursor_owner_unit() line 1 at FETCH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, s := cursorUnitScope(t)
			d := cursorOwnerRoutine(t, e, s, tc.body)
			before, err := parser.ParsePLpgSQL(tc.body, parser.PLReturnPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			frame := proc.NewFrame(nil)
			defer frame.Close()
			_, err = s.invokePLRoutine(d, frame, e.CatalogRead(), sqltype.CollationRef{})
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, InternalQuery: tc.query, InternalPosition: tc.position, Context: tc.context}
			var got *sqlstate.Error
			if !errors.As(err, &got) || *got != want {
				t.Fatalf("diagnostic got %+v want %+v (%v)", got, want, err)
			}
			if !reflect.DeepEqual(before, d.SQL.PLSyntax) {
				t.Fatal("execution mutated original source metadata")
			}
			if s.completed || s.sessionCompleted {
				t.Fatal("PL failure completed the containing command")
			}
		})
	}
}

func TestPLpgSQLCursorWholeArgumentProjection(t *testing.T) {
	for _, tc := range []struct {
		name, source                   string
		value                          int64
		code, message, query, position string
	}{
		{"first_row", "DECLARE c CURSOR(a integer) FOR SELECT a; x integer; BEGIN OPEN c(generate_series(1,3)); FETCH c INTO x; CLOSE c; RETURN x; END", 1, "", "", "", ""},
		{"empty_row_found", "DECLARE c CURSOR(a integer) FOR SELECT a; x integer; BEGIN OPEN c(generate_series(1,0)); IF FOUND THEN RETURN -1; END IF; FETCH c INTO x; IF NOT FOUND THEN RETURN -2; END IF; CLOSE c; IF x IS NULL THEN RETURN 7; END IF; RETURN -3; END", 7, "", "", "", ""},
		{"bind_all_before_planning", "DECLARE c CURSOR(a integer,b integer) FOR SELECT a+b; BEGIN OPEN c(1/0,missing_input); RETURN 0; END", 0, "42703", `column "missing_input" does not exist`, "1/0, missing_input", "6"},
		{"evaluate_all_before_conversion", "DECLARE c CURSOR(a integer,b integer) FOR SELECT a+b; n integer:=0; BEGIN OPEN c('bad',1/n); RETURN 0; END", 0, "22012", "division by zero", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, s := cursorUnitScope(t)
			d := cursorOwnerRoutine(t, e, s, tc.source)
			frame := proc.NewFrame(nil)
			defer frame.Close()
			value, err := s.invokePLRoutine(d, frame, e.CatalogRead(), sqltype.CollationRef{})
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				got, err := value.Integer()
				if err != nil || got != tc.value {
					t.Fatal("first-row/FOUND result", value, err)
				}
			} else {
				context := "PL/pgSQL function cursor_owner_unit() line 1 at OPEN"
				if tc.name == "evaluate_all_before_conversion" {
					context = "PL/pgSQL expression \"'bad', 1/n\"\n" + context
				}
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, InternalQuery: tc.query, InternalPosition: tc.position, Context: context}
				var got *sqlstate.Error
				if !errors.As(err, &got) || *got != want {
					t.Fatalf("whole projection got %+v want %+v (%v)", got, want, err)
				}
			}
			if len(payloadOf(s.op.Session).cursors.entries) != 0 {
				t.Fatal("failed or closed bound projection published a portal")
			}
		})
	}
}

func TestPLpgSQLCursorLookupBeforeCount(t *testing.T) {
	for _, action := range []string{"FETCH", "MOVE"} {
		for _, count := range []string{"NULL::integer", "1/n", "missing_count", "nextval('missing_count_sequence')"} {
			t.Run(action+"/"+count, func(t *testing.T) {
				e, s := cursorUnitScope(t)
				motion := "FETCH ABSOLUTE " + count + " FROM c INTO x;"
				if action == "MOVE" {
					motion = "MOVE FORWARD " + count + " FROM c;"
				}
				d := cursorOwnerRoutine(t, e, s, "DECLARE c refcursor:='pc_absent'; x integer; n integer:=0; BEGIN "+motion+" RETURN 0; END")
				frame := proc.NewFrame(nil)
				defer frame.Close()
				_, err := s.invokePLRoutine(d, frame, e.CatalogRead(), sqltype.CollationRef{})
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "34000", Message: `cursor "pc_absent" does not exist`, Context: "PL/pgSQL function cursor_owner_unit() line 1 at " + action}
				var got *sqlstate.Error
				if !errors.As(err, &got) || *got != want {
					t.Fatalf("missing portal evaluated/bound count: %+v (%v)", got, err)
				}
				if len(payloadOf(s.op.Session).cursors.entries) != 0 || s.completed || s.sessionCompleted {
					t.Fatal("lookup failure changed root or registry")
				}
			})
		}
	}
	t.Run("retained_identity", func(t *testing.T) {
		_, s := cursorUnitScope(t)
		registry := payloadOf(s.op.Session).cursors
		original := &sqlCursor{tx: s.txRef, epoch: s.txRef.Epoch()}
		registry.entries["retained"] = original
		retained, err := s.resolveCursorPortal("retained")
		if err != nil || retained != original {
			t.Fatal("lookup did not retain exact portal", err)
		}
		replacement := &sqlCursor{tx: s.txRef, epoch: s.txRef.Epoch()}
		registry.entries["retained"] = replacement
		_, err = s.fetchCursorPortal(retained, &parser.FetchCursor{Name: parser.Identifier{Text: "retained"}, Count: 1})
		var got *sqlstate.Error
		if !errors.As(err, &got) || got.Code != "34000" || registry.entries["retained"] != replacement || replacement.Place != exec.CursorBefore {
			t.Fatal("motion redirected retained identity to replacement", err)
		}
		delete(registry.entries, "retained")
	})
}

func TestPLpgSQLCursorInstallationNames(t *testing.T) {
	e, s := cursorUnitScope(t)
	read := e.CatalogRead()
	integer, _ := read.LookupType(sqltype.Int4)
	seven, _ := sqltype.Integer(integer, 7)
	root := &plan.ProjectNode{Child: &plan.SingletonNode{}, Expressions: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: seven}}}
	root.QuerySchema = []catalog.Column{{Name: "i", Usage: integer.Usage()}}
	declaration := &parser.DeclareCursor{Name: parser.Identifier{Text: "immutable_request"}}
	install := func(name *string, closed *int) (*sqlCursor, string, error) {
		restore := s.activateSQLRoutine(nil, read)
		s.authority = &exec.Authority{Host: hostOf(s)}
		s.inquiry.commentCatalog = read
		source := &exec.Source{Singleton: true, Locks: &exec.LockContext{}, NestedStage: &exec.Stage{Finish: func() error { (*closed)++; return nil }}}
		cursor, actual, err := s.installCursor(declaration, name, root, source, nil, true)
		return cursor, actual, restore(err)
	}
	closed := 0
	first, firstName, err := install(nil, &closed)
	if err != nil {
		t.Fatal(err)
	}
	second, secondName, err := install(nil, &closed)
	if err != nil {
		t.Fatal(err)
	}
	registry := payloadOf(s.op.Session).cursors
	if firstName == "" || firstName == secondName || registry.entries[firstName] != first || registry.entries[secondName] != second || first.ordinal >= second.ordinal || declaration.Name.Text != "immutable_request" || closed != 0 {
		t.Fatal("late allocation changed syntax, identity or live ownership")
	}
	ordinal := registry.ordinal
	duplicate, actual, err := install(&firstName, &closed)
	var got *sqlstate.Error
	if !errors.As(err, &got) || got.Code != "42P03" || duplicate != nil || actual != "" || registry.entries[firstName] != first || registry.ordinal != ordinal || closed != 1 {
		t.Fatal("final install overwrote a live identity or leaked failed source", err)
	}
	for _, name := range []string{firstName, secondName} {
		if _, err := s.closeCursor(&parser.CloseCursor{Name: parser.Identifier{Text: name}}); err != nil {
			t.Fatal(err)
		}
	}
	if closed != 3 || len(registry.entries) != 0 || s.completed || s.sessionCompleted {
		t.Fatal(fmt.Sprintf("name ownership cleanup: %d", closed))
	}
}

func TestPLpgSQLCursorFirstRowDemand(t *testing.T) {
	e, _ := cursorUnitScope(t)
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	seven, _ := sqltype.Integer(integer, 7)
	root := &plan.ProjectNode{
		Child:       &plan.SingletonNode{},
		QuerySchema: []catalog.Column{{Name: "a", Usage: integer.Usage()}, {Name: "b", Usage: integer.Usage()}},
		Expressions: []expr.Expr{
			plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: seven},
			plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: seven},
		},
	}
	// The shared producer's wider argument-row demand must not relax the
	// incumbent SQL scalar routine's one-column guard or evaluate its outputs.
	rows, count, err := finalizeSQLRoutineQuery(context.Background(), root, &exec.Source{Singleton: true}, nil, true)
	if err == nil || rows != nil || count != 0 {
		t.Fatal("scalar routine accepted a multi-column projection")
	}
	rows, count, err = finalizeSQLRoutineQuery(context.Background(), root, &exec.Source{Singleton: true}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	final, ok := rows.(*exec.FinalRows)
	if !ok || count != 1 || len(final.Columns) != 2 || len(final.Values) != 2 || final.Values[0] != seven || final.Values[1] != seven {
		t.Fatal("argument projection did not use the ordinary typed first row")
	}
}
