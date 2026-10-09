package reference_test

import (
	"bytes"
	"stuntgres"
	"testing"
)

func TestReferenceExpressionIndexAlterTypeDependency(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(c INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1)", 1))
			run(referenceExec("CREATE INDEX ix ON t((c+1))", 0))
			run(referenceSelect("SELECT c+1 AS next FROM t WHERE c+1=2", []string{"next"}, []string{"INT4"}, [][]any{{int64(2)}}))
			var id int64
			var before bytes.Buffer
			if backend.pg {
				if err := backend.db.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_class WHERE oid='ix'::regclass").Scan(&id); err != nil || id <= 0 {
					t.Fatal(id, err)
				}
			} else {
				if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
					t.Fatal(err)
				}
			}
			s := referenceImplicitError("ALTER TABLE t ALTER c TYPE TEXT USING c::text", referenceDiagnostic{Code: "42883", Message: "operator does not exist: text + integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "46"})
			s.errorStage = "execute"
			run(s)
			run(referenceSelect("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(referenceSelect("SELECT c+1 AS next FROM t WHERE c+1=2", []string{"next"}, []string{"INT4"}, [][]any{{int64(2)}}))
			if backend.pg {
				var retained int64
				if err := backend.db.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_class WHERE oid='ix'::regclass").Scan(&retained); err != nil || retained != id {
					t.Fatal("failed TYPE changed identity", id, retained, err)
				}
				run(referenceSelect("SELECT indisvalid FROM pg_index WHERE indexrelid='ix'::regclass", []string{"indisvalid"}, []string{"BOOL"}, [][]any{{true}}))
				run(referenceSelect("SELECT pg_get_indexdef('ix'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX ix ON public.t USING btree (((c + 1)))"}}))
			} else {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || before.String() != after.String() {
					t.Fatal("failed TYPE changed Dump", err, after.String())
				}
			}
			run(referencePrepared(referenceExec("ALTER TABLE t ALTER c TYPE BIGINT", 0)))
			run(referenceSelect("SELECT c,c+1 AS next FROM t", []string{"c", "next"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(2)}}))
			run(referenceExec("INSERT INTO t VALUES(2147483648)", 1))
			run(referenceSelect("SELECT c+1 AS next FROM t ORDER BY c", []string{"next"}, []string{"INT8"}, [][]any{{int64(2)}, {int64(2147483649)}}))
		})
	}
}

func TestReferenceExpressionIndexLifecycle(t *testing.T) {
	schemaReferenceCleanup(t, "moved")
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			for _, s := range []referenceStep{
				referenceExec("CREATE TABLE t(unused INT,n INT,v TEXT)", 0), referenceExec("INSERT INTO t VALUES(9,1,'Alpha')", 1), referenceExec("CREATE INDEX ix ON t(lower(v),(n+1))", 0),
				referenceExec("ALTER TABLE t DROP COLUMN unused", 0), referenceExec("ALTER TABLE t RENAME COLUMN v TO renamed", 0), referenceExec("ALTER TABLE t RENAME TO renamed_t", 0),
				referencePrepared(referenceExec("INSERT INTO renamed_t VALUES(2,'BETA')", 1)), referenceSelect("SELECT n,lower(renamed) AS folded FROM renamed_t ORDER BY n", []string{"n", "folded"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "alpha"}, {int64(2), "beta"}}),
				referenceExec("CREATE SCHEMA moved", 0), referenceExec("ALTER TABLE renamed_t SET SCHEMA moved", 0), referenceSelect("SELECT lower(renamed) AS folded FROM moved.renamed_t ORDER BY n", []string{"folded"}, []string{"TEXT"}, [][]any{{"alpha"}, {"beta"}}), referenceExec("ALTER TABLE moved.renamed_t DROP COLUMN renamed", 0), referenceSelect("SELECT relname FROM pg_class WHERE relname='ix'", []string{"relname"}, []string{"NAME"}, nil),
			} {
				run(s)
			}
		})
	}
}
