package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceCollationSeedNativeReplay(t *testing.T) {
	dsn := "reference/" + t.Name()
	roleReferenceCleanup(t, []string{"coll_seed_delta_owner"})
	collationScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		schemaReferenceCleanup(t, "coll_seed_delta")
		collationExec(t, p, b, `CREATE USER coll_seed_delta_owner`, `CREATE SCHEMA coll_seed_delta AUTHORIZATION coll_seed_delta_owner`)
		if b.pg {
			collationExec(t, p, b, `ALTER ROLE coll_seed_delta_owner PASSWORD 'role-core-fixture'`)
		}
		actor := roleScenarioLoginPool(t, p.nativeConnector.Driver().(*stuntgres.Driver), b.pg, dsn, "coll_seed_delta_owner")
		result, err := actor.ExecContext(p.ctx, `CREATE COLLATION coll_seed_delta.coll_seed_delta_owner_probe FROM pg_catalog."C"`)
		deleteReferenceCount(t, result, err, 0)
		if err = actor.Close(); err != nil {
			t.Fatal(err)
		}
		owner := sequenceCoreConn(t, p.ctx, b.db)
		run := func(s referenceStep) { t.Helper(); referenceRunStep(t, p.ctx, owner, b.recorder, s) }
		exec := func(queries ...string) {
			t.Helper()
			for _, q := range queries {
				run(referenceExec(q, 0))
			}
		}
		var bootstrap string
		if err = owner.QueryRowContext(p.ctx, `SELECT current_user`).Scan(&bootstrap); err != nil {
			t.Fatal(err)
		}
		quotedBootstrap := `"` + strings.ReplaceAll(bootstrap, `"`, `""`) + `"`
		repair := func(conn *sql.Conn) {
			for _, seed := range []struct{ canonical, current string }{{`"C"`, "coll_seed_c_delta"}, {"pg_unicode_fast", "coll_seed_full_delta"}} {
				for _, q := range []string{`ALTER COLLATION coll_seed_delta.` + seed.current + ` OWNER TO ` + quotedBootstrap, `ALTER COLLATION coll_seed_delta.` + seed.current + ` SET SCHEMA pg_catalog`, `ALTER COLLATION pg_catalog.` + seed.current + ` RENAME TO ` + seed.canonical} {
					_, _ = conn.ExecContext(p.ctx, q)
				}
			}
		}
		if b.pg {
			exec(`BEGIN`)
			defer owner.ExecContext(p.ctx, `ROLLBACK`)
		} else {
			defer repair(owner)
		}
		exec(`ALTER COLLATION pg_catalog."C" RENAME TO coll_seed_c_delta`, `ALTER COLLATION pg_catalog.coll_seed_c_delta SET SCHEMA coll_seed_delta`, `ALTER COLLATION coll_seed_delta.coll_seed_c_delta OWNER TO coll_seed_delta_owner`, `ALTER COLLATION pg_catalog.pg_unicode_fast RENAME TO coll_seed_full_delta`, `ALTER COLLATION pg_catalog.coll_seed_full_delta SET SCHEMA coll_seed_delta`, `ALTER COLLATION coll_seed_delta.coll_seed_full_delta OWNER TO coll_seed_delta_owner`)
		exec(`CREATE COLLATION coll_seed_copy_c FROM coll_seed_delta.coll_seed_c_delta`, `CREATE COLLATION coll_seed_copy_full FROM coll_seed_delta.coll_seed_full_delta`, `CREATE DOMAIN coll_seed_domain AS TEXT COLLATE coll_seed_delta.coll_seed_c_delta`, `CREATE TABLE coll_seed_t(id INT,v coll_seed_domain DEFAULT ('a' COLLATE coll_seed_delta.coll_seed_c_delta),CHECK(v COLLATE coll_seed_delta.coll_seed_c_delta > ''))`, `CREATE INDEX coll_seed_ix ON coll_seed_t(v COLLATE coll_seed_delta.coll_seed_c_delta)`, `CREATE VIEW coll_seed_v AS SELECT id,upper(v COLLATE coll_seed_delta.coll_seed_full_delta) AS v FROM coll_seed_t`)
		run(referenceExec(`INSERT INTO coll_seed_t VALUES(1,'ß'),(2,'é')`, 2))
		metadata := referenceSelect(`SELECT oid::bigint AS oid,collname,collprovider,collencoding,collversion,collowner=(SELECT collowner FROM pg_catalog.pg_collation WHERE collname='coll_seed_delta_owner_probe') AS owned,pg_collation_is_visible(oid) AS visible FROM pg_catalog.pg_collation WHERE oid=950 OR oid=6411 ORDER BY oid`, []string{"oid", "collname", "collprovider", "collencoding", "collversion", "owned", "visible"}, []string{"INT8", "NAME", "CHAR", "INT4", "TEXT", "BOOL", "BOOL"}, [][]any{{int64(950), "coll_seed_c_delta", "c", int64(-1), nil, true, false}, {int64(6411), "coll_seed_full_delta", "b", int64(6), "1", true, false}})
		copies := referenceSelect(`SELECT collname,collprovider,collencoding,collversion FROM pg_catalog.pg_collation WHERE collname='coll_seed_copy_c' OR collname='coll_seed_copy_full' ORDER BY collname`, []string{"collname", "collprovider", "collencoding", "collversion"}, []string{"NAME", "CHAR", "INT4", "TEXT"}, [][]any{{"coll_seed_copy_c", "c", int64(-1), nil}, {"coll_seed_copy_full", "b", int64(6), "1"}})
		values := referenceSelect(`SELECT id,v FROM coll_seed_v ORDER BY id`, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "SS"}, {int64(2), "É"}})
		for _, s := range []referenceStep{metadata, copies, values, referencePrepared(values)} {
			run(s)
		}
		if b.pg {
			exec(`ROLLBACK`)
			return
		}
		var dump bytes.Buffer
		if err = stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "ALTER COLLATION") || !strings.Contains(dump.String(), "coll_seed_c_delta") {
			t.Fatal("missing bootstrap metadata deltas", dump.String())
		}
		if p.postgres != nil {
			pgConn := sequenceCoreConn(t, p.ctx, p.postgres)
			if _, err = pgConn.ExecContext(p.ctx, `CREATE USER coll_seed_delta_owner`); err != nil {
				t.Fatal(err)
			}
			if _, err = pgConn.ExecContext(p.ctx, `BEGIN`); err != nil {
				t.Fatal(err)
			}
			defer pgConn.ExecContext(p.ctx, `ROLLBACK`)
			if _, err = pgConn.ExecContext(p.ctx, dump.String()); err != nil {
				t.Fatal("emitted mutable-seed PostgreSQL replay", err)
			}
			for _, s := range []referenceStep{metadata, copies, values, referencePrepared(values)} {
				referenceRunStep(t, p.ctx, pgConn, p.postgresRecord, s)
			}
			if _, err = pgConn.ExecContext(p.ctx, `ROLLBACK`); err != nil {
				t.Fatal(err)
			}
			if _, err = pgConn.ExecContext(p.ctx, `DROP ROLE coll_seed_delta_owner`); err != nil {
				t.Fatal(err)
			}
		}
		for range 2 {
			if err = stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			run(referenceSelect(`SELECT collname,pg_collation_is_visible(oid) AS visible FROM pg_catalog.pg_collation WHERE oid=950 OR oid=6411 ORDER BY oid`, []string{"collname", "visible"}, []string{"NAME", "BOOL"}, [][]any{{"C", true}, {"pg_unicode_fast", true}}))
			if err = stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			for _, s := range []referenceStep{metadata, copies, values, referencePrepared(values)} {
				run(s)
			}
		}
		fresh := updateReturningNativeDestination(t)
		if _, err = fresh.ExecContext(p.ctx, `CREATE USER coll_seed_delta_owner`); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Restore(p.ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		for _, s := range []referenceStep{metadata, copies, values} {
			rows, queryErr := fresh.QueryContext(p.ctx, s.query)
			deleteReferenceConsume(t, false, rows, queryErr, s)
		}
		freshOwner := sequenceCoreConn(t, p.ctx, fresh)
		repair(freshOwner)
	})
}
