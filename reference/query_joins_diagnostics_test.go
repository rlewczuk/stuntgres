//go:build reference

package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func TestReferenceQueryJoinsDiagnosticsOracle(t *testing.T) {
	queryJoinsDiagnostics(t, true)
}

func TestReferenceQueryJoinsDiagnostics(t *testing.T) {
	queryJoinsDiagnostics(t, false)
}

// Complete records were observed on the pinned PostgreSQL 18.0 UTF8/C oracle.
// Unspecified logical fields are asserted absent, including synthesized positions.
func queryJoinsDiagnostics(t *testing.T, oracle bool) {
	p := queryJoinsReferenceSetup(t)
	cleanupOther := func(db *sql.DB) {
		// Child subtests and their rows/statements finish before this cleanup;
		// the pair's connection/context cleanup was registered earlier.
		t.Cleanup(func() {
			for _, q := range []string{"DROP TABLE other.a", "DROP SCHEMA other"} {
				if _, err := db.ExecContext(p.ctx, q); err != nil {
					t.Errorf("join schema cleanup %s: %v", q, err)
				}
			}
		})
	}
	for _, q := range []string{
		"CREATE TABLE only_left(k INT)", "CREATE TABLE only_right(r INT)",
		"CREATE TABLE text_key(k TEXT)",
		"CREATE SCHEMA other", "CREATE TABLE other.a(k INT)",
		"CREATE TABLE char_a(k VARCHAR(3))", "CREATE TABLE char_b(k VARCHAR(5))", "CREATE TABLE char_c(k VARCHAR(3))",
		"INSERT INTO char_a VALUES('x')", "INSERT INTO char_b VALUES('x')", "INSERT INTO char_c VALUES('x')",
	} {
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec(q, func() int64 {
			if q == "INSERT INTO char_a VALUES('x')" || q == "INSERT INTO char_b VALUES('x')" || q == "INSERT INTO char_c VALUES('x')" {
				return 1
			}
			return 0
		}()))
		if q == "CREATE SCHEMA other" {
			cleanupOther(p.postgres)
		}
		if !oracle {
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec(q, func() int64 {
				if q == "INSERT INTO char_a VALUES('x')" || q == "INSERT INTO char_b VALUES('x')" || q == "INSERT INTO char_c VALUES('x')" {
					return 1
				}
				return 0
			}()))
			if q == "CREATE SCHEMA other" {
				cleanupOther(p.memory)
			}
		}
	}
	cases := []struct {
		query string
		want  *referenceDiagnostic
	}{
		{"SELECT k FROM a CROSS JOIN b", &referenceDiagnostic{Code: "42702", Message: `column reference "k" is ambiguous`, Position: "8"}},
		{"SELECT /*é*/ k FROM a CROSS JOIN b", &referenceDiagnostic{Code: "42702", Message: `column reference "k" is ambiguous`, Position: "14"}},
		{"SELECT a.no_such_column FROM a CROSS JOIN b", &referenceDiagnostic{Code: "42703", Message: `column a.no_such_column does not exist`, Position: "8"}},
		{"SELECT zz.k FROM a CROSS JOIN b", &referenceDiagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "zz"`, Position: "8"}},
		{"SELECT a.k FROM a AS x CROSS JOIN b", &referenceDiagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "a"`, Hint: `Perhaps you meant to reference the table alias "x".`, Position: "8"}},
		{"SELECT * FROM a AS x CROSS JOIN b AS x", &referenceDiagnostic{Code: "42712", Message: `table name "x" specified more than once`}},
		{"SELECT * FROM a CROSS JOIN a", &referenceDiagnostic{Code: "42712", Message: `table name "a" specified more than once`}},
		{"SELECT * FROM a JOIN b USING (k,k)", &referenceDiagnostic{Code: "42701", Message: `column name "k" appears more than once in USING clause`}},
		{"SELECT * FROM a JOIN b USING (absent)", &referenceDiagnostic{Code: "42703", Message: `column "absent" specified in USING clause does not exist in left table`}},
		{"SELECT * FROM only_left JOIN only_right USING(k)", &referenceDiagnostic{Code: "42703", Message: `column "k" specified in USING clause does not exist in right table`}},
		{"SELECT * FROM (a CROSS JOIN b) JOIN c USING(k)", &referenceDiagnostic{Code: "42702", Message: `common column name "k" appears more than once in left table`}},
		{"SELECT * FROM c JOIN (a CROSS JOIN b) USING(k)", &referenceDiagnostic{Code: "42702", Message: `common column name "k" appears more than once in right table`}},
		{"SELECT * FROM (a CROSS JOIN b) NATURAL JOIN c", &referenceDiagnostic{Code: "42702", Message: `common column name "k" appears more than once in left table`}},
		{"SELECT * FROM c NATURAL JOIN (a CROSS JOIN b)", &referenceDiagnostic{Code: "42702", Message: `common column name "k" appears more than once in right table`}},
		{"SELECT * FROM a, b JOIN c ON a.k=c.k", &referenceDiagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "a"`, Detail: `There is an entry for table "a", but it cannot be referenced from this part of the query.`, Position: "30"}},
		{"SELECT * FROM a JOIN b ON 1", &referenceDiagnostic{Code: "42804", Message: "argument of JOIN/ON must be type boolean, not type integer", Position: "27"}},
		{"SELECT * FROM a JOIN b ON", &referenceDiagnostic{Code: "42601", Message: "syntax error at end of input", Position: "26"}},
		{"SELECT * FROM a CROSS JOIN b ON TRUE", &referenceDiagnostic{Code: "42601", Message: `syntax error at or near "ON"`, Position: "30"}},
		{"SELECT * FROM a AS x(c1,c2,c3,c4)", &referenceDiagnostic{Code: "42P10", Message: `table "x" has 3 columns available but 4 columns specified`}},
		{"SELECT * FROM (a CROSS JOIN b) AS j(c1,c2,c3,c4,c5,c6,c7)", &referenceDiagnostic{Code: "42P10", Message: `join expression "j" has 6 columns available but 7 columns specified`}},
		{"SELECT * FROM a JOIN b ON count(*)>0", &referenceDiagnostic{Code: "42803", Message: "aggregate functions are not allowed in JOIN conditions", Position: "27"}},
		{"SELECT * FROM only_left JOIN text_key USING(k)", &referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer = text", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
		{"SELECT * FROM a CROSS JOIN missing", &referenceDiagnostic{Code: "42P01", Message: `relation "missing" does not exist`, Position: "28"}},
		{"SELECT * FROM a NATURAL JOIN b ON TRUE", &referenceDiagnostic{Code: "42601", Message: `syntax error at or near "ON"`, Position: "32"}},
		{"SELECT * FROM a NATURAL JOIN b USING(k)", &referenceDiagnostic{Code: "42601", Message: `syntax error at or near "USING"`, Position: "32"}},
		{"SELECT * FROM a JOIN b USING()", &referenceDiagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "30"}},
		{"SELECT * FROM a JOIN b", &referenceDiagnostic{Code: "42601", Message: "syntax error at end of input", Position: "23"}},
		{"SELECT * FROM a JOIN b ON TRUE ON TRUE", &referenceDiagnostic{Code: "42601", Message: `syntax error at or near "ON"`, Position: "32"}},
		{"SELECT u.l FROM a JOIN b USING(k) AS u", &referenceDiagnostic{Code: "42703", Message: "column u.l does not exist", Hint: `Perhaps you meant to reference the column "a.l".`, Position: "8"}},
		{"SELECT * FROM a JOIN b USING(k) AS u(z)", &referenceDiagnostic{Code: "42601", Message: `syntax error at or near "("`, Position: "37"}},
		{"SELECT * FROM a JOIN b USING(k) AS a", &referenceDiagnostic{Code: "42712", Message: `table name "a" specified more than once`}},
		{"SELECT * FROM (a JOIN b USING(k) AS u) CROSS JOIN c AS u", &referenceDiagnostic{Code: "42712", Message: `table name "u" specified more than once`}},
		{"SELECT a.k FROM (a JOIN b USING(k)) AS j", &referenceDiagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "a"`, Detail: `There is an entry for table "a", but it cannot be referenced from this part of the query.`, Position: "8"}},
		{"SELECT u.k FROM (a JOIN b USING(k) AS u) AS j", &referenceDiagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "u"`, Position: "8"}},
		{"SELECT x.k FROM a AS x(k,k)", &referenceDiagnostic{Code: "42702", Message: `column reference "k" is ambiguous`, Position: "8"}},
		{"SELECT a.k FROM public.a CROSS JOIN other.a", &referenceDiagnostic{Code: "42P09", Message: `table reference "a" is ambiguous`, Position: "8"}},
		{"SELECT public.a.no_such_column FROM public.a CROSS JOIN other.a", &referenceDiagnostic{Code: "42703", Message: "column a.no_such_column does not exist", Position: "8"}},
		{"SELECT public.a.k FROM public.a AS x CROSS JOIN b", &referenceDiagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "a"`, Hint: `Perhaps you meant to reference the table alias "x".`, Position: "8"}},
		{"SELECT /*é*/ zz.k FROM a CROSS JOIN b", &referenceDiagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "zz"`, Position: "14"}},
		{"SELECT k FROM a CROSS JOIN empty", &referenceDiagnostic{Code: "42702", Message: `column reference "k" is ambiguous`, Position: "8"}},
		{"SELECT * FROM a JOIN empty ON 1", &referenceDiagnostic{Code: "42804", Message: "argument of JOIN/ON must be type boolean, not type integer", Position: "31"}},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			if !oracle {
				step := referenceImplicitError(tc.query, *tc.want)
				step.selectRows, step.errorStage = true, "execute"
				p.step(t, step)
				step.errorStage = "prepare"
				p.step(t, referencePrepared(step))
				return
			}
			for _, prepared := range []bool{false, true} {
				name := "query"
				if prepared {
					name = "prepare"
				}
				t.Run(name, func(t *testing.T) {
					var err error
					if prepared {
						stmt, prepareErr := p.postgres.PrepareContext(p.ctx, tc.query)
						err = prepareErr
						if stmt != nil {
							stmt.Close()
							t.Fatal("diagnostic probe published a statement")
						}
					} else {
						rows, queryErr := p.postgres.QueryContext(p.ctx, tc.query)
						err = queryErr
						if rows != nil {
							rows.Close()
							t.Fatal("diagnostic probe published rows")
						}
					}
					if err == nil {
						t.Fatal("expected diagnostic, got success")
					}
					got := referenceExtractDiagnostic(t, err)
					t.Logf("JOIN ORACLE %s %q: %#v", name, tc.query, got)
					want := *tc.want
					want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
					if !reflect.DeepEqual(got, want) {
						t.Errorf("complete diagnostic got %#v want %#v", got, want)
					}
				})
			}
		})
	}
	t.Run("prepared_schema", func(t *testing.T) {
		t.Run("postgres", func(t *testing.T) { queryJoinsPreparedSchema(t, p, p.postgres) })
		if !oracle {
			t.Run("native", func(t *testing.T) { queryJoinsPreparedSchema(t, p, p.memory) })
		}
	})
	for _, q := range []string{"SELECT k,char_a.k,char_b.k FROM char_a JOIN char_b USING(k)", "SELECT k,char_a.k,char_c.k FROM char_a JOIN char_c USING(k)", "SELECT public.a.k,other.a.k FROM public.a CROSS JOIN other.a"} {
		t.Run(q, func(t *testing.T) {
			names, types, values := []string{"k", "k", "k"}, []string{"VARCHAR", "VARCHAR", "VARCHAR"}, [][]any{{"x", "x", "x"}}
			if q == "SELECT public.a.k,other.a.k FROM public.a CROSS JOIN other.a" {
				names, types, values = []string{"k", "k"}, []string{"INT4", "INT4"}, nil
			}
			s := referenceSelect(q, names, types, values)
			s.bag = true
			if !oracle {
				p.step(t, s)
				p.step(t, referencePrepared(s))
				return
			}
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referencePrepared(s))
		})
	}
}
