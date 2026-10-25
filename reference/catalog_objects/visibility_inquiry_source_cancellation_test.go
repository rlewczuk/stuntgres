package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVisibilityInquiryPreparedSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	dsn := "reference/" + t.Name()
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			for _, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
				stmt, err := backend.DB.Prepare("SELECT " + fn + "($1)")
				if err != nil {
					t.Fatal(err)
				}
				rows, err := stmt.Query("bad")
				if rows != nil {
					rows.Close()
					stmt.Close()
					t.Fatal("bad input published cursor")
				}
				want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type oid: "bad"`)
				want.Context = "unnamed portal parameter $1 = '...'"
				fixture.AssertDiagnostic(t, err, &want)
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
			}
			for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE ids(id OID)", 0), fixture.Exec("INSERT INTO ids VALUES(23),(23)", 2)} {
				s.NonContext = true
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			}
			if !backend.PG {
				// Preserve the original public Driver.Open acquisition, sharing the
				// fixture's exact driver and opaque DSN rather than a new engine.
				raw, err := p.NativeConnector.Driver().Open(dsn)
				if err != nil {
					t.Fatal(err)
				}
				defer raw.Close()
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT pg_type_is_visible(id) FROM ids", nil)
				if err != nil {
					t.Fatal(err)
				}
				defer cursor.Close()
				values := make([]driver.Value, 1)
				if err := cursor.Next(values); err != nil || values[0] != true {
					t.Fatal(values, err)
				}
				cancel()
				if err := cursor.Next(values); !errors.Is(err, context.Canceled) {
					t.Fatal("iteration cancellation", err)
				}
				if err := cursor.Close(); err != nil {
					t.Fatal(err)
				}
				rows, err := backend.DB.QueryContext(ctx, "SELECT pg_type_is_visible(23)")
				if rows != nil {
					rows.Close()
					t.Fatal("canceled Query published cursor")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			// The recorded Context companion retains real PostgreSQL admission,
			// cancellation and joined completion; host raw Open is native-only.
		})
	}
}
