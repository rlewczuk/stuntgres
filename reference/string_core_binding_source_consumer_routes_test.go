package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceStringCoreParametersSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			for _, tc := range []struct {
				q    string
				args []any
				want any
			}{
				{"SELECT $1||$2 AS v", []any{"a ", "b "}, "a b "},
				{"SELECT $1||42 AS v", []any{"Value: "}, "Value: 42"},
				{"SELECT 42||$1 AS v", []any{"x"}, "42x"},
				{"SELECT $1::char(3)||$2::varchar AS v", []any{"a ", "b "}, "ab "},
				{"SELECT btrim($1,$2) AS v", []any{"é🙂X🙂é", "é🙂"}, "X"},
				{`SELECT "normalize"($1,$2) AS v`, []any{"é", "NFC"}, "é"},
			} {
				rows, err := db.Query(tc.q, tc.args...)
				stringCoreRetainedRows(t, rows, err, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, false)
				stmt, err := db.Prepare(tc.q)
				if err != nil {
					t.Fatal(err)
				}
				rows, err = stmt.Query(tc.args...)
				stmt.Close()
				stringCoreRetainedRows(t, rows, err, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, false)
			}
			stmt, err := db.Prepare("SELECT $1||$2 AS v")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, args := range [][]any{{"a", "b"}, {nil, "b"}, {"c", "d"}} {
				var want any
				if args[0] != nil {
					want = args[0].(string) + args[1].(string)
				}
				rows, e := stmt.Query(args...)
				stringCoreRetainedRows(t, rows, e, []string{"v"}, []string{"TEXT"}, [][]any{{want}}, false)
			}
		})
	}
}

func TestReferenceStringCoreArgumentLimitSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			for _, name := range []string{"btrim", "ltrim", "rtrim", "is_normalized", `"normalize"`} {
				q := "SELECT " + name + "(" + strings.Repeat("'x',", 100) + "'x')"
				want := referenceDiagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}
				result, err := backend.db.Exec(q)
				if result != nil {
					t.Fatal("failed analysis exposed result")
				}
				stringCoreControlDiagnostic(t, err, want)
				stmt, err := backend.db.Prepare(q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("failed analysis exposed statement")
				}
				stringCoreControlDiagnostic(t, err, want)
			}
			result, err := backend.db.Exec("SELECT 'x'::text||setseed(2)")
			if result != nil {
				t.Fatal("failed seed exposed result")
			}
			stringCoreControlDiagnostic(t, err, referenceDiagnostic{Code: "22023", Message: "setseed parameter 2 is out of allowed range [-1,1]"})
		})
	}
}
