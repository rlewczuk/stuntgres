package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoForeignServersDiagnostics(t *testing.T) {
	fsvScenario(t, func(t *testing.T, c *fsvCase) {
		c.seed()
		held := c.hold(c.conn, fsvQuery)
		initial := c.seedRows()
		unchanged := func() {
			c.check(held, "fsv_text", initial)
		}
		for _, tc := range []struct{ query, state, message string }{
			{"ALTER SERVER fsv_text VERSION 'wrong' OPTIONS(ADD password 'duplicate')", "42710", `option "password" provided more than once`},
			{"ALTER SERVER fsv_text VERSION 'wrong' OPTIONS(SET absent 'wrong')", "42704", `option "absent" not found`},
			{"ALTER SERVER fsv_text VERSION 'wrong' OPTIONS(DROP absent)", "42704", `option "absent" not found`},
			{"ALTER SERVER fsv_text VERSION 'wrong' OPTIONS(ADD \"x=y\" 'wrong')", "22023", `invalid option name "x=y": must not contain "="`},
			{"ALTER SERVER fsv_absent VERSION 'wrong'", "42704", `server "fsv_absent" does not exist`},
		} {
			d := fixture.ControlsReferenceDiagnostic(tc.state, tc.message)
			s := fixture.ErrorDiagnostic(tc.query, d)
			s.ErrorStage = "execute"
			c.run(c.conn, s)
			unchanged()
			c.run(c.conn, fixture.Prepared(s))
			unchanged()
			mutation := c.hold(c.conn, tc.query)
			c.run(mutation, s)
			if err := mutation.Statement.Close(); err != nil {
				t.Fatal(err)
			}
			unchanged()
		}
		c.exec("CREATE ROLE fsv_denied")
		denied := fixture.ErrorDiagnostic("ALTER SERVER fsv_text VERSION 'denied'", fixture.ControlsReferenceDiagnostic("42501", "must be owner of foreign server fsv_text"))
		denied.ErrorStage = "execute"
		mutation := c.hold(c.conn, denied.Query)
		for _, e := range []fixture.Executor{c.conn, mutation} {
			c.exec("SET SESSION AUTHORIZATION fsv_denied")
			c.run(e, denied)
			c.run(c.conn, fixture.Prepared(denied))
			c.exec("RESET SESSION AUTHORIZATION")
			unchanged()
		}
		if !c.b.PG {
			ctx, cancel := context.WithCancel(c.p.Ctx)
			cancel()
			rows, err := c.conn.QueryContext(ctx, fsvQuery, "fsv_text")
			if rows != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-canceled rows=%v error=%v", rows, err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("ordinary cancellation acquired SQLSTATE", err)
			}
			c.check(held, "fsv_text", initial)
		}
	})
}
