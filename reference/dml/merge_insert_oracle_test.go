package reference_test

import (
	"context"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeInsertOracleQuestions(t *testing.T) {
	t.Run("O1", func(t *testing.T) {
		for _, populated := range []bool{false, true} {
			for _, source := range []string{"(VALUES(1)) s(i)", "(SELECT 1 AS i WHERE FALSE) s"} {
				for _, on := range []string{"1", "0", "'oops'", "NULL", "$1::integer"} {
					t.Run(map[bool]string{false: "emptyTarget/", true: "populatedTarget/"}[populated]+source+"/"+on, func(t *testing.T) {
						mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
							run := fixture.MergeRun(t, p, b, prepared)
							run(fixture.Exec("CREATE TABLE t(i INT)", 0))
							if populated {
								run(fixture.Exec("INSERT INTO t VALUES(99)", 1))
							}
							var args []any
							if on == "$1::integer" {
								args = []any{1}
							}
							inserted := source == "(VALUES(1)) s(i)" && (!populated || on == "0" || on == "NULL")
							var count int64
							want := [][]any{}
							if inserted {
								count = 1
								want = append(want, []any{int64(1)})
							}
							if populated {
								want = append(want, []any{int64(99)})
							}
							run(fixture.Exec("MERGE INTO t USING "+source+" ON "+on+" WHEN NOT MATCHED THEN INSERT VALUES(s.i)", count, args...))
							run(fixture.Select("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT4"}, want))
						})
					})
				}
			}
		}
	})
	t.Run("O2", func(t *testing.T) {
		for _, tc := range []struct {
			name, expr string
			rows       [][]any
		}{
			{"scalar", "(SELECT 7)", [][]any{{int64(7)}}},
			{"zero", "(SELECT 7 WHERE FALSE)", [][]any{{nil}}},
			{"many", "(SELECT x FROM (VALUES(7),(8)) v(x))", nil},
			{"correlated", "(SELECT s.i+6)", [][]any{{int64(7)}}},
			{"hiddenTarget", "(SELECT t.i)", nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
					run := fixture.MergeRun(t, p, b, prepared)
					run(fixture.Exec("CREATE TABLE t(i INT)", 0))
					q := "MERGE INTO t USING (VALUES(1)) s(i) ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(" + tc.expr + ")"
					if tc.rows == nil {
						d := fixture.Diagnostic{Code: "21000", Message: "more than one row returned by a subquery used as an expression"}
						stage := "execute"
						if tc.name == "hiddenTarget" {
							d = fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Detail: `There is an entry for table "t", but it cannot be referenced from this part of the query.`, Position: "90"}
							if prepared {
								stage = "prepare"
							}
						}
						s := fixture.ImplicitError(q, d)
						s.ErrorStage = stage
						run(s)
						run(fixture.Select("SELECT * FROM t", []string{"i"}, []string{"INT4"}, [][]any{}))
						return
					}
					run(fixture.Exec(q, 1))
					run(fixture.Select("SELECT * FROM t", []string{"i"}, []string{"INT4"}, tc.rows))
				})
			})
		}
	})
	t.Run("O3", func(t *testing.T) {
		for _, tc := range []struct {
			q, system        string
			hidden, positive bool
		}{
			{"MERGE INTO t USING s ON FALSE WHEN NOT MATCHED AND s.xmin IS NULL THEN INSERT VALUES('1')", "xmin", false, false},
			{"MERGE INTO t USING s ON FALSE WHEN NOT MATCHED AND s.ctid IS NULL THEN INSERT VALUES('1')", "ctid", false, false},
			{"MERGE INTO t USING s ON FALSE WHEN NOT MATCHED AND s.tableoid IS NOT NULL THEN INSERT VALUES('1')", "tableoid", false, true},
			{"MERGE INTO t USING s ON s.xmin IS NOT NULL WHEN NOT MATCHED THEN INSERT VALUES('1')", "xmin", false, true},
			{"MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.xmin::text)", "xmin", false, true},
			{"MERGE INTO t USING s ON FALSE WHEN NOT MATCHED AND t.tableoid IS NULL THEN INSERT VALUES('1')", "tableoid", true, false},
			{"MERGE INTO t USING s ON FALSE WHEN NOT MATCHED AND t.ctid IS NULL THEN INSERT VALUES('1')", "ctid", true, false},
			{"MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(t.tableoid::text)", "tableoid", true, false},
			{"MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(t.ctid::text)", "ctid", true, false},
		} {
			t.Run(tc.q, func(t *testing.T) {
				mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
					run := fixture.MergeRun(t, p, b, prepared)
					run(fixture.Exec("CREATE TABLE t(i TEXT)", 0))
					run(fixture.Exec("CREATE TABLE s(i INT)", 0))
					run(fixture.Exec("INSERT INTO s VALUES(1)", 1))
					if tc.positive && b.PG {
						run(fixture.Exec(tc.q, 1))
						if strings.Contains(tc.q, "VALUES(s.xmin") {
							// Compare to the same physical source identity, never a server's
							// allocator-dependent XID or a learned-native golden.
							run(fixture.Select("SELECT t.i=s.xmin::text AS same FROM t CROSS JOIN s", []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
						} else {
							run(fixture.Select("SELECT * FROM t", []string{"i"}, []string{"TEXT"}, [][]any{{"1"}}))
						}
						return
					}
					qualifier := "s."
					if tc.hidden {
						qualifier = "t."
					}
					d := fixture.Diagnostic{Code: "42P10", Message: `cannot use system column "` + tc.system + `" in MERGE WHEN condition`}
					if tc.hidden {
						d = fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Detail: `There is an entry for table "t", but it cannot be referenced from this part of the query.`}
					}
					if tc.positive {
						d = fixture.Diagnostic{Code: "42703", Message: "column s." + tc.system + " does not exist"}
					}
					d.Position = fixture.Position(strings.Index(tc.q, qualifier+tc.system) + 1)
					s := fixture.ImplicitError(tc.q, d)
					s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
					run(s)
					run(fixture.Select("SELECT * FROM t", []string{"i"}, []string{"TEXT"}, [][]any{}))
				})
			})
		}
	})
	t.Run("O4", mergeInsertColumnGrants)
}

