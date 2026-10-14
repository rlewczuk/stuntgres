package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBExistsCancellation(t *testing.T) {
	jsonbExistsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT $1::jsonb ?| $2 AS v"
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
					rows, err = stmt.QueryContext(ctx, `{"a":1}`, "{a}")
				} else {
					rows, err = c.QueryContext(ctx, q, `{"a":1}`, "{a}")
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
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbExistsSelect(q, true, `{"a":1}`, "{a}"))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jbe_active(id int,doc jsonb,keys text[])", 0))
		// Stored operands force work inside existence rather than input parsing.
		// Equal-length long prefixes defeat cheap length mismatches; every request
		// must scan all immediate strings, and no request matches.
		prefix := strings.Repeat("a", 255)
		doc := "[" + strings.TrimSuffix(strings.Repeat(`"`+prefix+`x",`, 4096), ",") + "]"
		keys := "{" + strings.TrimSuffix(strings.Repeat(prefix+"y,", 512), ",") + "}"
		for id := 1; id <= 16; id++ {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO jbe_active VALUES($1,$2,$3)", 1, id, doc, keys))
		}
		activeSQL := "SELECT count(*) AS n FROM jbe_active WHERE doc ?| keys"
		active, err := c.PrepareContext(p.Ctx, activeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer active.Close()
		fixture.BinaryConversionActiveCancel(t, p, b, c, activeSQL, "sqltype.jsonbExists", func(ctx context.Context) fixture.RelationOutcome {
			rows, err := active.QueryContext(ctx)
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		// SAME physical owner and both preheld consumers recover after joined work.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`UPDATE jbe_active SET doc='["a"]',keys='{a}'`, 16))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: active, SQL: activeSQL}, b.Recorder, fixture.Select(activeSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(16)}}))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbExistsSelect(q, false, `{"outer":{"a":1}}`, "{a}"))
	})
}
