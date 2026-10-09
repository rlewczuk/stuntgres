package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
)

func TestReferenceExpressionCoreCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE t(id INT,s TEXT,f BOOL)", 0))
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			q := "SELECT id FROM t WHERE $1"
			held, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			bad := referenceImplicitError(q, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Context: "unnamed portal parameter $1 = '...'"}, "truth")
			bad.selectRows = true
			bad.errorStage = "execute"
			scalarRunStep(t, p.ctx, referenceHeldStatement{backend.db, held, q}, backend.recorder, bad)
			rows, err := held.QueryContext(p.ctx)
			if rows != nil {
				rows.Close()
			}
			if err == nil {
				t.Fatal("missing actual arguments accepted")
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("argument count acquired SQLSTATE", err)
			}
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			rows, err = backend.db.QueryContext(canceled, "SELECT id FROM t")
			if rows != nil {
				rows.Close()
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
	scalarStep(t, p, referenceExec("INSERT INTO t VALUES(1,'x',TRUE)", 1))
	t.Run("native-raw", func(t *testing.T) {
		scalarNativeRaw(t, p, func(raw driver.QueryerContext) {
			ctx, cancel := context.WithCancel(p.ctx)
			cursor, err := raw.QueryContext(ctx, "SELECT 7 FROM t", nil)
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			defer cursor.Close()
			cancel()
			if err := cursor.Next(make([]driver.Value, 1)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	})
}
