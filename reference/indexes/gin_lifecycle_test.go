package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGINLifecycle(t *testing.T) {
	t.Run("transactions", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			run(fixture.Exec("CREATE TABLE gin_lifecycle(id integer,a integer[],v tsvector)", 0))
			run(fixture.Exec("INSERT INTO gin_lifecycle VALUES(1,'{1,2}','cat:1 rat:2'),(2,'{}','dog:1'),(3,NULL,NULL)", 3))
			const query = "SELECT id FROM gin_lifecycle WHERE a @> $1::integer[] AND v @@ $2::tsquery ORDER BY id"
			held := classHeld(t, p, conn, query)
			check := func(ids []int64) {
				s := ginIDs(query, ids, "{2}", "cat <-> rat")
				classRoutes(t, p, b, conn, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			check([]int64{1})
			run(fixture.Exec("BEGIN", 0))
			defer conn.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Prepared(fixture.Exec("CREATE INDEX gin_lifecycle_i ON gin_lifecycle USING gin(a,v)", 0)))
			run(fixture.Exec("SAVEPOINT gin_prefix", 0))
			run(fixture.Prepared(fixture.Exec("INSERT INTO gin_lifecycle VALUES(4,'{2,2}','cat:1 rat:2'),(5,'{2}','cat:1 rat:2')", 2)))
			check([]int64{1, 4, 5})
			run(fixture.Exec("UPDATE gin_lifecycle SET a='{9}',v='dog:1' WHERE id=1", 1))
			run(fixture.Exec("DELETE FROM gin_lifecycle WHERE id=4", 1))
			check([]int64{5})
			run(fixture.Exec("ROLLBACK TO gin_prefix", 0))
			check([]int64{1})
			run(fixture.Exec("COMMIT", 0))
			check([]int64{1})
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("CREATE INDEX gin_lifecycle_rolled ON gin_lifecycle USING gin(v)", 0))
			run(fixture.Exec("INSERT INTO gin_lifecycle VALUES(6,'{2}','cat:1 rat:2')", 1))
			check([]int64{1, 6})
			run(fixture.Exec("ROLLBACK", 0))
			check([]int64{1})
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='gin_lifecycle_rolled'", []string{"relname"}, []string{"NAME"}, nil))
			for _, commit := range []bool{false, true} {
				tx, err := conn.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer tx.Rollback()
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO gin_lifecycle VALUES($1,$2::integer[],$3::tsvector)", 1, 7, "{2}", "cat:1 rat:2")))
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, ginIDs(query, []int64{1, 7}, "{2}", "cat <-> rat"))
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
				}()
				if commit {
					check([]int64{1, 7})
				} else {
					check([]int64{1})
				}
			}
			run(fixture.Exec("REINDEX INDEX gin_lifecycle_i", 0))
			check([]int64{1, 7})
			run(fixture.Exec("DROP INDEX gin_lifecycle_i", 0))
			check([]int64{1, 7})
		})
	})
	t.Run("statement_atomicity", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			run(fixture.Exec("CREATE TABLE gin_atomic(id integer CONSTRAINT gin_positive CHECK(id>0),a integer[])", 0))
			run(fixture.Exec("CREATE INDEX gin_atomic_i ON gin_atomic USING gin(a)", 0))
			run(fixture.Exec("INSERT INTO gin_atomic VALUES(1,'{1}')", 1))
			d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "gin_atomic" violates check constraint "gin_positive"`)
			d.Detail, d.Schema, d.Table, d.Constraint = "Failing row contains (-1, {2}).", "public", "gin_atomic", "gin_positive"
			ginUtilityError(t, p, b, conn, "INSERT INTO gin_atomic VALUES(2,'{2}'),(-1,'{2}')", d)
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM gin_atomic ORDER BY id", []int64{1}))
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM gin_atomic WHERE a && '{2}'::integer[] ORDER BY id", nil))
			run(fixture.Exec("INSERT INTO gin_atomic VALUES(2,'{2}')", 1))
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM gin_atomic WHERE a && '{2}'::integer[] ORDER BY id", []int64{2}))
		})
	})
	t.Run("repeatable_read", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			reader, writer := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, reader, b.Recorder, s) }
			write := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, writer, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE gin_history(id integer,a integer[],v tsvector)", 0))
			run(fixture.Exec("CREATE INDEX gin_history_i ON gin_history USING gin(a,v)", 0))
			run(fixture.Exec("INSERT INTO gin_history VALUES(1,'{2}','cat:1'),(2,'{3}','dog:1')", 2))
			tx, err := reader.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			const query = "SELECT id FROM gin_history WHERE a @> '{2}'::integer[] AND v @@ 'cat'::tsquery ORDER BY id"
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, ginIDs(query, []int64{1}))
			write(fixture.Exec("UPDATE gin_history SET a='{9}',v='dog:1' WHERE id=1", 1))
			write(fixture.Exec("INSERT INTO gin_history VALUES(3,'{2}','cat:1')", 1))
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Prepared(ginIDs(query, []int64{1})))
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("INSERT INTO gin_history VALUES(4,'{2}','cat:1')", 1))
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, ginIDs(query, []int64{1, 4}))
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			run(ginIDs(query, []int64{3, 4}))
		})
	})
	t.Run("rebuild_and_closure", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			run(fixture.Exec("CREATE TABLE gin_rebuild(id integer,a integer[])", 0))
			run(fixture.Exec("CREATE INDEX gin_rebuild_i ON gin_rebuild USING gin(a)", 0))
			run(fixture.Exec("CREATE INDEX gin_rebuild_partial ON gin_rebuild USING gin(a) WHERE id>0", 0))
			run(fixture.Exec("CREATE INDEX gin_rebuild_expr ON gin_rebuild USING gin((COALESCE(a,'{}'::integer[])))", 0))
			run(fixture.Exec("INSERT INTO gin_rebuild VALUES(1,'{2,2}'),(2,'{}'),(3,NULL)", 3))
			run(fixture.Exec("CREATE TABLE gin_rebuild_copy(LIKE gin_rebuild INCLUDING INDEXES)", 0))
			run(amCount("SELECT count(*) FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid='gin_rebuild_copy'::regclass AND c.relam=2742", 3))
			run(fixture.Exec("INSERT INTO gin_rebuild_copy VALUES(8,'{2}'),(9,NULL)", 2))
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM gin_rebuild_copy WHERE COALESCE(a,'{}'::integer[]) @> '{2}'::integer[] ORDER BY id", []int64{8}))
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM gin_rebuild WHERE id>0 AND a @> '{2}'::integer[] ORDER BY id", []int64{1}))
			run(fixture.Exec("DROP INDEX gin_rebuild_partial,gin_rebuild_expr", 0))
			run(fixture.Exec("ALTER TABLE gin_rebuild ALTER COLUMN a TYPE bigint[] USING a::bigint[]", 0))
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM gin_rebuild WHERE a @> '{2}'::bigint[] ORDER BY id", []int64{1}))
			d := fixture.ControlsReferenceDiagnostic("42704", `data type integer has no default operator class for access method "gin"`)
			d.Hint = "You must specify an operator class for the index or define a default operator class for the data type."
			ginUtilityError(t, p, b, conn, "ALTER TABLE gin_rebuild ALTER COLUMN a TYPE integer USING id", d)
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM gin_rebuild WHERE a @> '{2}'::bigint[] ORDER BY id", []int64{1}))
			run(fixture.Exec("ALTER TABLE gin_rebuild RENAME COLUMN a TO tokens", 0))
			run(fixture.Exec("ALTER TABLE gin_rebuild RENAME TO gin_rebuilt", 0))
			run(fixture.Exec("ALTER INDEX gin_rebuild_i RENAME TO gin_rebuilt_i", 0))
			run(fixture.Exec("REINDEX INDEX gin_rebuilt_i", 0))
			classRoutes(t, p, b, conn, fixture.Select("SELECT pg_get_indexdef('gin_rebuilt_i'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE INDEX gin_rebuilt_i ON public.gin_rebuilt USING gin (tokens)"}}))
			run(fixture.Exec("INSERT INTO gin_rebuilt VALUES(4,'{2}')", 1))
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM gin_rebuilt WHERE tokens @> '{2}'::bigint[] ORDER BY id", []int64{1, 4}))
			run(fixture.Exec("ALTER TABLE gin_rebuilt DROP COLUMN tokens", 0))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='gin_rebuilt_i'", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Exec("DROP TABLE gin_rebuild_copy", 0))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname LIKE 'gin_rebuild_copy%'", []string{"relname"}, []string{"NAME"}, nil))
		})
	})
}
