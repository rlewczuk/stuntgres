package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"io"
	"reflect"
	"stuntgres"
	"testing"
)

func TestReferenceTableLikePartialControls(t *testing.T) { tableLikePartialLifecycle(t) }

func tableLikePartialLifecycle(t *testing.T) {
	tableLikeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, prepared bool, run func(referenceStep)) {
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
			run(referenceExec(q, 0))
		}
		run(referenceExec("INSERT INTO expr_clone VALUES('x',1,0,FALSE),('x',2,2,TRUE),('x',3,0,NULL)", 3))
		run(referenceExec("INSERT INTO unique_clone VALUES('x',1,7,FALSE),('x',2,7,NULL),('x',3,7,TRUE)", 3))
		run(referenceExec("DROP TABLE expr_src,unique_src RESTRICT", 0))
		checks := []referenceStep{
			referenceSelect("SELECT * FROM expr_clone ORDER BY id", []string{"lead", "id", "n", "active"}, []string{"TEXT", "INT4", "INT4", "BOOL"}, [][]any{{"x", int64(1), int64(0), false}, {"x", int64(2), int64(2), true}, {"x", int64(3), int64(0), nil}}),
			referenceSelect("SELECT * FROM unique_clone ORDER BY id", []string{"lead", "id", "k", "active"}, []string{"TEXT", "INT4", "INT4", "BOOL"}, [][]any{{"x", int64(1), int64(7), false}, {"x", int64(2), int64(7), nil}, {"x", int64(3), int64(7), true}}),
			referenceSelect("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename='expr_clone' ORDER BY indexname", []string{"indexname"}, []string{"NAME"}, [][]any{{"expr_clone_expr_idx"}, {"expr_clone_n_idx"}}),
			referenceSelect("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename='unique_clone'", []string{"indexname"}, []string{"NAME"}, [][]any{{"unique_clone_k_idx"}}),
		}
		verify := func(exec func(referenceStep), postgres bool) {
			for _, s := range checks {
				exec(s)
			}
			// Native catalog projection omits predicate-only edges, including after
			// Restore; actual predicate dependencies/enforcement remain independent.
			count := int64(0)
			if postgres {
				count = 1
			}
			exec(referenceSelect("SELECT count(*) AS dependencies FROM pg_depend WHERE objid='unique_clone_k_idx'::regclass::oid AND refobjid='anchor'::regclass::oid AND deptype='n'", []string{"dependencies"}, []string{"INT8"}, [][]any{{count}}))
			tableLikeAbsent(exec, "expr_src", prepared)
			tableLikeAbsent(exec, "unique_src", prepared)
		}
		duplicate := uniqueReferenceDuplicate("unique_clone", "unique_clone_k_idx", "Key (k)=(7) already exists.")
		future := func(exec func(referenceStep)) {
			exec(referenceExec("INSERT INTO expr_clone VALUES('after',4,0,FALSE),('after',5,0,NULL),('after',6,2,TRUE)", 3))
			exec(tableLikeError("INSERT INTO expr_clone VALUES('after',7,2,TRUE),('after',8,0,TRUE)", referenceDiagnostic{Code: "22012", Message: "division by zero"}))
			exec(tableLikeError("UPDATE expr_clone SET active=TRUE WHERE id=1", referenceDiagnostic{Code: "22012", Message: "division by zero"}))
			exec(partialIndexIDs("SELECT id FROM expr_clone ORDER BY id", 1, 2, 3, 4, 5, 6))
			exec(partialIndexIDs("SELECT id FROM expr_clone WHERE active ORDER BY id", 2, 6))
			exec(referenceExec("UPDATE expr_clone SET active=FALSE WHERE id=2", 1))
			exec(referenceExec("UPDATE expr_clone SET n=0 WHERE id=2", 1))
			exec(tableLikeError("UPDATE expr_clone SET active=TRUE WHERE id=2", referenceDiagnostic{Code: "22012", Message: "division by zero"}))
			exec(partialIndexIDs("SELECT id FROM expr_clone WHERE active ORDER BY id", 6))
			exec(referenceExec("INSERT INTO unique_clone VALUES('after',4,7,FALSE),('after',5,7,NULL)", 2))
			exec(tableLikeError("INSERT INTO unique_clone VALUES('after',6,7,TRUE)", duplicate))
			exec(referenceExec("UPDATE unique_clone SET active=FALSE WHERE id=3", 1))
			exec(referenceExec("UPDATE unique_clone SET active=TRUE WHERE id=1", 1))
			exec(tableLikeError("UPDATE unique_clone SET active=TRUE WHERE id=2", duplicate))
			exec(partialIndexIDs("SELECT id FROM unique_clone ORDER BY id", 1, 2, 3, 4, 5))
			exec(partialIndexIDs("SELECT id FROM unique_clone WHERE active AND ('anchor'::regclass IS NOT NULL) ORDER BY id", 1))
		}
		verify(run, b.pg)
		if b.pg {
			future(run)
			return
		}
		heldConn, err := p.native.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer heldConn.Close()
		var held driver.Rows
		if err := heldConn.Raw(func(raw any) error {
			var err error
			held, err = raw.(driver.QueryerContext).QueryContext(p.ctx, checks[0].query, nil)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		var before int64
		if err := c.QueryRowContext(p.ctx, "SELECT 'expr_clone'::regclass::oid::bigint").Scan(&before); err != nil {
			t.Fatal(err)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		tableLikeAbsent(run, "expr_clone", prepared)
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		var after int64
		if err := c.QueryRowContext(p.ctx, "SELECT 'expr_clone'::regclass::oid::bigint").Scan(&after); err != nil || after == before {
			t.Fatal("replacement table incarnation unchanged", before, after, err)
		}
		verify(run, false)
		if !reflect.DeepEqual(held.Columns(), checks[0].columns) {
			t.Fatal("retained labels changed", held.Columns())
		}
		cells := make([]driver.Value, 4)
		for _, row := range checks[0].rows {
			if err := held.Next(cells); err != nil || !reflect.DeepEqual([]any{cells[0], cells[1], cells[2], cells[3]}, row) {
				t.Fatal("retained partial result changed", cells, err, row)
			}
		}
		if err := held.Next(cells); err != io.EOF {
			t.Fatal(err)
		}
		foreign := newReferenceFixture(t, false, true)
		foreignConn, err := foreign.memory.Conn(foreign.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer foreignConn.Close()
		frun := func(s referenceStep) {
			s.prepared = prepared
			referenceRunStep(t, foreign.ctx, foreignConn, foreign.memoryRecord, s)
		}
		frun(referenceExec("CREATE TEMP SEQUENCE shifted CACHE 1", 0))
		frun(referenceSelect("SELECT nextval('pg_temp.shifted') AS value", []string{"value"}, []string{"INT8"}, [][]any{{int64(1)}}))
		if err := stuntgres.Restore(foreign.ctx, foreign.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		frun(referenceSelect("SELECT nextval('pg_temp.shifted') AS value", []string{"value"}, []string{"INT8"}, [][]any{{int64(2)}}))
		verify(frun, false)
		future(frun)
		if p.postgres != nil {
			if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
				t.Fatal("actual partial native dump PostgreSQL replay", err)
			}
			prun := func(s referenceStep) {
				s.prepared = prepared
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
			}
			verify(prun, true)
			future(prun)
			if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
		future(run)
	})
}
