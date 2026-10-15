package parser

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestArrayRelationalFromNormalization(t *testing.T) {
	for _, prefix := range []string{"", "/*é*/ "} {
		q := prefix + "SELECT * FROM unnest(ARRAY[1],ARRAY['a']) WITH ORDINALITY AS u(x,y,n)"
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		alias := statements[0].(*Select).From.(*AliasReference)
		raw := alias.Input.(*FunctionTable)
		first, second := NormalizedFunctionCalls(raw), NormalizedFunctionCalls(raw)
		if len(raw.Calls) != 1 || len(raw.Calls[0].Args) != 2 || len(first) != 2 || len(second) != 2 || len(alias.Columns) != 3 || !raw.Ordinality {
			t.Fatal("normalization mutated retained AST")
		}
		for i, call := range first {
			if len(call.Name.Parts) != 2 || call.Name.Parts[0].Text != "pg_catalog" || call.Name.Parts[1].Text != "unnest" || call.Pos != raw.Calls[0].Pos || !reflect.DeepEqual(call.Args[0], raw.Calls[0].Args[i]) || !reflect.DeepEqual(second[i].Args[0], call.Args[0]) {
				t.Fatal("lost rewrite identity/lexical argument ownership")
			}
		}
		for _, q := range []string{
			prefix + "SELECT * FROM pg_catalog.unnest(ARRAY[1],ARRAY['a'])",
			prefix + "SELECT * FROM unnest(ARRAY[1],VARIADIC ARRAY['a'])",
			prefix + "SELECT * FROM unnest(DISTINCT ARRAY[1],ARRAY['a'])",
			prefix + "SELECT * FROM unnest(ARRAY[1],ARRAY['a'] ORDER BY 1)",
		} {
			statements, err := Parse(q)
			if err != nil {
				t.Fatal(err)
			}
			raw := statements[0].(*Select).From.(*FunctionTable)
			normalized := NormalizedFunctionCalls(raw)
			if len(normalized) != 1 || &normalized[0] != &raw.Calls[0] {
				t.Fatal("qualified/decorated call rewritten or copied")
			}
		}
		q = prefix + "SELECT * FROM ROWS FROM (unnest(ARRAY[1],ARRAY['a']) AS (x INT,y TEXT))"
		statements, err = Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		raw = statements[0].(*Select).From.(*FunctionTable)
		call := raw.Calls[0]
		if len(call.ColumnDefinitions) != 2 || call.ColumnDefinitions[0].Name.Text != "x" || call.ColumnDefinitions[1].Name.Text != "y" || len(NormalizedFunctionCalls(raw)) != 1 {
			t.Fatal("per-function definition did not block rewrite")
		}
		at := strings.Index(q, "x INT")
		if call.ColumnDefinitions[0].Name.Pos.Offset != at || call.ColumnDefinitions[0].Name.Pos.Character != utf8.RuneCountInString(q[:at])+1 {
			t.Fatal("definition name location")
		}
		at = strings.Index(q, "INT")
		if call.ColumnDefinitions[0].Type.Pos.Offset != at || call.ColumnDefinitions[0].Type.Pos.Character != utf8.RuneCountInString(q[:at])+1 {
			t.Fatal("definition type location")
		}
	}
}

func TestArrayRelationalParserDiagnostics(t *testing.T) {
	for _, prefix := range []string{"", "/*é*/ "} {
		for _, tc := range []struct {
			query, token string
			position     int
		}{
			{"SELECT * FROM unnest(ARRAY[1],ARRAY['a']) FILTER (WHERE TRUE)", "WHERE", 51},
			{"SELECT * FROM unnest(ARRAY[1],ARRAY['a']) OVER ()", ")", 49},
		} {
			_, err := Parse(prefix + tc.query)
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "42601" || d.Message != `syntax error at or near "`+tc.token+`"` || d.Position != strconv.Itoa(tc.position+utf8.RuneCountInString(prefix)) {
				t.Fatal(prefix+tc.query, err)
			}
		}
	}
	for _, prefix := range []string{"", "/*é*/ "} {
		q := prefix + "SELECT * FROM ROWS FROM (unnest(ARRAY[1]) AS (x INT,))"
		_, err := Parse(q)
		var d *sqlstate.Error
		at := strings.Index(q, ",)") + 1
		if !errors.As(err, &d) || d.Code != "42601" || d.Message != `syntax error at or near ")"` || d.Position != strconv.Itoa(utf8.RuneCountInString(q[:at])+1) {
			t.Fatal("malformed per-function definition list", err)
		}
	}
	for _, q := range []string{
		"SELECT array_sample(ARRAY[1],1) FILTER (WHERE TRUE)",
		"SELECT array_shuffle(ARRAY[1]) OVER ()",
	} {
		if _, err := Parse(q); err != nil {
			t.Fatal("FROM restriction leaked into scalar syntax", err)
		}
	}
}
