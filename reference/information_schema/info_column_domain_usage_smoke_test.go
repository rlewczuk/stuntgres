package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnDomainUsageFeatureSmoke(t *testing.T) {
	cduScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		cduSeed(run)
		where := "table_schema='cdu'"
		want := cduSeedRows(database)
		held := cduHold(t, p, c, cduProjection(b.PG, where, nil).Query)
		check := func(rows [][]any) { cduCheck(t, p, c, r, held, cduProjection(b.PG, where, rows)) }
		check(want)
		run(fixture.Exec("ALTER TABLE cdu.t ADD extra cdu.parent", 0))
		check(append(cduSeedRows(database), cduRow(database, "cdu", "parent", "cdu", "t", "extra")))
		run(fixture.Exec("ALTER TABLE cdu.t ALTER COLUMN extra TYPE integer", 0))
		check(want)
		nullStep := cduProjection(b.PG, "table_schema='cdu' AND domain_name=$1", nil)
		nullStep.Args = []any{nil}
		cduCheck(t, p, c, r, cduHold(t, p, c, nullStep.Query), nullStep)
		run(fixture.Exec("ALTER DOMAIN cdu.child RENAME TO renamed_child", 0))
		want = [][]any{want[0], cduRow(database, "cdu", "renamed_child", "cdu", "t", "b"), want[2], cduRow(database, "cdu", "renamed_child", "cdu", "v", "b")}
		check(want)
		// Capture the serializable bootstrap-owned definition before the ACL
		// visibility phase, rather than bypassing Dump's ACL restrictions.
		var dump bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
		}
		for _, q := range []string{
			"CREATE ROLE cdu_reader", "CREATE ROLE cdu_owner",
			"GRANT USAGE ON SCHEMA cdu TO cdu_reader",
			"GRANT SELECT ON cdu.t,cdu.v TO cdu_reader",
			"GRANT USAGE ON DOMAIN cdu.parent,cdu.renamed_child TO PUBLIC,cdu_reader",
			"ALTER DOMAIN cdu.parent OWNER TO cdu_owner", "ALTER DOMAIN cdu.renamed_child OWNER TO cdu_owner",
			"GRANT cdu_owner TO cdu_reader WITH INHERIT FALSE, SET TRUE",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION cdu_reader", 0))
		check(nil)
		run(fixture.Exec("SET ROLE cdu_owner", 0))
		check(want)
		run(fixture.Exec("RESET ROLE", 0))
		check(nil)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		check(want)
		cduMissing(run)
		if b.PG {
			return
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(cduProjection(false, where, nil))
		run(cduProjection(false, "table_schema='information_schema' AND table_name='column_domain_usage'", cduSelfRows(database)))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		s := cduProjection(false, where, want)
		run(s)
		run(fixture.Prepared(s))
		cduIdentity(t, p, b, c, r)
		run(cduProjection(false, "table_schema='information_schema' AND table_name='column_domain_usage'", cduSelfRows(database)))
	})
}
