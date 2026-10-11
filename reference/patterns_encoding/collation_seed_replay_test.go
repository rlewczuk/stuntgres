package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCollationSeedNativeReplay(t *testing.T) {
	dsn := "reference/" + t.Name()
	fixture.RoleReferenceCleanup(t, []string{"coll_seed_delta_owner"})
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.SchemaReferenceCleanup(t, "coll_seed_delta")
		fixture.CollationExec(t, p, b, `CREATE USER coll_seed_delta_owner`, `CREATE SCHEMA coll_seed_delta AUTHORIZATION coll_seed_delta_owner`)
		if b.PG {
			fixture.CollationExec(t, p, b, `ALTER ROLE coll_seed_delta_owner PASSWORD 'role-core-fixture'`)
		}
		actor := fixture.RoleScenarioLoginPool(t, p.NativeConnector.Driver().(*stuntgres.Driver), b.PG, dsn, "coll_seed_delta_owner")
		result, err := actor.ExecContext(p.Ctx, `CREATE COLLATION coll_seed_delta.coll_seed_delta_owner_probe FROM pg_catalog."C"`)
		fixture.DeleteReferenceCount(t, result, err, 0)
		if err = actor.Close(); err != nil {
			t.Fatal(err)
		}
		owner := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, owner, b.Recorder, s) }
		exec := func(queries ...string) {
			t.Helper()
			for _, q := range queries {
				run(fixture.Exec(q, 0))
			}
		}
		var bootstrap string
		if err = owner.QueryRowContext(p.Ctx, `SELECT current_user`).Scan(&bootstrap); err != nil {
			t.Fatal(err)
		}
		quotedBootstrap := `"` + strings.ReplaceAll(bootstrap, `"`, `""`) + `"`
		repair := func(conn *sql.Conn) {
			for _, seed := range []struct{ canonical, current string }{{`"C"`, "coll_seed_c_delta"}, {"pg_unicode_fast", "coll_seed_full_delta"}} {
				for _, q := range []string{`ALTER COLLATION coll_seed_delta.` + seed.current + ` OWNER TO ` + quotedBootstrap, `ALTER COLLATION coll_seed_delta.` + seed.current + ` SET SCHEMA pg_catalog`, `ALTER COLLATION pg_catalog.` + seed.current + ` RENAME TO ` + seed.canonical} {
					_, _ = conn.ExecContext(p.Ctx, q)
				}
			}
		}
		if b.PG {
			exec(`BEGIN`)
			defer owner.ExecContext(p.Ctx, `ROLLBACK`)
		} else {
			defer repair(owner)
		}
		exec(`ALTER COLLATION pg_catalog."C" RENAME TO coll_seed_c_delta`, `ALTER COLLATION pg_catalog.coll_seed_c_delta SET SCHEMA coll_seed_delta`, `ALTER COLLATION coll_seed_delta.coll_seed_c_delta OWNER TO coll_seed_delta_owner`, `ALTER COLLATION pg_catalog.pg_unicode_fast RENAME TO coll_seed_full_delta`, `ALTER COLLATION pg_catalog.coll_seed_full_delta SET SCHEMA coll_seed_delta`, `ALTER COLLATION coll_seed_delta.coll_seed_full_delta OWNER TO coll_seed_delta_owner`)
		exec(`CREATE COLLATION coll_seed_copy_c FROM coll_seed_delta.coll_seed_c_delta`, `CREATE COLLATION coll_seed_copy_full FROM coll_seed_delta.coll_seed_full_delta`, `CREATE DOMAIN coll_seed_domain AS TEXT COLLATE coll_seed_delta.coll_seed_c_delta`, `CREATE TABLE coll_seed_t(id INT,v coll_seed_domain DEFAULT ('a' COLLATE coll_seed_delta.coll_seed_c_delta),CHECK(v COLLATE coll_seed_delta.coll_seed_c_delta > ''))`, `CREATE INDEX coll_seed_ix ON coll_seed_t(v COLLATE coll_seed_delta.coll_seed_c_delta)`, `CREATE VIEW coll_seed_v AS SELECT id,upper(v COLLATE coll_seed_delta.coll_seed_full_delta) AS v FROM coll_seed_t`)
		run(fixture.Exec(`INSERT INTO coll_seed_t VALUES(1,'ß'),(2,'é')`, 2))
		metadata := fixture.Select(`SELECT oid::bigint AS oid,collname,collprovider,collencoding,collversion,collowner=(SELECT collowner FROM pg_catalog.pg_collation WHERE collname='coll_seed_delta_owner_probe') AS owned,pg_collation_is_visible(oid) AS visible FROM pg_catalog.pg_collation WHERE oid=950 OR oid=6411 ORDER BY oid`, []string{"oid", "collname", "collprovider", "collencoding", "collversion", "owned", "visible"}, []string{"INT8", "NAME", "CHAR", "INT4", "TEXT", "BOOL", "BOOL"}, [][]any{{int64(950), "coll_seed_c_delta", "c", int64(-1), nil, true, false}, {int64(6411), "coll_seed_full_delta", "b", int64(6), "1", true, false}})
		copies := fixture.Select(`SELECT collname,collprovider,collencoding,collversion FROM pg_catalog.pg_collation WHERE collname='coll_seed_copy_c' OR collname='coll_seed_copy_full' ORDER BY collname`, []string{"collname", "collprovider", "collencoding", "collversion"}, []string{"NAME", "CHAR", "INT4", "TEXT"}, [][]any{{"coll_seed_copy_c", "c", int64(-1), nil}, {"coll_seed_copy_full", "b", int64(6), "1"}})
		values := fixture.Select(`SELECT id,v FROM coll_seed_v ORDER BY id`, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "SS"}, {int64(2), "É"}})
		for _, s := range []fixture.Step{metadata, copies, values, fixture.Prepared(values)} {
			run(s)
		}
		if b.PG {
			exec(`ROLLBACK`)
			return
		}
		var dump bytes.Buffer
		if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "ALTER COLLATION") || !strings.Contains(dump.String(), "coll_seed_c_delta") {
			t.Fatal("missing bootstrap metadata deltas", dump.String())
		}
		if p.Postgres != nil {
			pgConn := fixture.SequenceCoreConn(t, p.Ctx, p.Postgres)
			if _, err = pgConn.ExecContext(p.Ctx, `CREATE USER coll_seed_delta_owner`); err != nil {
				t.Fatal(err)
			}
			if _, err = pgConn.ExecContext(p.Ctx, `BEGIN`); err != nil {
				t.Fatal(err)
			}
			defer pgConn.ExecContext(p.Ctx, `ROLLBACK`)
			if _, err = pgConn.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("emitted mutable-seed PostgreSQL replay", err)
			}
			for _, s := range []fixture.Step{metadata, copies, values, fixture.Prepared(values)} {
				fixture.RunStep(t, p.Ctx, pgConn, p.PostgresRecord, s)
			}
			if _, err = pgConn.ExecContext(p.Ctx, `ROLLBACK`); err != nil {
				t.Fatal(err)
			}
			if _, err = pgConn.ExecContext(p.Ctx, `DROP ROLE coll_seed_delta_owner`); err != nil {
				t.Fatal(err)
			}
		}
		for range 2 {
			if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select(`SELECT collname,pg_collation_is_visible(oid) AS visible FROM pg_catalog.pg_collation WHERE oid=950 OR oid=6411 ORDER BY oid`, []string{"collname", "visible"}, []string{"NAME", "BOOL"}, [][]any{{"C", true}, {"pg_unicode_fast", true}}))
			if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			for _, s := range []fixture.Step{metadata, copies, values, fixture.Prepared(values)} {
				run(s)
			}
		}
		fresh := fixture.UpdateReturningNativeDestination(t)
		if _, err = fresh.ExecContext(p.Ctx, `CREATE USER coll_seed_delta_owner`); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		for _, s := range []fixture.Step{metadata, copies, values} {
			rows, queryErr := fresh.QueryContext(p.Ctx, s.Query)
			fixture.DeleteReferenceConsume(t, false, rows, queryErr, s)
		}
		freshOwner := fixture.SequenceCoreConn(t, p.Ctx, fresh)
		repair(freshOwner)
	})
}
