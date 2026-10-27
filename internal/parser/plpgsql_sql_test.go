package parser

import (
	"reflect"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestPLpgSQLSQLExecutionParser(t *testing.T) {
	commands := []struct {
		text string
		kind PLSQLKind
		typ  reflect.Type
	}{
		{"SELECT /* é中 */ 'a;into'", PLCommandSQL, reflect.TypeOf(&Select{})},
		{"INSERT INTO q VALUES(1)", PLCommandSQL, reflect.TypeOf(&Insert{})},
		{"UPDATE q SET i=2", PLCommandSQL, reflect.TypeOf(&Update{})},
		{"DELETE FROM q", PLCommandSQL, reflect.TypeOf(&Delete{})},
		{"MERGE INTO q d USING (SELECT 1 AS i) s ON d.i=s.i WHEN MATCHED THEN UPDATE SET i=2", PLCommandSQL, reflect.TypeOf(&Merge{})},
		{"PERFORM /* é */ 1 FROM q", PLPerformSQL, reflect.TypeOf(&Select{})},
		{"PERFORM set_config('application_name',x::text,false)", PLPerformSQL, reflect.TypeOf(&Select{})},
		{"VALUES(1)", PLCommandSQL, reflect.TypeOf(&Select{})},
		{"CALL q(b=>x,a:=r.n)", PLCallSQL, reflect.TypeOf(&Call{})},
		{"CREATE TABLE q(i integer)", PLCommandSQL, reflect.TypeOf(&CreateTable{})},
		{"SET application_name='a;é'", PLCommandSQL, reflect.TypeOf(&Set{})},
	}
	for _, tc := range commands {
		source := "DECLARE x integer; r record; BEGIN " + tc.text + "; x:=2; END"
		block, err := ParsePLpgSQL(source, PLReturnPolicy{})
		if err != nil {
			t.Fatal(tc.text, err)
		}
		raw := block.Statements[0].SQL
		if raw == nil || !ValidPLSQLStatement(raw) || raw.Kind != tc.kind || reflect.TypeOf(raw.Query) != tc.typ || raw.Text != tc.text || raw.End != raw.Delimiter || source[raw.End.Offset] != ';' || raw.Start != block.Statements[0].Pos {
			t.Fatalf("ordinary AST/boundaries lost: %#v", raw)
		}
		for _, pos := range []Position{raw.Start, raw.End} {
			if pos.Character != utf8.RuneCountInString(source[:pos.Offset])+1 {
				t.Fatal("UTF8 position", pos)
			}
		}
		if block.Statements[1].Target == nil {
			t.Fatal("SQL stole following assignment")
		}
		for _, suffix := range []string{" junk", "; SELECT 1 +; END"} {
			if _, err := ParsePLpgSQL(source+suffix, PLReturnPolicy{}); err == nil {
				t.Fatal("accepted malformed suffix", suffix)
			}
		}
	}
	source := "DECLARE perform integer; r record; c CURSOR FOR SELECT 1; BEGIN perform:=1; SELECT 1 INTO perform; FOR r IN SELECT 2 LOOP PERFORM r.n; END LOOP; OPEN c; END"
	block, err := ParsePLpgSQL(source, PLReturnPolicy{})
	if err != nil || block.Statements[0].Target == nil || block.Statements[1].Into == nil || block.Statements[2].QueryLoop == nil || block.Statements[3].Cursor == nil {
		t.Fatal("contextual head/capture/cursor boundary", err)
	}
	captured, err := ParsePLpgSQL("DECLARE x integer; BEGIN MERGE INTO q d USING s ON d.i=s.i WHEN MATCHED THEN DELETE RETURNING d.i INTO x; END", PLReturnPolicy{})
	if err != nil {
		t.Fatal("MERGE relation/destination INTO", err)
	}
	capture := captured.Statements[0].Into
	if capture == nil || len(capture.Targets) != 1 {
		t.Fatal("MERGE RETURNING lost its procedural destination", capture)
	}
	merged, ok := capture.Query.(*Merge)
	if !ok || merged.Alias.Text != "d" || merged.Returning == nil || len(merged.Returning.Targets) != 1 {
		t.Fatal("MERGE relation INTO or alias was stripped", capture.Query)
	}
	retained := block.Statements[2].QueryLoop.Body.Statements[0].SQL
	before := *retained
	identifiers, err := ParsePLpgSQL("DECLARE call integer; perform integer; BEGIN call:=1; perform:=2; CALL q(call); PERFORM perform; END", PLReturnPolicy{})
	if err != nil || identifiers.Statements[0].Target == nil || identifiers.Statements[1].Target == nil || identifiers.Statements[2].SQL.Kind != PLCallSQL || identifiers.Statements[3].SQL.Kind != PLPerformSQL {
		t.Fatal("contextual CALL/PERFORM stole identifiers", err)
	}
	for _, declarations := range []string{
		"perform integer; call integer;",
		"call integer; perform integer;",
		"x integer:=1; perform integer:=x; call integer; c CURSOR FOR SELECT perform;",
	} {
		for _, nested := range []bool{false, true} {
			source := "DECLARE " + declarations + " BEGIN call:=1; perform:=2; CALL q(call); PERFORM perform; END"
			if nested {
				source = "BEGIN " + source + "; PERFORM 1; END"
			}
			block, err := ParsePLpgSQL(source, PLReturnPolicy{})
			if err != nil {
				t.Fatal("declaration boundary", source, err)
			}
			if nested {
				if len(block.Statements) != 2 || block.Statements[0].Block == nil || block.Statements[1].SQL == nil || block.Statements[1].SQL.Kind != PLPerformSQL {
					t.Fatal("nested declaration stole outer statement", source)
				}
				block = block.Statements[0].Block
			}
			if len(block.Statements) != 4 || block.Statements[0].Target == nil || block.Statements[1].Target == nil || block.Statements[2].SQL == nil || block.Statements[2].SQL.Kind != PLCallSQL || block.Statements[3].SQL == nil || block.Statements[3].SQL.Kind != PLPerformSQL {
				t.Fatal("declaration/command boundary", source)
			}
			call := block.Statements[2].SQL.Query.(*Call)
			actual, ok := call.Function.Args[0].(ColumnExpr)
			if !ok || len(actual.Name.Parts) != 1 || actual.Name.Parts[0].Text != "call" {
				t.Fatal("CALL identifier actual", source, call.Function.Args)
			}
			query := block.Statements[3].SQL.Query.(*Select)
			target, ok := query.Targets[0].(ColumnExpr)
			if !ok || len(target.Name.Parts) != 1 || target.Name.Parts[0].Text != "perform" {
				t.Fatal("PERFORM identifier projection", source, query.Targets)
			}
		}
	}
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := ParsePLpgSQL("BEGIN PERFORM 'é;'; CALL q(NULL); END", PLReturnPolicy{})
			failures <- err
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if *retained != before || !strings.HasPrefix(retained.Text, "PERFORM") {
		t.Fatal("another parse mutated retained AST")
	}
}
