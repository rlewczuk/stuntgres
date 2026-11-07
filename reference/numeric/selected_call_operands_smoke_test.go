package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSelectedCallOperandsFeatureSmoke(t *testing.T) {
	selectedCallScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		selectedCallCreateUsers(t, p, b, c)
		before := selectedCallStamps(t, p, b, c)
		// The exact motivating UPDATE must affect two rows, and each stored
		// stamp must be a fresh independent lowercase 32-hex digest.
		selectedCallRun(t, p, b, c, fixture.Exec("UPDATE users SET security_stamp = md5(random()::text || id::text) WHERE tenant_id = 1", 2))
		selectedCallChangedStamps(t, before, selectedCallStamps(t, p, b, c), 1, 2)
		q := "UPDATE users SET security_stamp = md5(random()::text || id::text) WHERE tenant_id = $1"
		selectedCallHeld(t, p, b, c, q, func(e fixture.Executor) {
			for _, tc := range []struct {
				tenant, count int64
				changed       []int
			}{{1, 2, []int{1, 2}}, {2, 1, []int{3}}, {99, 0, nil}, {1, 2, []int{1, 2}}} {
				before := selectedCallStamps(t, p, b, c)
				selectedCallRun(t, p, b, e, fixture.Exec(q, tc.count, tc.tenant))
				selectedCallChangedStamps(t, before, selectedCallStamps(t, p, b, c), tc.changed...)
			}
		})
		selectedCallBoth(t, p, b, c, fixture.Select("SELECT random()::text IS NOT NULL,abs(-1)::text,1+abs(-1),round(2.5)::int,floor(random()*10)>=0,CAST(pg_catalog.abs(-1) AS text)",
			[]string{"?column?", "abs", "?column?", "round", "?column?", "abs"},
			[]string{"BOOL", "TEXT", "INT4", "INT4", "BOOL", "TEXT"},
			[][]any{{true, "1", int64(2), int64(3), true, "1"}}))
		selectedCallBoth(t, p, b, c, fixture.Select("SELECT id,abs(id)*2,abs(id)::text FROM users WHERE abs(id)+0=1 ORDER BY id", []string{"id", "?column?", "abs"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(2), "1"}}))
	})
}
