package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSPGiSTLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "spgist_moved")
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE spgist_lifecycle(id integer,p point,v text)", 0))
		run(fixture.Exec("INSERT INTO spgist_lifecycle VALUES(1,'(0,0)','a'),(2,'(3,4)','b'),(3,NULL,NULL)", 3))
		const query = "SELECT id FROM spgist_lifecycle WHERE p <@ box '(5,5),(-1,-1)' ORDER BY id"
		held := classHeld(t, p, conn, query)
		check := func(want [][]any) {
			s := fixture.Select(query, []string{"id"}, []string{"INT4"}, want)
			classRoutes(t, p, b, conn, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(fixture.Exec("BEGIN", 0))
		defer conn.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Prepared(fixture.Exec("CREATE INDEX spgist_lifecycle_i ON spgist_lifecycle USING spgist((point(id,0))) INCLUDE(v) WHERE id>0", 0)))
		run(fixture.Exec("SAVEPOINT prefix", 0))
		run(fixture.Exec("INSERT INTO spgist_lifecycle SELECT 4,p,v FROM spgist_lifecycle WHERE id=1", 1))
		check([][]any{{int64(1)}, {int64(2)}, {int64(4)}})
		run(fixture.Exec("UPDATE spgist_lifecycle SET p='(20,20)' WHERE id=2", 1))
		run(fixture.Exec("DELETE FROM spgist_lifecycle WHERE id=1", 1))
		check([][]any{{int64(4)}})
		run(fixture.Exec("ROLLBACK TO prefix", 0))
		check([][]any{{int64(1)}, {int64(2)}})
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE INDEX spgist_rolled_back ON spgist_lifecycle USING spgist(v text_ops)", 0))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='spgist_rolled_back'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Exec("ALTER TABLE spgist_lifecycle RENAME COLUMN p TO position", 0))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM spgist_lifecycle WHERE position ~= point '(0,0)'", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(fixture.Exec("ALTER INDEX spgist_lifecycle_i RENAME TO spgist_lifecycle_renamed", 0))
		run(fixture.Exec("REINDEX INDEX spgist_lifecycle_renamed", 0))
		run(fixture.Exec("ALTER TABLE spgist_lifecycle ALTER COLUMN id TYPE bigint", 0))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM spgist_lifecycle WHERE position <@ box '(5,5),(-1,-1)' ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}}))
		run(fixture.Exec("DROP INDEX spgist_lifecycle_renamed", 0))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM spgist_lifecycle WHERE position ~= point '(0,0)'", []string{"id"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(fixture.Exec("CREATE INDEX spgist_lifecycle_text ON spgist_lifecycle USING spgist(v)", 0))
		const prefixQuery = "SELECT id,v::text AS v FROM spgist_lifecycle WHERE v ^@ $1::text ORDER BY id"
		prefixHeld := classHeld(t, p, conn, prefixQuery)
		checkPrefix := func(prefix any, rows [][]any) {
			s := fixture.Select(prefixQuery, []string{"id", "v"}, []string{"INT8", "TEXT"}, rows, prefix)
			classRoutes(t, p, b, conn, s)
			fixture.RunStep(t, p.Ctx, prefixHeld, b.Recorder, s)
		}
		checkPrefix("a", [][]any{{int64(1), "a"}})
		run(fixture.Exec("ALTER INDEX spgist_lifecycle_text RENAME TO spgist_lifecycle_text_renamed", 0))
		checkPrefix("b", [][]any{{int64(2), "b"}})
		run(fixture.Exec("ALTER TABLE spgist_lifecycle ALTER COLUMN v TYPE varchar", 0))
		checkPrefix("a", [][]any{{int64(1), "a"}})
		checkPrefix("b", [][]any{{int64(2), "b"}})
		checkPrefix(nil, nil)
		d := fixture.ControlsReferenceDiagnostic("42704", `data type integer has no default operator class for access method "spgist"`)
		d.Hint = "You must specify an operator class for the index or define a default operator class for the data type."
		run(fixture.ErrorDiagnostic("ALTER TABLE spgist_lifecycle ALTER COLUMN v TYPE integer USING 1", d))
		run(fixture.Select("SELECT id,v::text AS v FROM spgist_lifecycle ORDER BY id", []string{"id", "v"}, []string{"INT8", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(3), nil}}))
		checkPrefix("a", [][]any{{int64(1), "a"}})
		run(fixture.Exec("CREATE SCHEMA spgist_moved", 0))
		run(fixture.Exec("ALTER TABLE spgist_lifecycle RENAME TO spgist_renamed", 0))
		run(fixture.Exec("ALTER TABLE spgist_renamed SET SCHEMA spgist_moved", 0))
		run(fixture.Exec("REINDEX INDEX spgist_moved.spgist_lifecycle_text_renamed", 0))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM spgist_moved.spgist_renamed WHERE v ^@ 'a' ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(fixture.Exec("DROP TABLE spgist_moved.spgist_renamed", 0))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='spgist_lifecycle_text_renamed'", []string{"relname"}, []string{"NAME"}, nil))
	})
}

func TestReferenceSPGiSTCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, c := fixture.LockConnections(t, p.Ctx, b.DB)
				runA := transactionalIndexRun(t, p, b, a)
				run := transactionalIndexRun(t, p, b, c)
				runA(fixture.Exec("CREATE TABLE spgist_cancel(id integer,p point)", 0))
				const q = "CREATE INDEX spgist_cancel_i ON spgist_cancel USING spgist(p) WITH(fillfactor=80)"
				var executor fixture.Executor = c
				if prepared {
					executor = classHeld(t, p, c, q)
				}
				if !b.PG {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					result, err := executor.ExecContext(ctx, q)
					if result != nil || !errors.Is(err, context.Canceled) {
						t.Fatal("pre-canceled adapter boundary", result, err)
					}
				}
				runA(fixture.Exec("BEGIN", 0))
				defer a.ExecContext(p.Ctx, "ROLLBACK")
				runA(fixture.Exec("INSERT INTO spgist_cancel VALUES(1,'(0,0)')", 1))
				fixture.RelationLockActiveCancel(t, p, b, c, q, func(ctx context.Context) fixture.RelationOutcome {
					r, err := executor.ExecContext(ctx, q)
					return fixture.RelationOutcome{Result: r, Err: err}
				})
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='spgist_cancel_i'", []string{"relname"}, []string{"NAME"}, nil))
				runA(fixture.Exec("COMMIT", 0))
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 0))
				run(fixture.Select("SELECT id FROM spgist_cancel ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
}
