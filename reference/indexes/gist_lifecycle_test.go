package reference_test

import (
	"bytes"
	"context"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGiSTLifecycle(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE gist_lifecycle(id integer,p point,v tsvector)", 0))
		run(fixture.Exec("INSERT INTO gist_lifecycle VALUES(1,'(0,0)','a:1'),(2,'(3,4)','b:2'),(3,NULL,NULL)", 3))
		const query = "SELECT id FROM gist_lifecycle WHERE p <@ box '(5,5),(-1,-1)' ORDER BY id"
		held := classHeld(t, p, conn, query)
		check := func(want [][]any) {
			s := fixture.Select(query, []string{"id"}, []string{"INT4"}, want)
			classRoutes(t, p, b, conn, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(fixture.Exec("BEGIN", 0))
		defer conn.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Prepared(fixture.Exec("CREATE INDEX gist_lifecycle_i ON gist_lifecycle USING gist((point(id,0)),p) INCLUDE(v) WHERE id>0", 0)))
		run(fixture.Exec("SAVEPOINT prefix", 0))
		run(fixture.Exec("INSERT INTO gist_lifecycle SELECT 4,p,v FROM gist_lifecycle WHERE id=1", 1))
		check([][]any{{int64(1)}, {int64(2)}, {int64(4)}})
		run(fixture.Exec("UPDATE gist_lifecycle SET p='(20,20)' WHERE id=2", 1))
		run(fixture.Exec("DELETE FROM gist_lifecycle WHERE id=1", 1))
		check([][]any{{int64(4)}})
		run(fixture.Exec("ROLLBACK TO prefix", 0))
		check([][]any{{int64(1)}, {int64(2)}})
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE INDEX gist_rolled_back ON gist_lifecycle USING gist(v tsvector_ops(siglen=64))", 0))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='gist_rolled_back'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Exec("ALTER TABLE gist_lifecycle RENAME COLUMN p TO position", 0))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM gist_lifecycle WHERE position ~= point '(0,0)'", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(fixture.Exec("ALTER INDEX gist_lifecycle_i RENAME TO gist_lifecycle_renamed", 0))
		run(fixture.Exec("REINDEX INDEX gist_lifecycle_renamed", 0))
		run(fixture.Exec("ALTER TABLE gist_lifecycle ALTER COLUMN id TYPE bigint", 0))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM gist_lifecycle WHERE position <@ box '(5,5),(-1,-1)' ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}}))
		run(fixture.Exec("DROP INDEX gist_lifecycle_renamed", 0))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM gist_lifecycle WHERE position ~= point '(0,0)'", []string{"id"}, []string{"INT8"}, [][]any{{int64(1)}}))
	})
}

func TestReferenceGiSTCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, c := fixture.LockConnections(t, p.Ctx, b.DB)
				runA := transactionalIndexRun(t, p, b, a)
				run := transactionalIndexRun(t, p, b, c)
				runA(fixture.Exec("CREATE TABLE gist_cancel(id integer,p point)", 0))
				const q = "CREATE INDEX gist_cancel_i ON gist_cancel USING gist(p) WITH(buffering=off)"
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
				runA(fixture.Exec("INSERT INTO gist_cancel VALUES(1,'(0,0)')", 1))
				fixture.RelationLockActiveCancel(t, p, b, c, q, func(ctx context.Context) fixture.RelationOutcome {
					r, err := executor.ExecContext(ctx, q)
					return fixture.RelationOutcome{Result: r, Err: err}
				})
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='gist_cancel_i'", []string{"relname"}, []string{"NAME"}, nil))
				runA(fixture.Exec("COMMIT", 0))
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 0))
				run(fixture.Select("SELECT id FROM gist_cancel ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
}

func TestReferenceGiSTFeatureSmoke(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE gist_smoke(id integer,p point,v tsvector)", 0))
		run(fixture.Exec("INSERT INTO gist_smoke VALUES(1,'(0,0)','a:1'),(2,'(3,4)','b:2'),(3,'(0,5)',NULL),(4,NULL,NULL)", 4))
		const q = "SELECT id,p <-> point '(0,0)' AS d FROM gist_smoke ORDER BY p <-> point '(0,0)' NULLS LAST,id"
		held := classHeld(t, p, conn, q)
		run(fixture.Exec("CREATE INDEX gist_smoke_i ON gist_smoke USING gist(p) INCLUDE(id) WITH(fillfactor=80,buffering=off)", 0))
		run(fixture.Exec("CREATE INDEX gist_smoke_vector ON gist_smoke USING gist(v tsvector_ops(siglen=32))", 0))
		check := func(want [][]any) {
			s := fixture.Select(q, []string{"id", "d"}, []string{"INT4", "FLOAT8"}, want)
			classRoutes(t, p, b, conn, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		before := [][]any{{int64(1), float64(0)}, {int64(2), float64(5)}, {int64(3), float64(5)}, {int64(4), nil}}
		check(before)
		classRoutes(t, p, b, conn, fixture.Select(q+" LIMIT 3", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, before[:3]))
		run(fixture.Select("SELECT pg_index_column_has_property('gist_smoke_i'::regclass,1,'distance_orderable') AS d,pg_index_column_has_property('gist_smoke_i'::regclass,1,'orderable') AS o", []string{"d", "o"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
		run(fixture.Exec("INSERT INTO gist_smoke VALUES(5,'(1,0)','c:3')", 1))
		run(fixture.Exec("UPDATE gist_smoke SET p='(6,8)' WHERE id=2", 1))
		run(fixture.Exec("DELETE FROM gist_smoke WHERE id=3", 1))
		check([][]any{{int64(1), float64(0)}, {int64(5), float64(1)}, {int64(2), float64(10)}, {int64(4), nil}})
		run(fixture.Exec("ALTER INDEX gist_smoke_i SET(buffering=auto)", 0))
		run(fixture.Exec("ALTER INDEX gist_smoke_i RESET(buffering)", 0))
		run(fixture.Select("SELECT attoptions::text AS options FROM pg_attribute WHERE attrelid='gist_smoke_vector'::regclass AND attnum=1", []string{"options"}, []string{"TEXT"}, [][]any{{"{siglen=32}"}}))
		run(fixture.Exec("CREATE TABLE gist_smoke_ex(p point,CONSTRAINT gist_smoke_exclusion EXCLUDE USING gist(p WITH ~=))", 0))
		run(fixture.Exec("INSERT INTO gist_smoke_ex VALUES('(1,2)')", 1))
		run(fixture.ErrorDiagnostic("INSERT INTO gist_smoke_ex VALUES('(1,2)')", fixture.ExclusionReferenceConflict("gist_smoke_ex", "gist_smoke_exclusion", "p", "(1,2)", "(1,2)")))
		d := fixture.ControlsReferenceDiagnostic("42704", `data type integer has no default operator class for access method "gist"`)
		d.Hint = "You must specify an operator class for the index or define a default operator class for the data type."
		run(fixture.ErrorDiagnostic("CREATE INDEX gist_smoke_bad ON gist_smoke USING gist(id)", d))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='gist_smoke_bad'", []string{"relname"}, []string{"NAME"}, nil))
		if !b.PG {
			dump := fixture.CheckDump(t, p)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			check([][]any{{int64(1), float64(0)}, {int64(5), float64(1)}, {int64(2), float64(10)}, {int64(4), nil}})
			run(fixture.Exec("INSERT INTO gist_smoke VALUES(6,'(2,0)','z:6')", 1))
			run(fixture.Select("SELECT id FROM gist_smoke WHERE p ~= point '(2,0)'", []string{"id"}, []string{"INT4"}, [][]any{{int64(6)}}))
		}
	})
}
