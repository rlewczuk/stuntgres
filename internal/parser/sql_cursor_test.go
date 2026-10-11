package parser

import (
	"errors"
	"math"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestSQLCursorGrammar(t *testing.T) {
	for _, q := range []string{
		`DECLARE "Mixed" NO SCROLL CURSOR FOR SELECT i FROM public.t ORDER BY i`,
		`DECLARE "Mixed" CURSOR WITHOUT HOLD FOR VALUES (1),(2)`,
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		cursor, ok := statements[0].(*DeclareCursor)
		if !ok || cursor.Name.Text != "Mixed" || cursor.Name.Pos.Offset != 8 || cursor.Pos.Offset != 0 || cursor.Query == nil || cursor.Hold {
			t.Fatal(q, statements[0])
		}
	}
	for _, test := range []struct {
		query         string
		count         int64
		all, backward bool
	}{
		{"FETCH 1 FROM c", 1, false, false},
		{"FETCH FORWARD +2 IN c", 2, false, false},
		{"FETCH FORWARD -2147483647 FROM c", -math.MaxInt32, false, false},
		{"FETCH FORWARD 2147483647 FROM c", math.MaxInt32, false, false},
		{"FETCH c", 1, false, false},
		{"FETCH FORWARD FROM c", 1, false, false},
		{"FETCH ALL FROM c", 0, true, false},
		{"FETCH FORWARD ALL IN c", 0, true, false},
		{"FETCH BACKWARD ALL FROM c", 0, true, true},
		{"FETCH BACKWARD 1 FROM c", 1, false, true},
	} {
		statements, err := Parse(test.query)
		if err != nil || len(statements) != 1 {
			t.Fatal(test.query, statements, err)
		}
		cursor, ok := statements[0].(*FetchCursor)
		if !ok || cursor.Name.Text != "c" || cursor.Count != test.count || cursor.All != test.all || (cursor.Direction == CursorBackward) != test.backward {
			t.Fatal(test.query, statements[0])
		}
	}
	for _, q := range []string{`CLOSE "Mixed"`, "CLOSE ALL"} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, err)
		}
		close := statements[0].(*CloseCursor)
		if close.All != (q == "CLOSE ALL") || (!close.All && close.Name.Text != "Mixed") {
			t.Fatal(close)
		}
	}
}

func TestSQLCursorMotionGrammar(t *testing.T) {
	for _, test := range []struct {
		sql       string
		direction CursorDirection
		count     int64
		move      bool
	}{
		{"FETCH NEXT FROM c", CursorForward, 1, false},
		{"FETCH PRIOR IN c", CursorBackward, 1, false},
		{"FETCH FIRST c", CursorAbsolute, 1, false},
		{"FETCH LAST c", CursorAbsolute, -1, false},
		{"FETCH ABSOLUTE -2 c", CursorAbsolute, -2, false},
		{"FETCH RELATIVE +2 c", CursorRelative, 2, false},
		{"MOVE BACKWARD -2 c", CursorBackward, -2, true},
		{"MOVE RELATIVE 0 c", CursorRelative, 0, true},
	} {
		statements, err := Parse(test.sql)
		if err != nil || len(statements) != 1 {
			t.Fatal(test.sql, err)
		}
		m := statements[0].(*FetchCursor)
		if m.Direction != test.direction || m.Count != test.count || m.Move != test.move || m.Name.Text != "c" {
			t.Fatal(test.sql, m)
		}
	}
	for _, mode := range []struct {
		text string
		want CursorScroll
	}{
		{"", CursorScrollDefault}, {"NO SCROLL", CursorNoScroll}, {"SCROLL", CursorScrollEnabled},
	} {
		statements, err := Parse("DECLARE c " + mode.text + " CURSOR FOR SELECT 1")
		if err != nil || statements[0].(*DeclareCursor).Scroll != mode.want {
			t.Fatal(mode, statements, err)
		}
	}
	for _, count := range []string{"2147483648", "-2147483648", "1.0", "1e1"} {
		for _, command := range []string{"FETCH", "MOVE"} {
			_, err := Parse(command + " ABSOLUTE " + count + " FROM c")
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
				t.Fatal(command, count, err)
			}
		}
	}
}
func TestSQLCursorFullInputAndExcludedCapabilities(t *testing.T) {
	for _, q := range []string{"FETCH FORWARD $1 FROM c", "FETCH 1+1 FROM c", "FETCH 1 FROM c trailing", "CLOSE c trailing", "DECLARE c NO SCROLL CURSOR FOR SELECT 1 junk junk"} {
		statements, err := Parse(q)
		if err == nil || len(statements) != 0 {
			t.Fatal("partial cursor syntax accepted", q, statements, err)
		}
	}
	statements, err := Parse("FETCH FORWARD 9223372036854775808 FROM c")
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || len(statements) != 0 {
		t.Fatal(statements, err)
	}
	// The utility FETCH entry must not consume SELECT's existing bound syntax.
	statements, err = Parse("SELECT i FROM t FETCH FIRST 2 ROWS ONLY")
	if err != nil || len(statements) != 1 || statements[0].(*Select).Bounds == nil {
		t.Fatal(statements, err)
	}
}
func TestSQLCursorQueryTraversal(t *testing.T) {
	statements, err := Parse("DECLARE c NO SCROLL CURSOR FOR SELECT a.i,(SELECT b.i FROM b) FROM a")
	if err != nil {
		t.Fatal(err)
	}
	queries, tables := 0, 0
	err = VisitQuerySources(statements[0], func(*Select) error { queries++; return nil }, func(*TableReference, *Select) error { tables++; return nil })
	if err != nil || queries != 2 || tables != 2 {
		t.Fatal(queries, tables, err)
	}
	statements, err = Parse("SELECT declare,cursor,close,scroll,hold,forward,backward,absolute,relative FROM t")
	if err != nil || len(statements) != 1 {
		t.Fatal("unreserved cursor words lost name legality", statements, err)
	}
}

func TestSQLCursorHoldGrammar(t *testing.T) {
	for _, clause := range []string{"", " WITHOUT HOLD", " WITH HOLD"} {
		query := `DECLARE "Mixed" NO SCROLL CURSOR` + clause + " FOR SELECT 1"
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
		cursor := statements[0].(*DeclareCursor)
		if cursor.Hold != (clause == " WITH HOLD") || cursor.Name.Text != "Mixed" || cursor.Name.Pos.Offset != 8 || cursor.Pos.Offset != 0 || cursor.Query == nil {
			t.Fatal(query, cursor)
		}
	}
	for _, query := range []string{"DECLARE c CURSOR WITH HOLD FOR SELECT 1 junk junk", "DECLARE c CURSOR WITH HOLD", "DECLARE c CURSOR WITH HOLD WITH HOLD FOR SELECT 1"} {
		if statements, err := Parse(query); err == nil || len(statements) != 0 {
			t.Fatal("partial hold declaration accepted", query, statements, err)
		}
	}
}
