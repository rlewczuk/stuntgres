package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func sqlPreparedAdapter(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	q := "PREPARE phase(int) AS SELECT $1 AS value"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal("PREPARE describe", err)
	}
	defer stmt.Close()
	sqlPreparedStep(t, p, b, c, fixture.ErrorDiagnostic("EXECUTE phase(7)", sqlPreparedMissing("phase")))
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	sqlPreparedStep(t, p, b, held, fixture.Exec(q, 0))
	sqlPreparedStep(t, p, b, held, fixture.ErrorDiagnostic(q, sqlPreparedDiagnostic("42P05", `prepared statement "phase" already exists`)))
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	sqlPreparedStep(t, p, b, c, sqlPreparedScalar("EXECUTE phase(7)", "value", "INT4", int64(7)))
	q = "DEALLOCATE phase"
	stmt, err = c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal("DEALLOCATE describe", err)
	}
	defer stmt.Close()
	sqlPreparedStep(t, p, b, c, sqlPreparedScalar("EXECUTE phase(8)", "value", "INT4", int64(8)))
	sqlPreparedStep(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, fixture.Exec(q, 0))
	sqlPreparedStep(t, p, b, c, fixture.ErrorDiagnostic("EXECUTE phase(7)", sqlPreparedMissing("phase")))
}

func TestReferenceSQLPreparedUtilityPhases(t *testing.T) { sqlPreparedScenario(t, sqlPreparedAdapter) }

func TestReferenceSQLPreparedMissingUtilityDescription(t *testing.T) {
	for _, query := range []string{"EXECUTE absent", "DEALLOCATE absent", "PREPARE p AS SELECT missing", "PREPARE p(no_such_type) AS SELECT 1"} {
		t.Run(query, func(t *testing.T) {
			sqlPreparedScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal("utility preparation must not execute semantic analysis", err)
				}
				defer stmt.Close()
				d := sqlPreparedMissing("absent")
				if query == "PREPARE p AS SELECT missing" {
					d = sqlPreparedDiagnostic("42703", `column "missing" does not exist`)
					d.Position = sqlPreparedPosition(query, "missing")
				}
				if query == "PREPARE p(no_such_type) AS SELECT 1" {
					d = sqlPreparedDiagnostic("42704", `type "no_such_type" does not exist`)
					d.Position = sqlPreparedPosition(query, "no_such_type")
				}
				sqlPreparedStep(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}, fixture.ErrorDiagnostic(query, d))
			})
		})
	}
}

func TestReferenceSQLPreparedOuterParameters(t *testing.T) {
	for _, zero := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			for _, supplied := range []bool{false, true} {
				name := "nonzero"
				if zero {
					name = "zero"
				}
				if prepared {
					name += "/handle"
				} else {
					name += "/direct"
				}
				if supplied {
					name += "/args"
				} else {
					name += "/no_args"
				}
				t.Run(name, func(t *testing.T) {
					sqlPreparedScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						definition := "PREPARE p(int) AS SELECT $1 AS value"
						if zero {
							definition = "PREPARE p AS SELECT 1 AS value"
						}
						sqlPreparedStep(t, p, b, c, fixture.Exec(definition, 0))
						q := "EXECUTE p($1)"
						var executor fixture.Executor = c
						if prepared {
							stmt, err := c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal("EXECUTE utility describe", err)
							}
							defer stmt.Close()
							executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						}
						if supplied {
							rows, err := executor.QueryContext(p.Ctx, q, 7)
							if rows != nil {
								rows.Close()
								t.Fatal("outer count error exposed rows")
							}
							var native *stuntgres.Error
							var postgres *pq.Error
							if err == nil || errors.As(err, &native) || errors.As(err, &postgres) {
								t.Fatalf("outer adapter count must be ordinary: %T %v", err, err)
							}
							// Full ordinary transport wording is discovery evidence, not a fabricated SQLSTATE.
							t.Logf("outer argument boundary: %T %v", err, err)
						} else if zero {
							sqlPreparedStep(t, p, b, executor, sqlPreparedScalar(q, "value", "INT4", int64(1)))
						} else {
							d := sqlPreparedDiagnostic("42P02", "there is no parameter $1")
							d.Position = sqlPreparedPosition(q, "$1")
							sqlPreparedStep(t, p, b, executor, fixture.ErrorDiagnostic(q, d))
						}
					})
				})
			}
		}
	}
}

func TestReferenceSQLPreparedPreCanceled(t *testing.T) {
	sqlPreparedScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if b.PG {
			return
		} // Ordinary native cancellation is not a wire SQL diagnostic.
		sqlPreparedStep(t, p, b, c, fixture.Exec("PREPARE retained AS SELECT 7", 0))
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, q := range []string{"PREPARE canceled AS SELECT 1", "DEALLOCATE retained", "DEALLOCATE ALL"} {
			result, err := c.ExecContext(ctx, q)
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("precanceled mutation %q: %v %v", q, result, err)
			}
			stmt, err := c.PrepareContext(ctx, q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("precanceled Prepare exposed handle")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
		sqlPreparedStep(t, p, b, c, fixture.ErrorDiagnostic("EXECUTE canceled", sqlPreparedMissing("canceled")))
		sqlPreparedStep(t, p, b, c, sqlPreparedScalar("EXECUTE retained", "?column?", "INT4", int64(7)))
	})
}
