package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSQueryFunctionsCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, queryMode := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared_%t_query_%t", prepared, queryMode), func(t *testing.T) {
				ftsQueryFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					// Scalar pg_sleep/IS NOT NULL is an existing expression path;
					// cancellation occurs while fetching the inner rule predicate.
					query := "SELECT ts_rewrite('a'::tsquery,'SELECT ''a''::tsquery,''b''::tsquery WHERE pg_sleep(600) IS NOT NULL')"
					var stmt *sql.Stmt
					if prepared {
						stmt, err = conn.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					operation := func(ctx context.Context) fixture.RelationOutcome {
						if queryMode {
							if stmt != nil {
								rows, err := stmt.QueryContext(ctx)
								return fixture.RelationOutcome{Rows: rows, Err: err}
							}
							rows, err := conn.QueryContext(ctx, query)
							return fixture.RelationOutcome{Rows: rows, Err: err}
						}
						if stmt != nil {
							result, err := stmt.ExecContext(ctx)
							return fixture.RelationOutcome{Result: result, Err: err}
						}
						result, err := conn.ExecContext(ctx, query)
						return fixture.RelationOutcome{Result: result, Err: err}
					}
					// Native pre-admission cancellation is an ordinary context error.
					// lib/pq's transport race is not the PostgreSQL SQL-error contract.
					if !b.PG {
						ctx, cancel := context.WithCancel(p.Ctx)
						cancel()
						outcome := operation(ctx)
						if outcome.Rows != nil {
							outcome.Rows.Close()
							t.Fatal("pre-canceled native Query published rows")
						}
						if outcome.Result != nil {
							t.Fatal("pre-canceled native Exec published a result")
						}
						fixture.LexicalOrdinary(t, outcome.Err, context.Canceled)
					}
					// Observe the actual nested sleep before canceling. The shared
					// fixture requires no published cursor/result, native context.Canceled,
					// and the full PostgreSQL 57014 record with absent optional fields.
					fixture.SleepActiveCancel(t, p, b, conn, query, nil, operation)
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT ts_rewrite('a'::tsquery,'VALUES (''a''::tsquery,''b''::tsquery)') AS rewritten", []string{"rewritten"}, []string{"TSQUERY"}, [][]any{{"'b'"}}))
				})
			})
		}
	}
}
