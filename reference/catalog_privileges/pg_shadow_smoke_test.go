package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGShadowFeatureSmoke(t *testing.T) {
	pgUserScenario(t, []string{"pgs_smoke", "pgs_smoke_null", "pgs_smoke_reader"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, name := range []string{"pgs_smoke", "pgs_smoke_null", "pgs_smoke_reader"} { pgUserCreate(t, p, b, name) }
		password := pgShadowMD5; pgShadowSetPassword(t, p, b, "pgs_smoke", &password)
		for _, prepared := range []bool{false, true} { pgShadowPassword(t, p, b.DB, prepared, "pgs_smoke", &password); pgShadowPassword(t, p, b.DB, prepared, "pgs_smoke_null", nil) }
		shape := pgShadowSelect("SELECT * FROM pg_shadow WHERE FALSE", [][]any{})
		shape.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgUserMetadata(t, rows, b.PG) }
		pgUserBoth(t, p, b, shape)
		conn, err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		q := "SELECT passwd FROM pg_shadow WHERE FALSE"
		stmt, err := conn.PrepareContext(p.Ctx, q); if err != nil { t.Fatal(err) }; defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		run(fixture.Exec("SET ROLE pgs_smoke_reader", 0))
		for _, prepared := range []bool{false, true} { run(pgShadowDenied("SELECT 1 FROM pg_shadow WHERE FALSE", prepared)); run(pgShadowDenied(q, prepared)) }
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgShadowDenied(q, false))
		run(fixture.Exec("RESET ROLE", 0))
		pgUserExec(t, p, b, "GRANT pg_read_all_data TO pgs_smoke_reader")
		run(fixture.Exec("SET ROLE pgs_smoke_reader", 0))
		for _, prepared := range []bool{false, true} { pgShadowPassword(t, p, conn, prepared, "pgs_smoke", &password) }
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"passwd"}, []string{"TEXT"}, [][]any{}))
		run(fixture.Exec("RESET ROLE", 0))
		pgUserExec(t, p, b, "REVOKE pg_read_all_data FROM pgs_smoke_reader")
		run(fixture.Exec("SET ROLE pgs_smoke_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgShadowDenied(q, false))
		for _, prepared := range []bool{false, true} { run(pgShadowDenied(q, prepared)) }
		run(fixture.Exec("RESET ROLE", 0))
		pgUserBoth(t, p, b, fixture.Select("SELECT passwd FROM pg_user WHERE usename='pgs_smoke'", []string{"passwd"}, []string{"TEXT"}, [][]any{{"********"}}))
		if !b.PG {
			dump := fixture.CommentInquiryDump(t, p)
			if strings.Contains(dump, password) || strings.Contains(dump, "PASSWORD") || strings.Contains(dump, "CREATE ROLE") { t.Fatal("credential leaked into dump") }
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil { t.Fatal(err) }
			pgShadowPassword(t, p, b.DB, true, "pgs_smoke", &password)
			pgShadowPassword(t, p, b.DB, true, "pgs_smoke_null", nil)
		}
		for _, name := range []string{"pgs_smoke", "pgs_smoke_null", "pgs_smoke_reader"} { pgUserExec(t, p, b, "DROP ROLE "+name) }
	})
}
