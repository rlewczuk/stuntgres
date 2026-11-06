package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgPrewarmFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pw_smoke_reader"})
	pwScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pwCacheInstall(t, p, b, c)
		pwRoutes(t, p, b, c, pwCount("SELECT pw_part.pg_prewarm('pw_part.heap') AS warmed", 0))
		pwBodyError(t, p, b, c, pwError(pwCall, "22023", "starting block number must be between 0 and -1", "pw_part.heap", "buffer", "main", int64(0), nil))
		pwInsert(t, p, b, c, "pw_part.heap", 1)
		st, err := c.PrepareContext(p.Ctx, pwCall)
		if err != nil { t.Fatal(err) }
		defer st.Close()
		h := fixture.HeldStatement{Executor: c, Statement: st, SQL: pwCall}
		for _, mode := range []string{"buffer", "buffer", "read", "prefetch"} {
			fixture.RunStep(t, p.Ctx, h, b.Recorder, pwCount(pwCall, 1, "pw_part.heap", mode, "main", int64(0), int64(0)))
			if mode == "buffer" || !b.PG { pwResident(t, p, b, c, "pw_part.heap", 0, 0, 0) }
		}
		s := pwError(pwCall, "22023", "invalid prewarm type", "pw_part.heap", "BAD", "main", nil, nil)
		s.Diagnostic.Hint = "Valid prewarm types are \"prefetch\", \"read\", and \"buffer\"."
		pwBodyError(t, p, b, c, s)
		bcDDL(t, p, b, c, "CREATE ROLE pw_smoke_reader", "GRANT USAGE ON SCHEMA pw_part TO pw_smoke_reader", "CREATE VIEW pw_part.smoke_view AS SELECT pw_part.pg_prewarm('pw_part.heap'::regclass) AS warmed", "SET SESSION AUTHORIZATION pw_smoke_reader")
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(t, p.Ctx, h, b.Recorder, pwError(pwCall, "42501", "permission denied for table heap", "pw_part.heap", "buffer", "main", nil, nil))
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON pw_part.heap TO pw_smoke_reader", "SET SESSION AUTHORIZATION pw_smoke_reader")
		fixture.RunStep(t, p.Ctx, h, b.Recorder, pwCount(pwCall, 1, "pw_part.heap", "buffer", "main", nil, nil))
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION")
		pwRoutes(t, p, b, c, pwViewCount("SELECT warmed FROM pw_part.smoke_view", 1, b.PG))
		if b.PG { return }
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_prewarm'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil { t.Fatal(err) }
		if len(pwCoordinates(t, p, b, c, "pw_part.heap")) != 0 { t.Fatal("restored cache must start cold") }
		fixture.RunStep(t, p.Ctx, h, b.Recorder, pwCount(pwCall, 1, "pw_part.heap", "buffer", "main", nil, nil))
		pwResident(t, p, b, c, "pw_part.heap", 0, 0, 0)
		pwRoutes(t, p, b, c, pwViewCount("SELECT warmed FROM pw_part.smoke_view", 1, false))
	})
}
