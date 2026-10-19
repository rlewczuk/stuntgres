package parser

import (
	"errors"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestJSONBPathOpsParser(t *testing.T) {
	for _, op := range []string{"@?", "@@"} {
		for _, prefix := range []string{"", "/*é*/ "} {
			query := prefix + `SELECT 'true'::jsonb OPERATOR("".` + op + `) '$'::jsonpath`
			_, err := Parse(query)
			var diagnostic *sqlstate.Error
			want := len([]rune(query[:strings.Index(query, `""`)])) + 1
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" ||
				diagnostic.Message != `zero-length delimited identifier at or near """"` ||
				diagnostic.Position != strconv.Itoa(want) {
				t.Fatalf("empty schema diagnostic=%v want position=%d", err, want)
			}
		}
		for _, qualified := range []bool{false, true} {
			spelling := op
			if qualified {
				spelling = "OPERATOR(pg_catalog." + op + ")"
			}
			query := `/*é*/ SELECT 'true'::jsonb ` + spelling + ` '$'::jsonpath`
			statements, err := Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatal(query, err)
			}
			raw, ok := statements[0].(*Select).Targets[0].(OperatorExpr)
			if !ok || len(raw.Args) != 2 || raw.Name.Parts[len(raw.Name.Parts)-1].Text != op {
				t.Fatal("operator shape", statements[0])
			}
			want := len([]rune(query[:strings.Index(query, spelling)])) + 1
			if raw.Pos.Character != want {
				t.Fatalf("position=%d want=%d", raw.Pos.Character, want)
			}
			if qualified && (len(raw.Name.Parts) != 2 || raw.Name.Parts[0].Text != "pg_catalog") {
				t.Fatal("lost qualification", raw.Name)
			}
			for _, suffix := range []string{"; garbage", " extra extra"} {
				if _, err := Parse(query + suffix); err == nil {
					t.Fatal("accepted trailing input", query+suffix)
				}
			}
		}
	}
}
