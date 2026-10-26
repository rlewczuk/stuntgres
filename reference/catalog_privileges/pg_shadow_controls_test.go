package reference_test

import (
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGShadowControls(t *testing.T) {
	var dump string
	var pair *fixture.Pair
	var postgres fixture.Backend
	pgUserScenario(t, []string{"pgs_controls"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pair = p; if b.PG { postgres = b }
		id := pgUserCreate(t, p, b, "pgs_controls")
		password := pgShadowSCRAM(t); pgShadowSetPassword(t, p, b, "pgs_controls", &password)
		pgUserExec(t, p, b, "ALTER ROLE pgs_controls CREATEDB VALID UNTIL '-infinity'")
		pgUserExec(t, p, b, "CREATE TABLE pgs_control_sentinel(a INT)")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgs_control_sentinel VALUES(7)", 1))
		pgUserExec(t, p, b, "CREATE VIEW pgs_control_view AS SELECT usename,usecreatedb FROM pg_catalog.pg_shadow WHERE usename='pgs_controls'")
		check := func() {
			t.Helper()
			for _, prepared := range []bool{false, true} { pgShadowPassword(t, p, b.DB, prepared, "pgs_controls", &password) }
			pgUserBoth(t, p, b, fixture.Select("SELECT usesysid,usecreatedb,valuntil::text AS expiry,useconfig FROM pg_shadow WHERE usename='pgs_controls'", []string{"usesysid", "usecreatedb", "expiry", "useconfig"}, []string{"OID", "BOOL", "TEXT", "_TEXT"}, [][]any{{id, true, "-infinity", nil}}))
			pgUserBoth(t, p, b, fixture.Select("SELECT * FROM pgs_control_view", []string{"usename", "usecreatedb"}, []string{"NAME", "BOOL"}, [][]any{{"pgs_controls", true}}))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT a FROM pgs_control_sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
		check()
		if !b.PG {
			old, err := b.DB.QueryContext(p.Ctx, "SELECT usename,passwd,valuntil::text AS expiry,usecreatedb FROM pg_shadow WHERE usename='pgs_controls'"); if err != nil { t.Fatal(err) }; defer old.Close()
			dump = fixture.CommentInquiryDump(t, p)
			for _, forbidden := range []string{password, "SCRAM-SHA-256$", "CREATE ROLE", "PASSWORD", "INSERT INTO \"pg_catalog\".\"pg_shadow\""} { if strings.Contains(dump, forbidden) { t.Fatal("Dump leaked shared credential state") } }
			for _, suffix := range []string{"CREATE ROLE pgs_restore_forbidden;", "INSERT INTO pgs_control_sentinel VALUES('bad');"} {
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+suffix)); err == nil { t.Fatal("invalid staged Restore succeeded") }
				check()
				if fixture.CommentInquiryDump(t, p) != dump { t.Fatal("failed Restore changed catalog") }
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
			pgShadowPassword(t, p, b.DB, false, "pgs_controls", &password)
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil { t.Fatal(err) }
			check()
			pgShadowRetained(t, old, "pgs_controls", password, "-infinity", true)
		}
	})
	if dump != "" && postgres.DB != nil {
		for _, sql := range []string{"DROP VIEW pgs_control_view", "DROP TABLE pgs_control_sentinel", dump} { if _, err := postgres.DB.ExecContext(pair.Ctx, sql); err != nil { t.Fatal("oracle rejected emitted Dump SQL") } }
		fixture.RunStep(t, pair.Ctx, postgres.DB, postgres.Recorder, fixture.Select("SELECT a FROM pgs_control_sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
	}
}
