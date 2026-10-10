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
	"testing"

	"stuntgres"
)

func TestReferenceDeleteUsingMigratedReturning(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := deleteUsingNonContextRun(t, p, b, b.db)
		run(referenceExec("CREATE TABLE t(id INT NOT NULL,v TEXT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,'a'),(2,NULL),(2,NULL),(3,'c')", 4))
		run(referenceExec("CREATE SEQUENCE s START 101 CACHE 1", 0))
		q := "DELETE FROM t WHERE id=$1 RETURNING WITH (NEW AS n, OLD AS o) o.id,o.v,n.id,n.v,id+10 AS bumped,n.id IS NULL AS gone,COALESCE(n.id,o.id) AS kept"
		stmt, err := b.db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := deleteUsingNonContextHeld{referenceHeldStatement: referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: q}, direct: b.db}
		step := referenceSelect(q, []string{"id", "v", "id", "v", "bumped", "gone", "kept"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4", "BOOL", "INT4"}, [][]any{{int64(2), nil, nil, nil, int64(12), true, int64(2)}, {int64(2), nil, nil, nil, int64(12), true, int64(2)}}, 2)
		step.bag = true
		step.nonContext = true
		referenceRunStep(t, p.ctx, held, b.recorder, step)
		step.rows = nil
		referenceRunStep(t, p.ctx, held, b.recorder, step)
		run(referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), "c"}}))
		run(referenceExec("INSERT INTO t VALUES(2,NULL),(2,NULL)", 2))
		for _, count := range []int64{2, 0} {
			run(referenceExec("DELETE FROM t WHERE id=2 RETURNING *,old.*,new.*", count))
		}
		step.args = []any{1}
		step.rows = [][]any{{int64(1), "a", nil, nil, int64(11), true, int64(1)}}
		referenceRunStep(t, p.ctx, held, b.recorder, step)
		run(referenceSelect("DELETE FROM t WHERE FALSE RETURNING nextval('s')", []string{"nextval"}, []string{"INT8"}, nil))
		run(referenceSelect("SELECT is_called FROM s", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
		var called bool
		if err := b.db.QueryRow("SELECT is_called FROM s").Scan(&called); err != nil || called {
			t.Fatal(called, err)
		}
	})
}

func TestReferenceDeleteUsingMigratedReturningControls(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := deleteUsingNonContextRun(t, p, b, b.db)
		for _, step := range []referenceStep{
			referenceExec("CREATE SEQUENCE s START 101 CACHE 1", 0),
			referenceExec("CREATE TABLE t(id INT NOT NULL DEFAULT 9,v TEXT)", 0),
			referenceExec("COMMENT ON TABLE t IS 'retained'", 0),
			referenceExec("CREATE VIEW survivors AS SELECT id,v FROM t", 0),
			referenceExec("INSERT INTO t VALUES(0,NULL),(1,'a'),(2,'b'),(2,'b')", 4),
		} {
			run(step)
		}
		run(referenceErrorDiagnostic("DELETE FROM t RETURNING 10/id", controlsReferenceDiagnostic("22012", "division by zero")))
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		rows, err := b.db.QueryContext(ctx, "DELETE FROM t RETURNING *")
		if rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal(rows, err)
		}
		run(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(2)}, {int64(2)}}))
		q := "DELETE FROM t WHERE id=2 RETURNING id,v,new.id,nextval('s')"
		var retained driver.Rows
		if b.pg {
			if _, err := c.ExecContext(p.ctx, "BEGIN"); err != nil {
				t.Fatal(err)
			}
			step := referenceSelect(q, []string{"id", "v", "id", "nextval"}, []string{"INT4", "TEXT", "INT4", "INT8"}, [][]any{{int64(2), "b", nil, int64(101)}, {int64(2), "b", nil, int64(102)}})
			step.bag = true
			referenceRunStep(t, p.ctx, c, b.recorder, step)
			if _, err := c.ExecContext(p.ctx, "ROLLBACK"); err != nil {
				t.Fatal(err)
			}
		} else {
			raw, err := p.nativeConnector.Connect(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			if _, err := raw.(driver.ExecerContext).ExecContext(p.ctx, "BEGIN", nil); err != nil {
				t.Fatal(err)
			}
			retained, err = raw.(driver.QueryerContext).QueryContext(p.ctx, q, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			var private bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &private); err != nil {
				t.Fatal(err)
			}
			if _, err := raw.(driver.ExecerContext).ExecContext(p.ctx, "ROLLBACK", nil); err != nil {
				t.Fatal(err)
			}
		}
		run(referenceSelect("DELETE FROM t WHERE id=0 RETURNING id", []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}}))
		if !b.pg {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "DELETE") || !strings.Contains(dump.String(), "retained") || !strings.Contains(dump.String(), "NOT NULL") {
				t.Fatal(dump.String())
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
		}
		run(referenceSelect("SELECT id,v FROM survivors ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(2), "b"}}))
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
		if err := b.db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 103 {
			t.Fatal(next, err)
		}
		run(referenceExec("INSERT INTO t(v) VALUES('default')", 1))
		if !b.pg {
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("DELETE FROM t RETURNING *;")); err == nil {
				t.Fatal("Restore accepted DELETE history")
			}
		}
		run(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {int64(9)}}))
	})
}
