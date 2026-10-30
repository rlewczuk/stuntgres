package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoForeignDataWrappersDiagnostics(t *testing.T) {
	fdwvScenario(t, func(t *testing.T, c *fdwvCase) {
		c.seed()
		c.exec("CREATE ROLE fdwview_newowner")
		held := c.hold(c.conn, fdwvQuery)
		initial := c.seedRows()
		unchanged := func() {
			c.check(held, "fdwview_wrapper", initial)
			// The selected five fields intentionally cannot reveal an option
			// replacement. This existing sibling is an atomicity witness only.
			c.run(c.conn, fixture.Select("SELECT option_value FROM information_schema.foreign_data_wrapper_options WHERE foreign_data_wrapper_name='fdwview_wrapper' AND option_name='remote'", []string{"option_value"}, []string{"VARCHAR"}, [][]any{{"é=🙂"}}))
		}
		for _, tc := range []struct{ query, state, message, hint string }{
			{"ALTER FOREIGN DATA WRAPPER fdwview_wrapper OPTIONS(SET remote 'wrong', ADD password 'duplicate')", "42710", `option "password" provided more than once`, ""},
			{"ALTER FOREIGN DATA WRAPPER fdwview_wrapper OPTIONS(SET absent 'wrong')", "42704", `option "absent" not found`, ""},
			{"ALTER FOREIGN DATA WRAPPER fdwview_wrapper OPTIONS(DROP absent)", "42704", `option "absent" not found`, ""},
			{"ALTER FOREIGN DATA WRAPPER fdwview_wrapper OPTIONS(ADD \"x=y\" 'wrong')", "22023", `invalid option name "x=y": must not contain "="`, ""},
			{"ALTER FOREIGN DATA WRAPPER fdwview_wrapper OWNER TO fdwview_newowner", "42501", `permission denied to change owner of foreign-data wrapper "fdwview_wrapper"`, "The owner of a foreign-data wrapper must be a superuser."},
		} {
			d := fixture.ControlsReferenceDiagnostic(tc.state, tc.message)
			d.Hint = tc.hint
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
		if !c.b.PG {
			ctx, cancel := context.WithCancel(c.p.Ctx)
			cancel()
			rows, err := c.conn.QueryContext(ctx, fdwvQuery, "fdwview_wrapper")
			if rows != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-canceled rows=%v error=%v", rows, err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("ordinary cancellation acquired SQLSTATE", err)
			}
			c.check(held, "fdwview_wrapper", initial)
		}
	})
}
