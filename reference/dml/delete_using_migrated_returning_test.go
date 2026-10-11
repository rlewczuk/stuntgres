package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteUsingMigratedReturning(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := deleteUsingNonContextRun(t, p, b, b.DB)
		run(fixture.Exec("CREATE TABLE t(id INT NOT NULL,v TEXT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'a'),(2,NULL),(2,NULL),(3,'c')", 4))
		run(fixture.Exec("CREATE SEQUENCE s START 101 CACHE 1", 0))
		q := "DELETE FROM t WHERE id=$1 RETURNING WITH (NEW AS n, OLD AS o) o.id,o.v,n.id,n.v,id+10 AS bumped,n.id IS NULL AS gone,COALESCE(n.id,o.id) AS kept"
		stmt, err := b.DB.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := deleteUsingNonContextHeld{HeldStatement: fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, direct: b.DB}
		step := fixture.Select(q, []string{"id", "v", "id", "v", "bumped", "gone", "kept"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4", "BOOL", "INT4"}, [][]any{{int64(2), nil, nil, nil, int64(12), true, int64(2)}, {int64(2), nil, nil, nil, int64(12), true, int64(2)}}, 2)
		step.Bag = true
		step.NonContext = true
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		step.Rows = nil
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), "c"}}))
		run(fixture.Exec("INSERT INTO t VALUES(2,NULL),(2,NULL)", 2))
		for _, count := range []int64{2, 0} {
			run(fixture.Exec("DELETE FROM t WHERE id=2 RETURNING *,old.*,new.*", count))
		}
		step.Args = []any{1}
		step.Rows = [][]any{{int64(1), "a", nil, nil, int64(11), true, int64(1)}}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		run(fixture.Select("DELETE FROM t WHERE FALSE RETURNING nextval('s')", []string{"nextval"}, []string{"INT8"}, nil))
		run(fixture.Select("SELECT is_called FROM s", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
		var called bool
		if err := b.DB.QueryRow("SELECT is_called FROM s").Scan(&called); err != nil || called {
			t.Fatal(called, err)
		}
	})
}

func TestReferenceDeleteUsingMigratedReturningControls(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := deleteUsingNonContextRun(t, p, b, b.DB)
		for _, step := range []fixture.Step{
			fixture.Exec("CREATE SEQUENCE s START 101 CACHE 1", 0),
			fixture.Exec("CREATE TABLE t(id INT NOT NULL DEFAULT 9,v TEXT)", 0),
			fixture.Exec("COMMENT ON TABLE t IS 'retained'", 0),
			fixture.Exec("CREATE VIEW survivors AS SELECT id,v FROM t", 0),
			fixture.Exec("INSERT INTO t VALUES(0,NULL),(1,'a'),(2,'b'),(2,'b')", 4),
		} {
			run(step)
		}
		run(fixture.ErrorDiagnostic("DELETE FROM t RETURNING 10/id", fixture.ControlsReferenceDiagnostic("22012", "division by zero")))
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		rows, err := b.DB.QueryContext(ctx, "DELETE FROM t RETURNING *")
		if rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal(rows, err)
		}
		run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(2)}, {int64(2)}}))
		q := "DELETE FROM t WHERE id=2 RETURNING id,v,new.id,nextval('s')"
		var retained driver.Rows
		if b.PG {
			if _, err := c.ExecContext(p.Ctx, "BEGIN"); err != nil {
				t.Fatal(err)
			}
			step := fixture.Select(q, []string{"id", "v", "id", "nextval"}, []string{"INT4", "TEXT", "INT4", "INT8"}, [][]any{{int64(2), "b", nil, int64(101)}, {int64(2), "b", nil, int64(102)}})
			step.Bag = true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			if _, err := c.ExecContext(p.Ctx, "ROLLBACK"); err != nil {
				t.Fatal(err)
			}
		} else {
			raw, err := p.NativeConnector.Connect(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			if _, err := raw.(driver.ExecerContext).ExecContext(p.Ctx, "BEGIN", nil); err != nil {
				t.Fatal(err)
			}
			retained, err = raw.(driver.QueryerContext).QueryContext(p.Ctx, q, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			var private bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &private); err != nil {
				t.Fatal(err)
			}
			if _, err := raw.(driver.ExecerContext).ExecContext(p.Ctx, "ROLLBACK", nil); err != nil {
				t.Fatal(err)
			}
		}
		run(fixture.Select("DELETE FROM t WHERE id=0 RETURNING id", []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}}))
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "DELETE") || !strings.Contains(dump.String(), "retained") || !strings.Contains(dump.String(), "NOT NULL") {
				t.Fatal(dump.String())
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
		}
		run(fixture.Select("SELECT id,v FROM survivors ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(2), "b"}}))
		if retained != nil {
			dst := make([]driver.Value, 4)
			for _, draw := range []int64{101, 102} {
				if err := retained.Next(dst); err != nil || !reflect.DeepEqual(dst, []driver.Value{int64(2), "b", nil, draw}) {
					t.Fatal(dst, err)
				}
			}
			if err := retained.Next(dst); err != io.EOF {
				t.Fatal(err)
			}
		}
		var next int64
		if err := b.DB.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 103 {
			t.Fatal(next, err)
		}
		run(fixture.Exec("INSERT INTO t(v) VALUES('default')", 1))
		if !b.PG {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("DELETE FROM t RETURNING *;")); err == nil {
				t.Fatal("Restore accepted DELETE history")
			}
		}
		run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {int64(9)}}))
	})
}
