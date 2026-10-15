package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBContainmentCancellation(t *testing.T) {
	jsonbContainmentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT $1::jsonb @> $2::jsonb AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				if prepared {
					rows, err = stmt.QueryContext(ctx, `{}`, `{}`)
				} else {
					rows, err = c.QueryContext(ctx, q, `{}`, `{}`)
				}
				if rows != nil {
					rows.Close()
					t.Fatal("precancel published cursor")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE jbc_active(id int,doc jsonb,request jsonb,marked bool)", 0))
		// Stored nested candidates force containment work, not input conversion.
		// Every repeated RHS container can reuse the final matching LHS object;
		// the final unmatched request prevents any row from passing the predicate.
		prefix := strings.Repeat("a", 255)
		x, y, z := `{"a":"`+prefix+`x"}`, `{"a":"`+prefix+`y"}`, `{"a":"`+prefix+`z"}`
		doc := "[" + strings.Repeat(x+",", 4096) + y + "]"
		request := "[" + strings.Repeat(y+",", 512) + z + "]"
		for id := 1; id <= 16; id++ {
			run(fixture.Exec("INSERT INTO jbc_active VALUES($1,$2,$3,false)", 1, id, doc, request))
		}
		for _, activeSQL := range []string{
			"SELECT count(*) AS n FROM jbc_active WHERE doc @> request",
			"UPDATE jbc_active SET marked=true WHERE request <@ doc",
		} {
			t.Run(activeSQL, func(t *testing.T) {
				active, err := c.PrepareContext(p.Ctx, activeSQL)
				if err != nil {
					t.Fatal(err)
				}
				defer active.Close()
				fixture.BinaryConversionActiveCancel(t, p, b, c, activeSQL, "sqltype.jsonbContain", func(ctx context.Context) fixture.RelationOutcome {
					if strings.HasPrefix(activeSQL, "UPDATE") {
						result, err := active.ExecContext(ctx)
						return fixture.RelationOutcome{Result: result, Err: err}
					}
					rows, err := active.QueryContext(ctx)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbExistsSelect(q, true, `{"a":1}`, `{}`))
				run(fixture.Select("SELECT count(*) AS n FROM jbc_active WHERE marked", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				// Recover the SAME active statement without rerunning the huge scan.
				run(fixture.Exec(`UPDATE jbc_active SET doc='{}',request='{}'`, 16))
				s := fixture.Select(activeSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(16)}})
				if strings.HasPrefix(activeSQL, "UPDATE") {
					s = fixture.Exec(activeSQL, 16)
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: active, SQL: activeSQL}, b.Recorder, s)
				// Restore the workload before the next independently canceled route.
				run(fixture.Exec("UPDATE jbc_active SET doc=$1,request=$2,marked=false", 16, doc, request))
			})
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbExistsSelect(q, false, `{"a":2}`, `{"a":1}`))
	})
}
