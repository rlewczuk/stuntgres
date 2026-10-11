package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBtreeIndexExcludedDefinitionsDiagnostics(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "context", true: "nonContext"}[nonContext], func(t *testing.T) {
			for _, tc := range []struct {
				name, query, message, position, index string
				execute                               bool
			}{
				{"storage", "CREATE INDEX i ON t(n) WITH(fillfactor=80)", "index storage parameters are unsupported", "24", "i", true},
				{"tablespace", "CREATE INDEX i ON t(n) TABLESPACE pg_default", "index tablespaces are unsupported", "24", "i", false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					p := fixture.NewScenario(t)
					referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *fixture.Recorder) {
						run := func(s fixture.Step) {
							s.NonContext = nonContext
							fixture.RunStep(t, p.Ctx, db, recorder, s)
						}
						for _, step := range []fixture.Step{fixture.Exec("CREATE TABLE t(n INT,c INT,s TEXT)", 0), fixture.Exec("INSERT INTO t VALUES(2,1,'one')", 1)} {
							run(step)
						}
						if db == p.Memory {
							var before bytes.Buffer
							if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
								t.Fatal(err)
							}
							step := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "0A000", Message: tc.message, Position: tc.position})
							step.ErrorStage = "execute"
							run(step)
							step = fixture.Prepared(step)
							if !tc.execute {
								step.ErrorStage = "prepare"
							}
							run(step)
							var after bytes.Buffer
							if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || before.String() != after.String() {
								t.Fatal("exclusion changed dump", err, after.String())
							}
							run(fixture.Select("SELECT relname FROM pg_class WHERE relname='i' OR relname='ix'", []string{"relname"}, []string{"NAME"}, nil))
							run(fixture.Select("SELECT c,s FROM t", []string{"c", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
							run(fixture.Exec("CREATE INDEX i ON t(n)", 0))
							run(fixture.Exec("ALTER TABLE t ALTER c TYPE TEXT USING c::text", 0))
							run(fixture.Select("SELECT c FROM t", []string{"c"}, []string{"TEXT"}, [][]any{{"1"}}))
						} else {
							// These definitions remain PostgreSQL-valid native capability boundaries.
							run(fixture.Exec(tc.query, 0))
							run(fixture.Select("SELECT relname FROM pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, [][]any{{tc.index}}, tc.index))
							run(fixture.Select("SELECT relname,relkind FROM pg_class WHERE relname=$1", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{tc.index, "i"}}, tc.index))
							run(fixture.Exec("DROP INDEX "+tc.index, 0))
							run(fixture.Prepared(fixture.Exec(tc.query, 0)))
							run(fixture.Select("SELECT relname,relkind FROM pg_class WHERE relname=$1", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{tc.index, "i"}}, tc.index))
							run(fixture.Exec("DROP INDEX "+tc.index, 0))
							run(fixture.Select("SELECT relname FROM pg_class WHERE relname='i' OR relname='ix'", []string{"relname"}, []string{"NAME"}, nil))
							run(fixture.Exec("CREATE INDEX i ON t(n)", 0))
							run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(3,2,'two')", 1)))
							run(fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
							run(fixture.Exec("DELETE FROM t WHERE n=3", 1))
							run(fixture.Select("SELECT c,s FROM t", []string{"c", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
							run(fixture.Exec("ALTER TABLE t ALTER c TYPE TEXT USING c::text", 0))
							run(fixture.Select("SELECT c FROM t", []string{"c"}, []string{"TEXT"}, [][]any{{"1"}}))
						}
					})
				})
			}
		})
	}
}

// Partial definitions now use shared success routes rather than the former
// native exclusion. Keep the complete direct/held and Context/plain route union.
func TestReferenceBtreeIndexPartialPublicRoutes(t *testing.T) {
	for _, plain := range []bool{false, true} {
		t.Run(map[bool]string{false: "context", true: "plain"}[plain], func(t *testing.T) {
			p := fixture.NewScenario(t)
			referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *fixture.Recorder) {
				run := func(s fixture.Step) {
					s.NonContext = plain
					fixture.RunStep(t, p.Ctx, db, recorder, s)
				}
				run(fixture.Exec("CREATE TABLE t(n INT,c INT,s TEXT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(2,1,'one')", 1))
				for _, prepared := range []bool{false, true} {
					s := fixture.Exec("CREATE INDEX i ON t(n) WHERE n>0", 0)
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname='i'", []string{"relname"}, []string{"NAME"}, [][]any{{"i"}}))
					run(fixture.Select("SELECT relname,relkind FROM pg_class WHERE relname='i'", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{"i", "i"}}))
					run(fixture.Select("SELECT c,s FROM t", []string{"c", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
					run(fixture.Exec("DROP INDEX i", 0))
				}
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='i' OR relname='ix'", []string{"relname"}, []string{"NAME"}, nil))
				run(fixture.Exec("CREATE INDEX i ON t(n)", 0))
				run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(3,2,'two')", 1)))
				run(fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
				run(fixture.Exec("DELETE FROM t WHERE n=3", 1))
				run(fixture.Select("SELECT c,s FROM t", []string{"c", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
				run(fixture.Exec("ALTER TABLE t ALTER c TYPE TEXT USING c::text", 0))
				run(fixture.Select("SELECT c FROM t", []string{"c"}, []string{"TEXT"}, [][]any{{"1"}}))
			})
		})
	}
}
