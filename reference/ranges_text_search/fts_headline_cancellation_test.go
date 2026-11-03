package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFTSHeadlineCancellation(t *testing.T) {
	for _, stage := range []struct{ name, document, options, frame, point string }{
		{"normalization", strings.Repeat("alpha Cats beta rats gamma ", 100000), "HighlightAll=true", ".walk", ""},
		// One genuine matching cover bounds discovery before first selection;
		// the remaining source still exercises the real configured walk.
		{"selection", "Cats rats " + strings.Repeat("alpha beta gamma delta ", 40000), "MaxFragments=100000,MaxWords=5,MinWords=1", "fts.markHeadlineFragments", "text_search.headline.select"},
		{"output", strings.Repeat("Cats rats ", 100000), "HighlightAll=true,StartSel=<<,StopSel=>>", "fts.renderHeadlineWords", "text_search.headline.render"},
	} {
		for _, prepared := range []bool{false, true} {
			for _, queryMode := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s_prepared_%t_query_%t", stage.name, prepared, queryMode), func(t *testing.T) {
					ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						query := "SELECT ts_headline('english'::regconfig,$1::text,$2::tsquery,$3::text)"
						var stmt *sql.Stmt
						if prepared {
							var err error
							stmt, err = c.PrepareContext(p.Ctx, query)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
						}
						operation := func(ctx context.Context) fixture.RelationOutcome {
							args := []any{stage.document, "cat & rat", stage.options}
							if queryMode {
								if stmt != nil {
									rows, err := stmt.QueryContext(ctx, args...)
									return fixture.RelationOutcome{Rows: rows, Err: err}
								}
								rows, err := c.QueryContext(ctx, query, args...)
								return fixture.RelationOutcome{Rows: rows, Err: err}
							}
							if stmt != nil {
								result, err := stmt.ExecContext(ctx, args...)
								return fixture.RelationOutcome{Result: result, Err: err}
							}
							result, err := c.ExecContext(ctx, query, args...)
							return fixture.RelationOutcome{Result: result, Err: err}
						}
						if !b.PG {
							ctx, cancel := context.WithCancel(p.Ctx)
							cancel()
							ftsRankCanceledOutcome(t, operation(ctx), context.Canceled)
							expired, stop := context.WithDeadline(p.Ctx, time.Now().Add(-time.Second))
							ftsRankCanceledOutcome(t, operation(expired), context.DeadlineExceeded)
							stop()
						}
						// Stack sampling alone does not reserve the observed stage.
						// Native select/render cancellation additionally waits for
						// actual progress at a request-scoped host pause, so the
						// worker cannot publish before cancellation. PostgreSQL
						// proves active-statement cancellation, not stage entry.
						active := operation
						var admitted func(context.Context)
						if !b.PG && stage.point != "" {
							pause := stuntgres.NewExecutionPause(stage.point)
							defer pause.Resume()
							active = func(ctx context.Context) fixture.RelationOutcome {
								return operation(stuntgres.WithExecutionPause(ctx, pause))
							}
							admitted = func(ctx context.Context) {
								if err := pause.Wait(ctx); err != nil {
									t.Fatal("headline work checkpoint", err)
								}
							}
						}
						fixture.BinaryConversionActiveCancelAfter(t, p, b, c, query, stage.frame, admitted, active)
						if stmt != nil {
							rows, err := stmt.QueryContext(p.Ctx, "Cats rats", "cat & rat", "HighlightAll=true")
							if err != nil {
								t.Fatal(err)
							}
							ftsRankRead(t, p, b, rows, "ts_headline", "TEXT", "<b>Cats</b> <b>rats</b>", false)
						}
						ftsRankRoutes(t, p, b, c, query, "ts_headline", "TEXT", "<b>Cats</b> <b>rats</b>", "Cats rats", "cat & rat", "HighlightAll=true")
					})
				})
			}
		}
	}
}
