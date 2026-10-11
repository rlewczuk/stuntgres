package reference_test

import (
	"bytes"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCheckCoreAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.CheckRun(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT only_one CHECK(a=1) NO INHERIT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES($1)", 1, 1))
					run(fixture.ImplicitError("INSERT INTO t VALUES($1)", fixture.CheckViolation("t", "only_one", "2"), 2))
					run(fixture.Exec("INSERT INTO t VALUES($1)", 1, nil))
					run(fixture.Exec("INSERT INTO t VALUES($1)", 1, 1))
					expected := fixture.Select("SELECT a FROM t ORDER BY a NULLS FIRST", []string{"a"}, []string{"INT4"}, [][]any{{nil}, {int64(1)}, {int64(1)}})
					run(expected)
					run(fixture.ImplicitError("INSERT INTO t VALUES(1),(2),(1)", fixture.CheckViolation("t", "only_one", "2")))
					run(expected)
					run(fixture.Exec("CREATE TABLE src(a INT)", 0))
					run(fixture.Exec("INSERT INTO src VALUES(1),(2),(1)", 3))
					run(fixture.ImplicitError("INSERT INTO t SELECT a FROM src", fixture.CheckViolation("t", "only_one", "2")))
					run(expected)
					run(fixture.ImplicitError("UPDATE t SET a=$1", fixture.CheckViolation("t", "only_one", "2"), 2))
					run(expected)
					run(fixture.Exec("CREATE TABLE u(a INT CONSTRAINT c CHECK(a<3))", 0))
					run(fixture.Exec("INSERT INTO u VALUES(1),(2)", 2))
					run(fixture.ImplicitError("UPDATE u SET a=a+1", fixture.CheckViolation("u", "c", "3")))
					run(fixture.Select("SELECT a FROM u ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					run(fixture.Exec("CREATE TABLE complete(CONSTRAINT c CHECK(public.complete.a<complete.b),a INT DEFAULT 1,b INT)", 0))
					run(fixture.Exec("INSERT INTO complete(b) VALUES(2)", 1))
					run(fixture.ImplicitError("UPDATE complete SET a=b,b=a", fixture.CheckViolation("complete", "c", "2, 1")))
					run(fixture.Select("SELECT * FROM complete", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
				})
			}
		})
	}
}
func TestReferenceCheckDefinitionDiagnostics(t *testing.T) {
	for _, tc := range []struct{ q, code, message, position string }{
		{"CREATE TABLE t(a INT CHECK(a))", "42804", "argument of CHECK must be type boolean, not type integer", "28"},
		{"CREATE TABLE t(a TEXT CHECK(a))", "42804", "argument of CHECK must be type boolean, not type text", "29"},
		{"CREATE TABLE t(a INT CHECK(missing=1))", "42703", "column \"missing\" does not exist", "28"},
		{"CREATE TABLE t(a INT CHECK(other.a=1))", "42P01", "missing FROM-clause entry for table \"other\"", "28"},
		{"CREATE TABLE t(a INT CHECK(ctid IS NULL))", "42P10", "system column \"ctid\" reference in check constraint is invalid", "28"},
		{"CREATE TABLE t(a INT CHECK(count(*)>0))", "42803", "aggregate functions are not allowed in check constraints", "28"},
		{"CREATE TABLE t(a INT CHECK($1=1))", "42P02", "there is no parameter $1", "28"},
		{"CREATE TABLE t(a INT CHECK(FALSE AND $1=1))", "42P02", "there is no parameter $1", "38"},
		{"CREATE TABLE t(a INT CONSTRAINT c CHECK(TRUE),CONSTRAINT c CHECK(TRUE))", "42710", "check constraint \"c\" already exists", ""},
		{"CREATE TABLE t(a INT CHECK(TRUE),CONSTRAINT t_check CHECK(TRUE))", "42710", "check constraint \"t_check\" already exists", ""},
	} {
		t.Run(tc.q, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(fmt.Sprintf("%s/%t", b.Name, prepared), func(t *testing.T) {
						run := func(s fixture.Step) { s.Prepared = prepared; fixture.CheckRun(t, p.Ctx, b.DB, b.Recorder, s) }
						run(fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position}))
						fixture.CheckRun(t, p.Ctx, b.DB, b.Recorder, fixture.ImplicitError("SELECT * FROM t", fixture.Diagnostic{Code: "42P01", Message: `relation "t" does not exist`, Position: "15"}))
						run(fixture.Exec("CREATE TABLE t(a INT)", 0))
					})
				}
			}
		})
	}
}
func TestReferenceCheckScalarFamilies(t *testing.T) {
	for _, tc := range []struct{ column, expr, good, bad string }{
		{"a INT", "a=1", "1", "2"},
		{"a BOOL", "a", "TRUE", "FALSE"},
		{"a INT", "a IS NOT NULL", "1", "NULL"},
		{"a INT", "(a=1 OR a IS NULL) AND NOT(a=2)", "1", "2"},
		{"a INT", "a IS DISTINCT FROM 2", "1", "2"},
		{"a INT", "a IS NOT DISTINCT FROM 1", "1", "2"},
		{"a INT", "a BETWEEN 1 AND 3", "3", "4"},
		{"a INT", "CASE WHEN a IS NULL THEN TRUE ELSE a=1 END", "1", "2"},
		{"a INT", "COALESCE(a,1)=1", "NULL", "2"},
		{"a INT", "NULLIF(a,1) IS NULL", "1", "2"},
		{"a INT", "CAST(a AS BIGINT)+1=2", "1", "2"},
		{"a INT", "abs(a)=1", "-1", "2"},
		{"a INT", "num_nonnulls(a)=1", "1", "NULL"},
		{"a INT", "num_nulls(a)=0", "1", "NULL"},
		{"a TEXT", "current_schemas(false) IS NOT NULL AND a='yes'", "'yes'", "'no'"},
		{"a INT", "current_schemas(false) IS NOT NULL AND a=1", "1", "2"},
		{"a DATE", "a=DATE '2024-01-02'", "'2024-01-02'", "'2024-01-03'"},
		{"a INT", "(a & 1)=1", "1", "2"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t("+tc.column+",CONSTRAINT c CHECK("+tc.expr+"))", 0))
					run(fixture.Exec("INSERT INTO t VALUES("+tc.good+")", 1))
					if !b.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
					}
					detail := tc.bad
					if detail == "NULL" {
						detail = "null"
					} else if detail == "FALSE" {
						detail = "f"
					} else if len(detail) > 1 && detail[0] == '\'' {
						detail = detail[1 : len(detail)-1]
					}
					run(fixture.ImplicitError("INSERT INTO t VALUES("+tc.bad+")", fixture.CheckViolation("t", "c", detail)))
					run(fixture.Exec("DROP TABLE t", 0))
				})
			}
		})
	}
}
func TestReferenceCheckLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(prefix TEXT,a INT CHECK(a=1),b INT CHECK(b>0))", 0))
			run(fixture.Exec("INSERT INTO t VALUES('old',1,2)", 1))
			run(fixture.Exec("CREATE TABLE clone(LIKE t)", 0))
			run(fixture.Exec("INSERT INTO clone VALUES('copy',2,0)", 1))
			run(fixture.Exec("ALTER TABLE t RENAME TO u", 0))
			run(fixture.Exec("ALTER TABLE u RENAME a TO renamed", 0))
			run(fixture.Exec("ALTER TABLE u DROP prefix", 0))
			run(fixture.ImplicitError("INSERT INTO u VALUES(2,2)", fixture.CheckViolation("u", "t_a_check", "2, 2")))
			run(fixture.Exec("ALTER TABLE u DROP renamed", 0))
			run(fixture.ImplicitError("INSERT INTO u VALUES(0)", fixture.CheckViolation("u", "t_b_check", "0")))
			run(fixture.Exec("ALTER TABLE u DROP CONSTRAINT t_b_check", 0))
			run(fixture.Exec("INSERT INTO u VALUES(0)", 1))
		})
	}
}
