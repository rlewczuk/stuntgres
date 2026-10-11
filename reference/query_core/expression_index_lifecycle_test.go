package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceExpressionIndexAlterTypeDependency(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(c INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			run(fixture.Exec("CREATE INDEX ix ON t((c+1))", 0))
			run(fixture.Select("SELECT c+1 AS next FROM t WHERE c+1=2", []string{"next"}, []string{"INT4"}, [][]any{{int64(2)}}))
			var id int64
			var before bytes.Buffer
			if backend.PG {
				if err := backend.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE oid='ix'::regclass").Scan(&id); err != nil || id <= 0 {
					t.Fatal(id, err)
				}
			} else {
				if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
					t.Fatal(err)
				}
			}
			s := fixture.ImplicitError("ALTER TABLE t ALTER c TYPE TEXT USING c::text", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: text + integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "46"})
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Select("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Select("SELECT c+1 AS next FROM t WHERE c+1=2", []string{"next"}, []string{"INT4"}, [][]any{{int64(2)}}))
			if backend.PG {
				var retained int64
				if err := backend.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE oid='ix'::regclass").Scan(&retained); err != nil || retained != id {
					t.Fatal("failed TYPE changed identity", id, retained, err)
				}
				run(fixture.Select("SELECT indisvalid FROM pg_index WHERE indexrelid='ix'::regclass", []string{"indisvalid"}, []string{"BOOL"}, [][]any{{true}}))
				run(fixture.Select("SELECT pg_get_indexdef('ix'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX ix ON public.t USING btree (((c + 1)))"}}))
			} else {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || before.String() != after.String() {
					t.Fatal("failed TYPE changed Dump", err, after.String())
				}
			}
			run(fixture.Prepared(fixture.Exec("ALTER TABLE t ALTER c TYPE BIGINT", 0)))
			run(fixture.Select("SELECT c,c+1 AS next FROM t", []string{"c", "next"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(2)}}))
			run(fixture.Exec("INSERT INTO t VALUES(2147483648)", 1))
			run(fixture.Select("SELECT c+1 AS next FROM t ORDER BY c", []string{"next"}, []string{"INT8"}, [][]any{{int64(2)}, {int64(2147483649)}}))
		})
	}
}

func TestReferenceExpressionIndexLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "moved")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			// The shared fixture resets public only; own this custom schema even
			// when a lifecycle assertion fails or the scenario context expires.
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := backend.DB.ExecContext(cleanup, "DROP SCHEMA IF EXISTS moved CASCADE"); err != nil {
					t.Error("expression index lifecycle schema cleanup", err)
				}
			})
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE TABLE t(unused INT,n INT,v TEXT)", 0), fixture.Exec("INSERT INTO t VALUES(9,1,'Alpha')", 1), fixture.Exec("CREATE INDEX ix ON t(lower(v),(n+1))", 0),
				fixture.Exec("ALTER TABLE t DROP COLUMN unused", 0), fixture.Exec("ALTER TABLE t RENAME COLUMN v TO renamed", 0), fixture.Exec("ALTER TABLE t RENAME TO renamed_t", 0),
				fixture.Prepared(fixture.Exec("INSERT INTO renamed_t VALUES(2,'BETA')", 1)), fixture.Select("SELECT n,lower(renamed) AS folded FROM renamed_t ORDER BY n", []string{"n", "folded"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "alpha"}, {int64(2), "beta"}}),
				fixture.Exec("CREATE SCHEMA moved", 0), fixture.Exec("ALTER TABLE renamed_t SET SCHEMA moved", 0), fixture.Select("SELECT lower(renamed) AS folded FROM moved.renamed_t ORDER BY n", []string{"folded"}, []string{"TEXT"}, [][]any{{"alpha"}, {"beta"}}), fixture.Exec("ALTER TABLE moved.renamed_t DROP COLUMN renamed", 0), fixture.Select("SELECT relname FROM pg_class WHERE relname='ix'", []string{"relname"}, []string{"NAME"}, nil),
			} {
				run(s)
			}
		})
	}
}
