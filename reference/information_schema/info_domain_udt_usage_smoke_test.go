package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceInfoDomainUdtUsageFeatureSmoke(t *testing.T) {
	duScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		duOwnedSeed(run, "du_smoke")
		want := duOwnedRows(database, "du_smoke")
		where := "domain_schema='du_smoke'"
		held := cduHold(t, p, c, duProjection(b.PG, where, nil).Query)
		check := func(rows [][]any) { duCheck(t, p, c, r, held, duProjection(b.PG, where, rows)) }
		check(want)
		for _, x := range []struct {
			role string
			rows [][]any
		}{{"du_owner", want[:1]}, {"du_domain_owner", want[1:]}, {"du_reader", nil}, {"du_member", want[:1]}} {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+x.role, 0))
			check(x.rows)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Exec("GRANT USAGE ON TYPE du_smoke.e TO du_reader WITH GRANT OPTION", 0))
		run(fixture.Exec("GRANT USAGE ON DOMAIN du_smoke.p,du_smoke.c TO du_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION du_reader", 0))
		check(nil)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION du_set", 0))
		check(nil)
		run(fixture.Exec("SET ROLE du_owner", 0))
		check(want[:1])
		run(fixture.Exec("RESET ROLE", 0))
		check(nil)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		var dump bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
		}
		run(fixture.Exec("ALTER DOMAIN du_smoke.p RENAME TO renamed", 0))
		check([][]any{duRow(database, "du_smoke", "e", "du_smoke", "renamed"), duRow(database, "du_smoke", "renamed", "du_smoke", "c")})
		run(fixture.Exec("DROP DOMAIN du_smoke.c", 0))
		check([][]any{duRow(database, "du_smoke", "e", "du_smoke", "renamed")})
		run(fixture.Exec("DROP DOMAIN du_smoke.renamed", 0))
		check(nil)
		if b.PG {
			return
		} // No process-local controls exist in PostgreSQL.
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		s := duProjection(false, where, want)
		retained, err := b.DB.QueryContext(p.Ctx, s.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		columns, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		duMetadata(t, columns, false)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(duProjection(false, where, nil))
		duIdentity(t, p, b, c, r)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		run(s)
		run(fixture.Prepared(s))
		duIdentity(t, p, b, c, r)
		duRetained(t, retained, false, want)
		duMetadata(t, columns, false)
	})
}
