package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestReferenceSequenceArithmeticFrozenOutput(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := p.ctx
			c, peer := referenceLockConnections(t, ctx, backend.db)
			schema := fmt.Sprintf("seq_arith_%d", time.Now().UnixNano())
			t.Cleanup(func() {
				_, _ = c.ExecContext(ctx, "ROLLBACK")
				_, _ = c.ExecContext(ctx, "SET search_path TO public")
				_, _ = c.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
			})
			exec := func(q string, n int64) { referenceRunStep(t, ctx, c, backend.recorder, referenceExec(q, n)) }
			exec("CREATE SCHEMA "+schema, 0)
			exec("SET search_path TO "+schema, 0)
			exec("CREATE TABLE src(id INT, nullable BIGINT)", 0)
			exec("INSERT INTO src VALUES(1,NULL),(2,7)", 2)
			exec("CREATE SEQUENCE late_s START 3", 0)
			q := "SELECT 'late_s'::regclass,nextval('late_s')+0 FROM src WHERE id=1"
			r, err := c.QueryContext(context.WithValue(ctx, referenceRecorderKey{}, backend.recorder), q)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			metadata, err := r.ColumnTypes()
			if err != nil || len(metadata) != 2 || metadata[0].DatabaseTypeName() != "REGCLASS" || metadata[1].DatabaseTypeName() != "INT8" {
				t.Fatal(metadata, err)
			}
			labels, err := r.Columns()
			if err != nil || len(labels) != 2 || labels[0] != "regclass" || labels[1] != "?column?" {
				t.Fatal(labels, err)
			}
			if _, err := peer.ExecContext(ctx, "ALTER SEQUENCE "+schema+".late_s RENAME TO frozen_s"); err != nil {
				t.Fatal(err)
			}
			step := referenceSelect(q, []string{"regclass", "?column?"}, []string{"REGCLASS", "INT8"}, [][]any{{"late_s", int64(3)}})
			step.checkScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
				var label string
				var n int64
				if err := rows.Scan(&label, &n); err != nil {
					t.Fatal(err)
				}
				if rowIndex != 0 || label != "late_s" || n != 3 {
					t.Fatalf("frozen typed row %d = (%q,%d), want (late_s,3)", rowIndex, label, n)
				}
			}
			referenceRunStep(t, ctx, sequenceFrozenRows{referenceExecutor: c, rows: r, query: q}, backend.recorder, step)

		})
	}
}
