package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExpressionCoreCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE t(id INT,s TEXT,f BOOL)", 0))
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			q := "SELECT id FROM t WHERE $1"
			held, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			bad := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Context: "unnamed portal parameter $1 = '...'"}, "truth")
			bad.SelectRows = true
			bad.ErrorStage = "execute"
			fixture.ScalarRunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, held, q}, backend.Recorder, bad)
			rows, err := held.QueryContext(p.Ctx)
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
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err = backend.DB.QueryContext(canceled, "SELECT id FROM t")
			if rows != nil {
				rows.Close()
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO t VALUES(1,'x',TRUE)", 1))
	t.Run("native-raw", func(t *testing.T) {
		fixture.ScalarNativeRaw(t, p, func(raw driver.QueryerContext) {
			ctx, cancel := context.WithCancel(p.Ctx)
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