// Public column GRANT exercises the same authority manifests in both backends.
func mergeInsertColumnGrants(t *testing.T) {
	const role = "merge_oracle_writer"
	fixture.RoleReferenceCleanup(t, []string{role})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		run := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
		run("CREATE ROLE " + role)
		run("CREATE SEQUENCE merge_defaults")
		run("CREATE TABLE t(id INT DEFAULT nextval('merge_defaults'),v INT)")
		run("CREATE TABLE s(id INT,v INT)")
		if b.PG {
			run("GRANT USAGE ON SCHEMA public TO " + role)
		}
		run("GRANT INSERT(id) ON t TO " + role)
		run("GRANT SELECT(id) ON s TO " + role)
		queries := []string{
			"MERGE INTO t USING s ON FALSE WHEN NOT MATCHED AND FALSE THEN INSERT(id) VALUES(s.id) WHEN NOT MATCHED THEN INSERT(v) VALUES(1)",
			"MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT(id) VALUES(s.id)",
			"MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT(id) VALUES(1)",
			"MERGE INTO t USING s ON t.id=s.id WHEN NOT MATCHED THEN INSERT(id) VALUES(s.id)",
			"MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT DEFAULT VALUES",
		}
		check := func(q string, prepared bool, d *fixture.Diagnostic, stage string) {
			s := fixture.Exec(q, 0)
			if d != nil {
				s = fixture.ImplicitError(q, *d)
				s.ErrorStage = stage
			}
			s.Prepared = prepared
			fixture.InsertSelectRun(t, p.Ctx, c, b.Recorder, s)
		}
		for _, prepared := range []bool{false, true} {
			run("SET SESSION AUTHORIZATION " + role)
			for i, q := range queries {
				var d *fixture.Diagnostic
				if i == 0 || i == 3 {
					d = &fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"}
				}
				check(q, prepared, d, "execute")
			}
			run("RESET SESSION AUTHORIZATION")
		}
		run("GRANT INSERT(v),SELECT(id) ON t TO " + role)
		if b.PG {
			run("GRANT USAGE ON SEQUENCE merge_defaults TO " + role)
		} else {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ImplicitError(
				"GRANT USAGE ON SEQUENCE merge_defaults TO "+role,
				fixture.Diagnostic{Code: "0A000", Message: "statement is not supported"},
			))
		}
		for _, prepared := range []bool{false, true} {
			run("SET SESSION AUTHORIZATION " + role)
			for _, q := range queries {
				check(q, prepared, nil, "")
			}
			run("RESET SESSION AUTHORIZATION")
		}
		run("REVOKE SELECT ON s FROM " + role)
		for _, prepared := range []bool{false, true} {
			run("SET SESSION AUTHORIZATION " + role)
			for _, q := range queries[1:3] {
				check(q, prepared, &fixture.Diagnostic{Code: "42501", Message: "permission denied for table s"}, "execute")
			}
			run("RESET SESSION AUTHORIZATION")
		}
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				q       string
				d       fixture.Diagnostic
				runtime bool
			}{
				{"MERGE INTO absent USING missing ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(1)", fixture.Diagnostic{Code: "42P01", Message: `relation "absent" does not exist`, Position: "12"}, false},
				{"MERGE INTO t USING missing ON FALSE WHEN NOT MATCHED THEN INSERT(missing) VALUES(1)", fixture.Diagnostic{Code: "42P01", Message: `relation "missing" does not exist`, Position: "20"}, false},
				{"MERGE INTO t USING s ON FALSE WHEN NOT MATCHED AND FALSE THEN INSERT(missing) VALUES(1)", fixture.Diagnostic{Code: "42703", Message: `column "missing" of relation "t" does not exist`, Position: "70"}, false},
				{"MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT VALUES('oops')", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "67"}, false},
				{"MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(2147483648)", fixture.Diagnostic{Code: "22003", Message: "integer out of range"}, true},
				{"MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(1)", fixture.Diagnostic{Code: "25006", Message: "cannot execute MERGE in a read-only transaction"}, true},
			} {
				run("SET SESSION AUTHORIZATION " + role)
				run("BEGIN READ ONLY")
				stage := "execute"
				if prepared && !tc.runtime {
					stage = "prepare"
				}
				check(tc.q, prepared, &tc.d, stage)
				run("ROLLBACK")
				run("RESET SESSION AUTHORIZATION")
			}
		}
		fixture.InsertSelectRun(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{}))
	}
}
