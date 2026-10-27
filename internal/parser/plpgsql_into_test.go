package parser

import (
	"errors"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"unicode/utf8"
)

func TestPLpgSQLSelectIntoParser(t *testing.T) {
	for _, query := range []string{
		`SELECT /* é中 */ 1 INTO STRICT x FROM q`,
		`SELECT INTO x 9`,
		`SELECT INTO x,y 9,10`,
		`SELECT 1 FROM q INTO x`,
		`SELECT 'into strict' AS "into" INTO "strict"`,
		`SELECT (SELECT 1) INTO x`,
		`WITH c AS (SELECT 1 AS i) SELECT i INTO x FROM c`,
		`WITH c AS (INSERT INTO q VALUES(1) RETURNING i) SELECT i INTO x FROM c`,
		`INSERT INTO q VALUES(1) RETURNING i INTO x`,
		`UPDATE q SET i=1 RETURNING i INTO x`,
		`DELETE FROM q RETURNING i INTO x`,
		`SELECT 1,2 INTO s."from",s.b`,
	} {
		t.Run(query, func(t *testing.T) {
			source := `DECLARE x integer; BEGIN ` + query + `; RETURN 7; END`
			raw, err := ParsePLpgSQL(source, PLReturnPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			capture := raw.Statements[0].Into
			if capture == nil || capture.Query == nil || len(capture.Targets) == 0 {
				t.Fatal("missing capture payload")
			}
			if selectQuery, ok := capture.Query.(*Select); ok && selectQuery.Into != nil {
				t.Fatal("procedural INTO became SQL table creation")
			}
			if capture.Start.Character != utf8.RuneCountInString(source[:capture.Start.Offset])+1 || source[capture.Delimiter.Offset] != ';' {
				t.Fatal("lost query positions")
			}
			if len(capture.Text) != len(query) || strings.Contains(capture.Text, "INTO STRICT") {
				t.Fatal("lost blanked original query", capture.Text)
			}
		})
	}
	t.Run("redacted_query_spelling", func(t *testing.T) {
		for _, tc := range []struct {
			query, want string
		}{
			{"SELECT /*é*/\n 1 INTO\n x\n FROM q", "SELECT /*é*/\n 1 " + strings.Repeat(" ", 9) + "FROM q"},
			{"SELECT /*é*/\r\n 1 INTO\r\n x\r\n FROM q", "SELECT /*é*/\r\n 1 " + strings.Repeat(" ", 11) + "FROM q"},
		} {
			source := "DECLARE x integer; BEGIN " + tc.query + "; END"
			block, err := ParsePLpgSQL(source, PLReturnPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			capture := block.Statements[0].Into
			if capture == nil || capture.Text != tc.want || len(capture.Text) != len(tc.query) {
				t.Fatalf("redacted execution text = %#v, want %q", capture, tc.want)
			}
			if source[capture.IntoStart.Offset:capture.TargetsEnd.Offset] != tc.query[strings.Index(tc.query, "INTO"):strings.Index(tc.query, "FROM")] || capture.TargetsEnd.Character != utf8.RuneCountInString(source[:capture.TargetsEnd.Offset])+1 {
				t.Fatal("redaction changed original source coordinates")
			}
		}
	})
	for _, query := range []string{`SELECT 1 INTO`, `SELECT 1 INTO STRICT`, `SELECT 1 INTO x,`, `SELECT 1 INTO x INTO x`, `SELECT 1 INTO x FETCH FIRST ROW ONLY WHERE false`, `SELECT 1 INTO x[`, `SELECT 1 INTO x; nonsense`} {
		if _, err := ParsePLpgSQL(`DECLARE x integer; BEGIN RETURN 7; `+query+`; END`, PLReturnPolicy{}); err == nil {
			t.Fatal("accepted malformed unreachable capture", query)
		}
	}
	t.Run("structural_boundaries", func(t *testing.T) {
		for _, tc := range []struct {
			query, targets, tail    string
			strict                  bool
			bounds, locking, window bool
		}{
			{`SELECT 1 INTO STRICT x FETCH FIRST ROW ONLY`, `x `, `FETCH`, true, true, false, false},
			{`SELECT id FROM q INTO x FOR UPDATE`, `x `, `FOR`, false, false, true, false},
			{`SELECT row_number() OVER w INTO x WINDOW w AS ()`, `x `, `WINDOW`, false, false, false, true},
			{`SELECT INTO s."from",s."where" 9,10`, `s."from",s."where" `, `9,10`, false, false, false, false},
			{`SELECT 1,2 INTO s."from",s."where" FROM q`, `s."from",s."where" `, `FROM`, false, false, false, false},
			{`SELECT 1,2 INTO s."from",s."where" FETCH FIRST ROW ONLY`, `s."from",s."where" `, `FETCH`, false, true, false, false},
			{`SELECT 1 INTO x UNION ALL SELECT 2`, `x `, `UNION`, false, false, false, false},
		} {
			t.Run(tc.query, func(t *testing.T) {
				source := `DECLARE x integer; BEGIN /* é中 */ ` + tc.query + `; END`
				block, err := ParsePLpgSQL(source, PLReturnPolicy{})
				if err != nil {
					t.Fatal(err)
				}
				capture := block.Statements[0].Into
				query, ok := capture.Query.(*Select)
				if !ok || query.Into != nil || capture.Strict != tc.strict {
					t.Fatalf("unexpected capture query/STRICT: %#v", capture)
				}
				if (query.Bounds != nil) != tc.bounds || (len(query.Locking) != 0) != tc.locking || (len(query.Windows) != 0) != tc.window {
					t.Fatalf("SQL tail lost its ordinary AST: %#v", query)
				}
				if source[capture.TargetsStart.Offset:capture.TargetsEnd.Offset] != tc.targets || !strings.HasPrefix(source[capture.TargetsEnd.Offset:], tc.tail) {
					t.Fatalf("incorrect target interval: %#v", capture)
				}
				if capture.TargetsEnd.Character != utf8.RuneCountInString(source[:capture.TargetsEnd.Offset])+1 {
					t.Fatal("lost UTF8 boundary position")
				}
				if strings.Contains(tc.targets, ",") {
					if len(capture.Targets) != 2 || len(capture.Separators) != 1 || source[capture.Separators[0].Offset] != ',' {
						t.Fatal("lost target list shape/commas")
					}
					for i, name := range []string{"from", "where"} {
						field, ok := capture.Targets[i].(FieldExpr)
						if !ok || field.Field.Text != name || source[field.Field.Pos.Offset] != '"' {
							t.Fatalf("lost quoted field identity/position: %#v", capture.Targets[i])
						}
					}
				} else if len(capture.Targets) != 1 || len(capture.Separators) != 0 {
					t.Fatal("SQL tail became a destination")
				}
			})
		}
	})
	t.Run("continuation_diagnostics", func(t *testing.T) {
		for _, tc := range []struct{ query, token, message string }{
			{`SELECT 1 INTO r.from`, `.`, `syntax error at or near "."`},
			{`SELECT INTO r.from 1`, `.`, `syntax error at or near "."`},
			{`SELECT 1 INTO r.`, `.`, `syntax error at or near "."`},
			{`SELECT 1 INTO a[1]`, `[`, `syntax error at or near "["`},
			{`SELECT INTO a[(SELECT 1)],x 1,2`, `[`, `syntax error at or near "["`},
			{`SELECT 1 INTO r.order[1+]`, `[`, `syntax error at or near "["`},
			{`SELECT 1 INTO x INTO x`, `INTO x;`, `INTO specified more than once at or near "INTO"`},
			{`SELECT 1 INTO x FETCH FIRST ROW ONLY WHERE false`, `WHERE`, `syntax error at or near "WHERE"`},
		} {
			source := `DECLARE x integer; BEGIN /* é中 */ ` + tc.query + `; END`
			_, err := ParsePLpgSQL(source, PLReturnPolicy{})
			var diagnostic *sqlstate.Error
			at := strings.Index(source, tc.token)
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != tc.message || diagnostic.Position != strconv.Itoa(utf8.RuneCountInString(source[:at])+1) {
				t.Fatalf("%s: diagnostic = %#v, want %s at byte %d", tc.query, diagnostic, tc.message, at)
			}
		}
	})
	t.Run("assignment_fetch_subscripts", func(t *testing.T) {
		block, err := ParsePLpgSQL(`DECLARE a integer[]; c refcursor; BEGIN a[1] := 2; FETCH c INTO a[2]; END`, PLReturnPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := block.Statements[0].Target.(SubscriptExpr); !ok {
			t.Fatal("INTO restriction changed assignment target grammar")
		}
		if _, ok := block.Statements[1].Cursor.Targets[0].(SubscriptExpr); !ok {
			t.Fatal("static INTO restriction changed FETCH target grammar")
		}
	})
	t.Run("trailing_statement_origin", func(t *testing.T) {
		for _, head := range []string{`nonsense`, `"nonsense"`, `"é中"`} {
			source := `DECLARE x integer; BEGIN RETURN; SELECT 1 INTO x; /* é中 */ ` + head + `; END`
			_, err := ParsePLpgSQL(source, PLReturnPolicy{})
			var diagnostic *sqlstate.Error
			at := strings.Index(source, head)
			wantPosition := strconv.Itoa(utf8.RuneCountInString(source[:at]) + 1)
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != `syntax error at or near "`+head+`"` || diagnostic.Position != wantPosition {
				t.Fatalf("bare statement %s diagnostic = %v, want original token at %s", head, err, wantPosition)
			}
		}
	})
	t.Run("following_assignment", func(t *testing.T) {
		block, err := ParsePLpgSQL(`DECLARE x integer; BEGIN SELECT 1 INTO x; x := 2; x = 3; RETURN x; END`, PLReturnPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		if len(block.Statements) != 4 || block.Statements[1].Target == nil || block.Statements[2].Target == nil {
			t.Fatal("bare-target rejection consumed a valid assignment")
		}
	})
	statements, err := Parse(`SELECT 1 INTO captured_table`)
	if err != nil {
		t.Fatal(err)
	}
	control, ok := statements[0].(*CreateQueryTable)
	if !ok || control.Form != SelectInto || len(control.Name.Parts) != 1 || control.Name.Parts[0].Text != "captured_table" {
		t.Fatal("ordinary SQL lost table INTO")
	}
	mixed, err := ParsePLpgSQL(`DECLARE r record; x integer; c CURSOR FOR SELECT 1; BEGIN FOR r IN SELECT 1 LOOP SELECT 2 INTO x; END LOOP; OPEN c; CLOSE c; RETURN x; END`, PLReturnPolicy{})
	if err != nil || mixed.Statements[0].QueryLoop == nil || mixed.Statements[0].QueryLoop.Body.Statements[0].Into == nil {
		t.Fatal("capture stole cursor/query-FOR SQL lookahead", err)
	}
}
