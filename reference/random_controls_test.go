package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceRandomControlsPreparedAndCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, s := range []referenceStep{referenceExec("CREATE TABLE materialized(id INT,n NUMERIC,f DOUBLE PRECISION)", 0), referenceExec("CREATE TABLE ids(id INT)", 0), referenceExec("INSERT INTO ids VALUES(7)", 1), referenceExec("INSERT INTO materialized SELECT id,random(1.0,1.000),random_normal(5,0) FROM ids", 1)} {
		p.step(t, s)
	}
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, e := b.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			randomReseed(t, p, c, b, .25)
			first := randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8")
			second := randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8")
			randomReseed(t, p, c, b, .25)
			q := "SELECT random()"
			stmt, e := c.PrepareContext(p.ctx, q)
			if e != nil {
				t.Fatal(e)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
			numericStep := referenceSelect("SELECT random(1.0,1.000)", []string{"random"}, []string{"NUMERIC"}, [][]any{{"1.000"}})
			numericStep.checkScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
				var text string
				if err := rows.Scan(&text); err != nil {
					t.Fatal("retained NUMERIC string scan", err)
				}
				if rowIndex != 0 || text != "1.000" {
					t.Fatal("retained NUMERIC string", rowIndex, text)
				}
			}
			if !b.pg {
				// C: the original unread native snapshot survives persistent replacement.
				recordingCtx := context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder)
				rows, e := c.QueryContext(recordingCtx, "SELECT random(1.0,1.000)")
				if e != nil {
					t.Fatal(e)
				}
				defer rows.Close()
				var dump bytes.Buffer
				if e := stuntgres.Dump(p.ctx, p.native, &dump); e != nil {
					t.Fatal(e)
				}
				if strings.Contains(dump.String(), "setseed") || strings.Contains(dump.String(), "random(") {
					t.Fatal("live generator state in dump")
				}
				if e := stuntgres.Reset(p.ctx, p.native); e != nil {
					t.Fatal(e)
				}
				if e := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); e != nil {
					t.Fatal(e)
				}
				frozen := sequenceFrozenRows{referenceExecutor: c, rows: rows, query: "SELECT random(1.0,1.000)"}
				referenceRunStep(t, p.ctx, frozen, b.recorder, numericStep)
			} else {
				// P: consume before another same-session command; no fabricated controls.
				referenceRunStep(t, p.ctx, c, b.recorder, numericStep)
			}
			referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"random"}, []string{"FLOAT8"}, [][]any{{first}}))
			if randomScalar(t, p, c, b, q, "random", "FLOAT8") != second {
				t.Fatal("catalog/control stream changed")
			}
			referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT id,n,f FROM materialized", []string{"id", "n", "f"}, []string{"INT4", "NUMERIC", "FLOAT8"}, [][]any{{int64(7), "1.000", float64(5)}}))
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			randomReseed(t, p, c, b, .25)
			var executor referenceExecutor = c
			if b.pg {
				// database/sql checks a canceled pool borrow before pq execution;
				// pq's zero-argument borrowed-Conn path can execute despite it.
				executor = b.db
			}
			result, e := executor.ExecContext(canceled, "SELECT setseed(-1)")
			if !errors.Is(e, context.Canceled) || result != nil {
				t.Fatal("pre-canceled seed", result, e)
			}
			var state interface{ SQLState() string }
			if errors.As(e, &state) {
				t.Fatal("context error became SQLSTATE", e)
			}
			if randomScalar(t, p, c, b, q, "random", "FLOAT8") != first {
				t.Fatal("canceled seed mutation")
			}
		})
	}
}
