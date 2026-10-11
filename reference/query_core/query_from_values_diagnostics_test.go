package reference_test

import (
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

// All logical fields, including absent fields and error stages, were captured
// independently from pinned PostgreSQL 18.0 before production changes.
func TestReferenceQueryFromValuesDiagnostics(t *testing.T) {
	cases := []struct {
		query string
		want  fixture.Diagnostic
	}{
		{"VALUES (1), (2,3)", fixture.Diagnostic{Code: "42601", Message: "VALUES lists must all be the same length", Position: "14"}},
		{"VALUES(1),(TRUE)", fixture.Diagnostic{Code: "42804", Message: "VALUES types integer and boolean cannot be matched", Position: "12"}},
		{"VALUES(DEFAULT)", fixture.Diagnostic{Code: "42601", Message: "DEFAULT is not allowed in this context", Position: "8"}},
		{"VALUES ()", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "9"}},
		{"VALUES", fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: "7"}},
		{"SELECT * FROM (VALUES(1)) v(a,b)", fixture.Diagnostic{Code: "42P10", Message: `table "v" has 1 columns available but 2 columns specified`}},
		{"VALUES(1),(2,DEFAULT)", fixture.Diagnostic{Code: "42601", Message: "DEFAULT is not allowed in this context", Position: "14"}},
		{"VALUES(1),(missing,2)", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "12"}},
		{"VALUES(1),('bad'::integer,2)", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "12"}},
		{"VALUES(1),(2+3,4)", fixture.Diagnostic{Code: "42601", Message: "VALUES lists must all be the same length", Position: "12"}},
		{"VALUES(1),(2::integer,4)", fixture.Diagnostic{Code: "42601", Message: "VALUES lists must all be the same length", Position: "12"}},
		{"VALUES(TIME '00:00'),(TIMESTAMP 'epoch')", fixture.Diagnostic{Code: "42846", Message: "VALUES could not convert type timestamp without time zone to time without time zone", Position: "33"}},
		{"VALUES(1::text),(2)", fixture.Diagnostic{Code: "42804", Message: "VALUES types text and integer cannot be matched", Position: "18"}},
		{"VALUES('bad'),(2)", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "8"}},
		{"VALUES(missing)", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "8"}},
		{"VALUES(t.n)", fixture.Diagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "t"`, Position: "8"}},
		{"VALUES(count(*))", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in VALUES", Position: "8"}},
		{"SELECT x FROM (VALUES(1,2)) v(x,x)", fixture.Diagnostic{Code: "42702", Message: `column reference "x" is ambiguous`, Position: "8"}},
		{"SELECT v.x FROM (VALUES(1,2)) v(x,x)", fixture.Diagnostic{Code: "42702", Message: `column reference "x" is ambiguous`, Position: "8"}},
		{"SELECT * FROM (VALUES(1)) v, (VALUES(2)) v", fixture.Diagnostic{Code: "42712", Message: `table name "v" specified more than once`}},
		{"SELECT unnamed_subquery.column1 FROM (VALUES(1))", fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "unnamed_subquery"`, Detail: `There is an entry for table "unnamed_subquery", but it cannot be referenced from this part of the query.`, Position: "8"}},
		{"SELECT v.column1 FROM (VALUES(1)) v(n)", fixture.Diagnostic{Code: "42703", Message: "column v.column1 does not exist", Position: "8"}},
		{"SELECT v.n FROM ((VALUES(1)) v(n)) j", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "34"}},
		{"VALUES(1) ORDER BY missing", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "20"}},
		{"VALUES(1) ORDER BY 0", fixture.Diagnostic{Code: "42P10", Message: "ORDER BY position 0 is not in select list", Position: "20"}},
		{"VALUES(1) ORDER BY 2", fixture.Diagnostic{Code: "42P10", Message: "ORDER BY position 2 is not in select list", Position: "20"}},
		{"VALUES(1) ORDER BY 'x'", fixture.Diagnostic{Code: "42601", Message: "non-integer constant in ORDER BY", Position: "20"}},
		{"SELECT * FROM (VALUES(1)", fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: "25"}},
		{"SELECT * FROM (VALUES(1)) v(a,)", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "31"}},
		{"VALUES(1) garbage", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "garbage"`, Position: "11"}},
		{"SELECT * FROM t, (VALUES(t.n)) v(n)", fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Detail: `There is an entry for table "t", but it cannot be referenced from this part of the query.`, Hint: "To reference that table, you must mark this subquery with LATERAL.", Position: "26"}},
		{"VALUES(1),(TRUE) LIMIT 0", fixture.Diagnostic{Code: "42804", Message: "VALUES types integer and boolean cannot be matched", Position: "12"}},
		{"SELECT * FROM empty CROSS JOIN (VALUES(1),(TRUE)) v(n)", fixture.Diagnostic{Code: "42804", Message: "VALUES types integer and boolean cannot be matched", Position: "44"}},
		{"INSERT INTO integer_destination SELECT column1 FROM (VALUES('1'))", fixture.Diagnostic{Code: "42804", Message: `column "n" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "40"}},
		{"INSERT INTO integer_destination SELECT column1 FROM (VALUES(NULL))", fixture.Diagnostic{Code: "42804", Message: `column "n" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "40"}},
		{"VALUES(1) FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "FOR UPDATE cannot be applied to VALUES"}},
		{"INSERT INTO t VALUES(9); VALUES (", fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: "34"}},
	}
	for _, tc := range cases[:11] {
		tc.query = "/*é🙂*/ " + tc.query
		if tc.want.Position != "" {
			n, err := strconv.Atoi(tc.want.Position)
			if err != nil {
				t.Fatal(err)
			}
			tc.want.Position = strconv.Itoa(n + 7)
		}
		cases = append(cases, tc)
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
				for _, s := range []fixture.Step{
					fixture.Exec("CREATE TABLE t(n INT)", 0), fixture.Exec("INSERT INTO t VALUES(1),(2)", 2),
					fixture.Exec("CREATE TABLE empty(n INT)", 0), fixture.Exec("CREATE TABLE integer_destination(n INT)", 0),
				} {
					run(s)
				}
				for _, prepared := range []bool{false, true} {
					for _, queryRows := range []bool{false, true} {
						s := fixture.ImplicitError(tc.query, tc.want)
						s.Prepared, s.SelectRows, s.ErrorStage = prepared, queryRows, "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						run(s)
						run(fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
						run(fixture.Select("SELECT n FROM integer_destination", []string{"n"}, []string{"INT4"}, [][]any{}))
					}
				}
			}
		})
	}
}

func TestReferenceQueryFromValuesLocking(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE t(n INT)", 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
		for _, prepared := range []bool{false, true} {
			for _, s := range []fixture.Step{
				fixture.Select("SELECT * FROM (VALUES(1)) v FOR UPDATE", []string{"column1"}, []string{"INT4"}, [][]any{{int64(1)}}),
				fixture.Exec("SELECT * FROM (VALUES(1)) v FOR UPDATE", 1),
				fixture.Select("SELECT * FROM t CROSS JOIN (VALUES(1)) v FOR UPDATE", []string{"n", "column1"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}}),
				fixture.Exec("SELECT * FROM t CROSS JOIN (VALUES(1)) v FOR UPDATE", 2),
			} {
				s.Prepared, s.Bag = prepared, true
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			}
		}
	}
}
