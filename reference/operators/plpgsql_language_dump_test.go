package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func plpgsqlLanguageDump(t *testing.T) {
	t.Helper()
	t.Run("altered_language_dump", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "GRANT USAGE ON LANGUAGE plpgsql TO PUBLIC"); err != nil {
					t.Error(err)
				}
			}()
			userOperatorExec(t, p, b, c, "REVOKE USAGE ON LANGUAGE plpgsql FROM PUBLIC")
			plpgsqlValue(t, p, b, c, "SELECT lanacl IS NOT NULL AS v FROM pg_language WHERE lanname='plpgsql'", "BOOL", true)
			if !b.PG {
				var w plpgsqlWriteProbe
				err := stuntgres.Dump(p.Ctx, p.Native, &w)
				if err == nil || !strings.Contains(err.Error(), "language") || w.calls != 0 {
					t.Fatalf("altered language authority must reject before output: err=%v writes=%d", err, w.calls)
				}
			}
		})
	})
}

type plpgsqlWriteProbe struct{ calls int }

func (w *plpgsqlWriteProbe) Write(p []byte) (int, error) {
	w.calls++
	return len(p), nil
}
