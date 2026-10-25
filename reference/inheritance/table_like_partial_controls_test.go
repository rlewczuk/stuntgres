package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"io"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTableLikePartialControls(t *testing.T) { tableLikePartialLifecycle(t) }

func tableLikePartialLifecycle(t *testing.T) {
	fixture.TableLikeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		for _, q := range []string{
			"CREATE TABLE anchor(a INT)",
			"CREATE TABLE expr_src(id INT,n INT,gap INT,active BOOL)",
			"ALTER TABLE expr_src DROP COLUMN gap",
			"CREATE INDEX source_ordinary ON expr_src(n) WHERE active",
			"CREATE INDEX source_expression ON expr_src((10/n)) WHERE active",
			"CREATE TABLE unique_src(id INT,k INT,gap INT,active BOOL)",
			"ALTER TABLE unique_src DROP COLUMN gap",
			"CREATE UNIQUE INDEX source_unique ON unique_src(k) WHERE active AND ('anchor'::regclass IS NOT NULL)",
			"CREATE TABLE expr_clone(lead TEXT,LIKE expr_src INCLUDING INDEXES)",
			"CREATE TABLE unique_clone(lead TEXT,LIKE unique_src INCLUDING ALL)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO expr_clone VALUES('x',1,0,FALSE),('x',2,2,TRUE),('x',3,0,NULL)", 3))
		run(fixture.Exec("INSERT INTO unique_clone VALUES('x',1,7,FALSE),('x',2,7,NULL),('x',3,7,TRUE)", 3))
		run(fixture.Exec("DROP TABLE expr_src,unique_src RESTRICT", 0))
		checks := []fixture.Step{
			fixture.Select("SELECT * FROM expr_clone ORDER BY id", []string{"lead", "id", "n", "active"}, []string{"TEXT", "INT4", "INT4", "BOOL"}, [][]any{{"x", int64(1), int64(0), false}, {"x", int64(2), int64(2), true}, {"x", int64(3), int64(0), nil}}),
			fixture.Select("SELECT * FROM unique_clone ORDER BY id", []string{"lead", "id", "k", "active"}, []string{"TEXT", "INT4", "INT4", "BOOL"}, [][]any{{"x", int64(1), int64(7), false}, {"x", int64(2), int64(7), nil}, {"x", int64(3), int64(7), true}}),
			fixture.Select("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename='expr_clone' ORDER BY indexname", []string{"indexname"}, []string{"NAME"}, [][]any{{"expr_clone_expr_idx"}, {"expr_clone_n_idx"}}),
			fixture.Select("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename='unique_clone'", []string{"indexname"}, []string{"NAME"}, [][]any{{"unique_clone_k_idx"}}),
		}
		verify := func(exec func(fixture.Step), postgres bool) {
			for _, s := range checks {
				exec(s)
			}
			// Native catalog projection omits predicate-only edges, including after
			// Restore; actual predicate dependencies/enforcement remain independent.
			count := int64(0)
			if postgres {
				count = 1
			}
			exec(fixture.Select("SELECT count(*) AS dependencies FROM pg_depend WHERE objid='unique_clone_k_idx'::regclass::oid AND refobjid='anchor'::regclass::oid AND deptype='n'", []string{"dependencies"}, []string{"INT8"}, [][]any{{count}}))
			fixture.TableLikeAbsent(exec, "expr_src", prepared)
			fixture.TableLikeAbsent(exec, "unique_src", prepared)
		}
		duplicate := fixture.UniqueReferenceDuplicate("unique_clone", "unique_clone_k_idx", "Key (k)=(7) already exists.")
		future := func(exec func(fixture.Step)) {
			exec(fixture.Exec("INSERT INTO expr_clone VALUES('after',4,0,FALSE),('after',5,0,NULL),('after',6,2,TRUE)", 3))
			exec(fixture.TableLikeError("INSERT INTO expr_clone VALUES('after',7,2,TRUE),('after',8,0,TRUE)", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
			exec(fixture.TableLikeError("UPDATE expr_clone SET active=TRUE WHERE id=1", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
			exec(fixture.PartialIndexIDs("SELECT id FROM expr_clone ORDER BY id", 1, 2, 3, 4, 5, 6))
			exec(fixture.PartialIndexIDs("SELECT id FROM expr_clone WHERE active ORDER BY id", 2, 6))
			exec(fixture.Exec("UPDATE expr_clone SET active=FALSE WHERE id=2", 1))
			exec(fixture.Exec("UPDATE expr_clone SET n=0 WHERE id=2", 1))
			exec(fixture.TableLikeError("UPDATE expr_clone SET active=TRUE WHERE id=2", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
			exec(fixture.PartialIndexIDs("SELECT id FROM expr_clone WHERE active ORDER BY id", 6))
			exec(fixture.Exec("INSERT INTO unique_clone VALUES('after',4,7,FALSE),('after',5,7,NULL)", 2))
			exec(fixture.TableLikeError("INSERT INTO unique_clone VALUES('after',6,7,TRUE)", duplicate))
			exec(fixture.Exec("UPDATE unique_clone SET active=FALSE WHERE id=3", 1))
			exec(fixture.Exec("UPDATE unique_clone SET active=TRUE WHERE id=1", 1))
			exec(fixture.TableLikeError("UPDATE unique_clone SET active=TRUE WHERE id=2", duplicate))
			exec(fixture.PartialIndexIDs("SELECT id FROM unique_clone ORDER BY id", 1, 2, 3, 4, 5))
			exec(fixture.PartialIndexIDs("SELECT id FROM unique_clone WHERE active AND ('anchor'::regclass IS NOT NULL) ORDER BY id", 1))
		}
		verify(run, b.PG)
		if b.PG {
			future(run)
			return
		}
		heldConn, err := p.Native.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer heldConn.Close()
		var held driver.Rows
		if err := heldConn.Raw(func(raw any) error {
			var err error
			held, err = raw.(driver.QueryerContext).QueryContext(p.Ctx, checks[0].Query, nil)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		var before int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'expr_clone'::regclass::oid::bigint").Scan(&before); err != nil {
			t.Fatal(err)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		fixture.TableLikeAbsent(run, "expr_clone", prepared)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		var after int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'expr_clone'::regclass::oid::bigint").Scan(&after); err != nil || after == before {
			t.Fatal("replacement table incarnation unchanged", before, after, err)
		}
		verify(run, false)
		if !reflect.DeepEqual(held.Columns(), checks[0].Columns) {
			t.Fatal("retained labels changed", held.Columns())
		}
		cells := make([]driver.Value, 4)
		for _, row := range checks[0].Rows {
			if err := held.Next(cells); err != nil || !reflect.DeepEqual([]any{cells[0], cells[1], cells[2], cells[3]}, row) {
				t.Fatal("retained partial result changed", cells, err, row)
			}
		}
		if err := held.Next(cells); err != io.EOF {
			t.Fatal(err)
		}
		foreign := fixture.NewFixture(t, false, true)
		foreignConn, err := foreign.Memory.Conn(foreign.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer foreignConn.Close()
		frun := func(s fixture.Step) {
			s.Prepared = prepared
			fixture.RunStep(t, foreign.Ctx, foreignConn, foreign.MemoryRecord, s)
		}
		frun(fixture.Exec("CREATE TEMP SEQUENCE shifted CACHE 1", 0))
		frun(fixture.Select("SELECT nextval('pg_temp.shifted') AS value", []string{"value"}, []string{"INT8"}, [][]any{{int64(1)}}))
		if err := stuntgres.Restore(foreign.Ctx, foreign.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		frun(fixture.Select("SELECT nextval('pg_temp.shifted') AS value", []string{"value"}, []string{"INT8"}, [][]any{{int64(2)}}))
		verify(frun, false)
		future(frun)
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("actual partial native dump PostgreSQL replay", err)
			}
			prun := func(s fixture.Step) {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
			}
			verify(prun, true)
			future(prun)
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
		future(run)
	})
}
