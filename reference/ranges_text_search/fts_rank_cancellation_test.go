package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSRankCancellation(t *testing.T) {
	var vector, operands strings.Builder
	// Retained positions per lexeme stay below the existing 256-position
	// limit. Pair work is genuine rank work, not a pg_sleep surrogate.
	for word := range 256 {
		if word != 0 {
			vector.WriteByte(' ')
			operands.WriteString(" & ")
		}
		lexeme := fmt.Sprintf("w%03d", word)
		vector.WriteString(lexeme + ":")
		operands.WriteString(lexeme)
		for position := 1; position <= 128; position++ {
			if position > 1 {
				vector.WriteByte(',')
			}
			fmt.Fprint(&vector, position)
		}
	}
	for _, name := range ftsRankNames {
		for _, prepared := range []bool{false, true} {
			for _, queryMode := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s_prepared_%t_query_%t", name, prepared, queryMode), func(t *testing.T) {
					ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						query := "SELECT " + name + "($1::tsvector,$2::tsquery)"
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
							args := []any{vector.String(), operands.String()}
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
							canceled, cancel := context.WithCancel(p.Ctx)
							cancel()
							ftsRankCanceledOutcome(t, operation(canceled), context.Canceled)
							expired, stop := context.WithDeadline(p.Ctx, time.Now().Add(-time.Second))
							ftsRankCanceledOutcome(t, operation(expired), context.DeadlineExceeded)
							stop()
						}
						// Existing public fixture observes the actual provider frame
						// natively and pg_stat_activity on PG, then cancels and joins.
						// No new admission API, timing sleep, or fake rank work.
						fixture.BinaryConversionActiveCancelAfter(t, p, b, c, query, ".Rank", nil, operation)
						want := ftsRankExact(.06079271)
						if name == "ts_rank_cd" {
							want = ftsRankExact(.1)
						}
						if stmt != nil {
							rows, err := stmt.QueryContext(p.Ctx, "cat:1", "cat")
							if err != nil {
								t.Fatal(err)
							}
							ftsRankRead(t, p, b, rows, name, "FLOAT4", want, false)
						}
						ftsRankRoutes(t, p, b, c, query, name, "FLOAT4", want, "cat:1", "cat")
					})
				})
			}
		}
	}
}

func ftsRankCanceledOutcome(t *testing.T, outcome fixture.RelationOutcome, want error) {
	t.Helper()
	if outcome.Rows != nil {
		outcome.Rows.Close()
		t.Fatal("canceled call published rows")
	}
	if outcome.Result != nil {
		t.Fatal("canceled call published result")
	}
	fixture.LexicalOrdinary(t, outcome.Err, want)
}
