package reference_test

import (
	"strconv"
	"testing"
)

// All logical fields, including absent fields and error stages, were captured
// independently from pinned PostgreSQL 18.0 before production changes.
func TestReferenceQueryFromValuesDiagnostics(t *testing.T) {
	cases := []struct {
		query string
		want  referenceDiagnostic
	}{
		{"VALUES (1), (2,3)", referenceDiagnostic{Code: "42601", Message: "VALUES lists must all be the same length", Position: "14"}},
		{"VALUES(1),(TRUE)", referenceDiagnostic{Code: "42804", Message: "VALUES types integer and boolean cannot be matched", Position: "12"}},
		{"VALUES(DEFAULT)", referenceDiagnostic{Code: "42601", Message: "DEFAULT is not allowed in this context", Position: "8"}},
		{"VALUES ()", referenceDiagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "9"}},
		{"VALUES", referenceDiagnostic{Code: "42601", Message: "syntax error at end of input", Position: "7"}},
		{"SELECT * FROM (VALUES(1)) v(a,b)", referenceDiagnostic{Code: "42P10", Message: `table "v" has 1 columns available but 2 columns specified`}},
		{"VALUES(1),(2,DEFAULT)", referenceDiagnostic{Code: "42601", Message: "DEFAULT is not allowed in this context", Position: "14"}},
		{"VALUES(1),(missing,2)", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "12"}},
		{"VALUES(1),('bad'::integer,2)", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "12"}},
		{"VALUES(1),(2+3,4)", referenceDiagnostic{Code: "42601", Message: "VALUES lists must all be the same length", Position: "12"}},
		{"VALUES(1),(2::integer,4)", referenceDiagnostic{Code: "42601", Message: "VALUES lists must all be the same length", Position: "12"}},
		{"VALUES(TIME '00:00'),(TIMESTAMP 'epoch')", referenceDiagnostic{Code: "42846", Message: "VALUES could not convert type timestamp without time zone to time without time zone", Position: "33"}},
		{"VALUES(1::text),(2)", referenceDiagnostic{Code: "42804", Message: "VALUES types text and integer cannot be matched", Position: "18"}},
		{"VALUES('bad'),(2)", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "8"}},
		{"VALUES(missing)", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "8"}},
		{"VALUES(t.n)", referenceDiagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "t"`, Position: "8"}},
		{"VALUES(count(*))", referenceDiagnostic{Code: "42803", Message: "aggregate functions are not allowed in VALUES", Position: "8"}},
		{"SELECT x FROM (VALUES(1,2)) v(x,x)", referenceDiagnostic{Code: "42702", Message: `column reference "x" is ambiguous`, Position: "8"}},
		{"SELECT v.x FROM (VALUES(1,2)) v(x,x)", referenceDiagnostic{Code: "42702", Message: `column reference "x" is ambiguous`, Position: "8"}},
		{"SELECT * FROM (VALUES(1)) v, (VALUES(2)) v", referenceDiagnostic{Code: "42712", Message: `table name "v" specified more than once`}},
		{"SELECT unnamed_subquery.column1 FROM (VALUES(1))", referenceDiagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "unnamed_subquery"`, Detail: `There is an entry for table "unnamed_subquery", but it cannot be referenced from this part of the query.`, Position: "8"}},
		{"SELECT v.column1 FROM (VALUES(1)) v(n)", referenceDiagnostic{Code: "42703", Message: "column v.column1 does not exist", Position: "8"}},
		{"SELECT v.n FROM ((VALUES(1)) v(n)) j", referenceDiagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "34"}},
		{"VALUES(1) ORDER BY missing", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "20"}},
		{"VALUES(1) ORDER BY 0", referenceDiagnostic{Code: "42P10", Message: "ORDER BY position 0 is not in select list", Position: "20"}},
		{"VALUES(1) ORDER BY 2", referenceDiagnostic{Code: "42P10", Message: "ORDER BY position 2 is not in select list", Position: "20"}},
		{"VALUES(1) ORDER BY 'x'", referenceDiagnostic{Code: "42601", Message: "non-integer constant in ORDER BY", Position: "20"}},
		{"SELECT * FROM (VALUES(1)", referenceDiagnostic{Code: "42601", Message: "syntax error at end of input", Position: "25"}},
		{"SELECT * FROM (VALUES(1)) v(a,)", referenceDiagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "31"}},
		{"VALUES(1) garbage", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "garbage"`, Position: "11"}},
		{"SELECT * FROM t, (VALUES(t.n)) v(n)", referenceDiagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Detail: `There is an entry for table "t", but it cannot be referenced from this part of the query.`, Hint: "To reference that table, you must mark this subquery with LATERAL.", Position: "26"}},
		{"VALUES(1),(TRUE) LIMIT 0", referenceDiagnostic{Code: "42804", Message: "VALUES types integer and boolean cannot be matched", Position: "12"}},
		{"SELECT * FROM empty CROSS JOIN (VALUES(1),(TRUE)) v(n)", referenceDiagnostic{Code: "42804", Message: "VALUES types integer and boolean cannot be matched", Position: "44"}},
		{"INSERT INTO integer_destination SELECT column1 FROM (VALUES('1'))", referenceDiagnostic{Code: "42804", Message: `column "n" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "40"}},
		{"INSERT INTO integer_destination SELECT column1 FROM (VALUES(NULL))", referenceDiagnostic{Code: "42804", Message: `column "n" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "40"}},
		{"VALUES(1) FOR UPDATE", referenceDiagnostic{Code: "0A000", Message: "FOR UPDATE cannot be applied to VALUES"}},
		{"INSERT INTO t VALUES(9); VALUES (", referenceDiagnostic{Code: "42601", Message: "syntax error at end of input", Position: "34"}},
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
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
				for _, s := range []referenceStep{
					referenceExec("CREATE TABLE t(n INT)", 0), referenceExec("INSERT INTO t VALUES(1),(2)", 2),
					referenceExec("CREATE TABLE empty(n INT)", 0), referenceExec("CREATE TABLE integer_destination(n INT)", 0),
				} {
					run(s)
				}
				for _, prepared := range []bool{false, true} {
					for _, queryRows := range []bool{false, true} {
						s := referenceImplicitError(tc.query, tc.want)
						s.prepared, s.selectRows, s.errorStage = prepared, queryRows, "execute"
						if prepared {
							s.errorStage = "prepare"
						}
						run(s)
						run(referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
						run(referenceSelect("SELECT n FROM integer_destination", []string{"n"}, []string{"INT4"}, [][]any{}))
					}
				}
			}
		})
	}
}

func TestReferenceQueryFromValuesLocking(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("CREATE TABLE t(n INT)", 0))
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO t VALUES(1),(2)", 2))
		for _, prepared := range []bool{false, true} {
			for _, s := range []referenceStep{
				referenceSelect("SELECT * FROM (VALUES(1)) v FOR UPDATE", []string{"column1"}, []string{"INT4"}, [][]any{{int64(1)}}),
				referenceExec("SELECT * FROM (VALUES(1)) v FOR UPDATE", 1),
				referenceSelect("SELECT * FROM t CROSS JOIN (VALUES(1)) v FOR UPDATE", []string{"n", "column1"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}}),
				referenceExec("SELECT * FROM t CROSS JOIN (VALUES(1)) v FOR UPDATE", 2),
			} {
				s.prepared, s.bag = prepared, true
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
		}
	}
}
