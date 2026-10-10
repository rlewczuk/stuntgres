package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
)

func TestReferenceBtreeIndexExcludedDefinitionsDiagnostics(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "context", true: "nonContext"}[nonContext], func(t *testing.T) {
			for _, tc := range []struct {
				name, query, message, position, index string
				execute                               bool
			}{
				{"partial", "CREATE INDEX i ON t(n) WHERE n>0", "partial indexes are unsupported", "24", "i", false},
				{"storage", "CREATE INDEX i ON t(n) WITH(fillfactor=80)", "index storage parameters are unsupported", "24", "i", true},
				{"tablespace", "CREATE INDEX i ON t(n) TABLESPACE pg_default", "index tablespaces are unsupported", "24", "i", false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					p := newReferenceScenario(t)
					referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *referenceRecorder) {
						run := func(s referenceStep) {
							s.nonContext = nonContext
							referenceRunStep(t, p.ctx, db, recorder, s)
						}
						for _, step := range []referenceStep{referenceExec("CREATE TABLE t(n INT,c INT,s TEXT)", 0), referenceExec("INSERT INTO t VALUES(2,1,'one')", 1)} {
							run(step)
						}
						if db == p.memory {
							var before bytes.Buffer
							if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
								t.Fatal(err)
							}
							step := referenceImplicitError(tc.query, referenceDiagnostic{Code: "0A000", Message: tc.message, Position: tc.position})
							step.errorStage = "execute"
							run(step)
							step = referencePrepared(step)
							if !tc.execute {
								step.errorStage = "prepare"
							}
							run(step)
							var after bytes.Buffer
							if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || before.String() != after.String() {
								t.Fatal("exclusion changed dump", err, after.String())
							}
							run(referenceSelect("SELECT relname FROM pg_class WHERE relname='i' OR relname='ix'", []string{"relname"}, []string{"NAME"}, nil))
							run(referenceSelect("SELECT c,s FROM t", []string{"c", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
							run(referenceExec("CREATE INDEX i ON t(n)", 0))
							run(referenceExec("ALTER TABLE t ALTER c TYPE TEXT USING c::text", 0))
							run(referenceSelect("SELECT c FROM t", []string{"c"}, []string{"TEXT"}, [][]any{{"1"}}))
						} else {
							// These definitions remain PostgreSQL-valid native capability boundaries.
							run(referenceExec(tc.query, 0))
							run(referenceSelect("SELECT relname FROM pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, [][]any{{tc.index}}, tc.index))
							run(referenceSelect("SELECT relname,relkind FROM pg_class WHERE relname=$1", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{tc.index, "i"}}, tc.index))
							run(referenceExec("DROP INDEX "+tc.index, 0))
							run(referencePrepared(referenceExec(tc.query, 0)))
							run(referenceSelect("SELECT relname,relkind FROM pg_class WHERE relname=$1", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{tc.index, "i"}}, tc.index))
							run(referenceExec("DROP INDEX "+tc.index, 0))
							run(referenceSelect("SELECT relname FROM pg_class WHERE relname='i' OR relname='ix'", []string{"relname"}, []string{"NAME"}, nil))
							run(referenceExec("CREATE INDEX i ON t(n)", 0))
							run(referencePrepared(referenceExec("INSERT INTO t VALUES(3,2,'two')", 1)))
							run(referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
							run(referenceExec("DELETE FROM t WHERE n=3", 1))
							run(referenceSelect("SELECT c,s FROM t", []string{"c", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
							run(referenceExec("ALTER TABLE t ALTER c TYPE TEXT USING c::text", 0))
							run(referenceSelect("SELECT c FROM t", []string{"c"}, []string{"TEXT"}, [][]any{{"1"}}))
						}
					})
				})
			}
		})
	}
}
