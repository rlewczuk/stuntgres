package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSJSONCancellation(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer ftsConvertSetting(t, p, c)()
		for _, form := range ftsJSONForms() {
			query, args := form.query(false, "english", `["cat","dog"]`, `"string"`)
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			// Pre-canceled pq I/O can race transport cancellation. Only native
			// promises ordinary context.Canceled and no published cursor here;
			// actual mid-traversal admission is witnessed privately in core.
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				rows, err := c.QueryContext(ctx, query, args...)
				if rows != nil {
					rows.Close()
					t.Fatal("canceled direct conversion published rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("direct cancellation", err)
				}
				rows, err = stmt.QueryContext(ctx, args...)
				if rows != nil {
					rows.Close()
					t.Fatal("canceled held conversion published rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("held cancellation", err)
				}
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, ftsJSONValue(query, "'cat':1 'dog':3", args...))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsJSONValue(query, "'cat':1 'dog':3", args...))
			stmt.Close()
		}
	})
}
