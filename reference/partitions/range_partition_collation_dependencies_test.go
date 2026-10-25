package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangePartitionCollationDependencies(t *testing.T) {
	for _, tc := range []struct {
		name, column, key string
		keyOnly           bool
	}{
		{"key_only", "TEXT", "k COLLATE rp_key_coll", true},
		{"effective", "TEXT COLLATE rp_key_coll", "k", false},
		{"explicit_effective", "TEXT COLLATE rp_key_coll", "k COLLATE rp_key_coll", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				for _, q := range []string{
					`CREATE COLLATION rp_key_coll FROM pg_catalog."C"`,
					`CREATE TABLE rp_coll_root(k ` + tc.column + `,payload INT) PARTITION BY RANGE(` + tc.key + `)`,
					`CREATE TABLE rp_coll_mid PARTITION OF rp_coll_root DEFAULT PARTITION BY RANGE(payload)`,
					`CREATE TABLE rp_coll_leaf PARTITION OF rp_coll_mid DEFAULT`,
					`CREATE SEQUENCE rp_coll_owned OWNED BY rp_coll_root.payload`,
					`CREATE INDEX rp_coll_leaf_ix ON rp_coll_leaf(payload)`,
					`CREATE TABLE rp_coll_sentinel(k INT)`,
				} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec(`INSERT INTO rp_coll_root VALUES('a',1)`, 1))
				run(fixture.Prepared(fixture.Exec(`INSERT INTO rp_coll_root VALUES($1,$2)`, 1, "é", 2)))
				run(fixture.Exec(`INSERT INTO rp_coll_sentinel VALUES(9)`, 1))
				data := fixture.Select(`SELECT k,payload FROM rp_coll_root ORDER BY payload`, []string{"k", "payload"}, []string{"TEXT", "INT4"}, [][]any{{"a", int64(1)}, {"é", int64(2)}})
				// A normal whole-relation key edge is separate from column edges.
				// Provisional release expectation: explicit + effective authority is deduplicated.
				edges := fixture.Select(`SELECT r.relname,d.objsubid,d.refobjsubid,d.deptype FROM pg_catalog.pg_depend d JOIN pg_catalog.pg_class r ON r.oid=d.objid JOIN pg_catalog.pg_collation c ON c.oid=d.refobjid WHERE d.classid=1259 AND d.refclassid=3456 AND r.relname='rp_coll_root' AND c.collname='rp_key_coll' AND d.objsubid=0 ORDER BY r.relname`, []string{"relname", "objsubid", "refobjsubid", "deptype"}, []string{"NAME", "INT4", "INT4", "CHAR"}, [][]any{{"rp_coll_root", int64(0), int64(0), "n"}})
				fixture.CollationRoutes(t, p, b, edges)
				fixture.CollationRoutes(t, p, b, data)
				if tc.keyOnly {
					// Independently predicted from StorePartitionKey's normal relation dependency;
					// root must resolve exact English/optional fields with the pinned oracle first.
					fixture.CollationError(t, p, b, `DROP COLLATION rp_key_coll RESTRICT`, fixture.Diagnostic{Code: "2BP01", Message: "cannot drop collation rp_key_coll because other objects depend on it", Detail: "table rp_coll_root depends on collation rp_key_coll", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
					fixture.CollationRoutes(t, p, b, data)
					fixture.CollationRoutes(t, p, b, edges)
					fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT relname FROM pg_catalog.pg_class WHERE relname='rp_coll_root' OR relname='rp_coll_mid' OR relname='rp_coll_leaf' OR relname='rp_coll_owned' OR relname='rp_coll_leaf_ix' ORDER BY relname`, []string{"relname"}, []string{"NAME"}, [][]any{{"rp_coll_leaf"}, {"rp_coll_leaf_ix"}, {"rp_coll_mid"}, {"rp_coll_owned"}, {"rp_coll_root"}}))
					fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT last_value,is_called FROM rp_coll_owned`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
				}
				run(fixture.Prepared(fixture.Exec(`DROP COLLATION rp_key_coll CASCADE`, 0)))
				fixture.CollationAbsent(t, p, b, "rp_key_coll")
				fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT relname FROM pg_catalog.pg_class WHERE relname='rp_coll_root' OR relname='rp_coll_mid' OR relname='rp_coll_leaf' OR relname='rp_coll_owned' OR relname='rp_coll_leaf_ix' ORDER BY relname`, []string{"relname"}, []string{"NAME"}, [][]any{}))
				fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT k FROM rp_coll_sentinel`, []string{"k"}, []string{"INT4"}, [][]any{{int64(9)}}))
				// The same catalog query must lose the key edge with the actual dependent objects.
				edges.Rows = [][]any{}
				fixture.CollationRoutes(t, p, b, edges)
				// Reuse each name to witness actual closure removal, not only filtered metadata.
				for _, q := range []string{`CREATE COLLATION rp_key_coll FROM pg_catalog."C"`, `CREATE TABLE rp_coll_root(k TEXT) PARTITION BY RANGE(k COLLATE rp_key_coll)`, `CREATE TABLE rp_coll_mid PARTITION OF rp_coll_root DEFAULT`, `CREATE TABLE rp_coll_leaf(k TEXT)`, `CREATE SEQUENCE rp_coll_owned`, `CREATE INDEX rp_coll_leaf_ix ON rp_coll_leaf(k)`} {
					run(fixture.Exec(q, 0))
				}
			})
		})
	}
}

func TestReferenceRangePartitionCollationTemporaryDependencies(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		owner, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(conn *sql.Conn, s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		for _, q := range []string{
			`CREATE COLLATION pg_temp.rp_temp_key_coll FROM pg_catalog."C"`,
			`CREATE TABLE rp_temp_coll_root(k TEXT,payload INT) PARTITION BY RANGE(k COLLATE pg_temp.rp_temp_key_coll)`,
			`CREATE TABLE rp_temp_coll_mid PARTITION OF rp_temp_coll_root DEFAULT PARTITION BY RANGE(payload)`,
			`CREATE TABLE rp_temp_coll_leaf PARTITION OF rp_temp_coll_mid DEFAULT`,
			`CREATE SEQUENCE rp_temp_coll_owned OWNED BY rp_temp_coll_root.payload`,
			`CREATE INDEX rp_temp_coll_leaf_ix ON rp_temp_coll_leaf(payload)`,
			`CREATE TABLE rp_temp_coll_sentinel(k INT)`,
		} {
			run(owner, fixture.Exec(q, 0))
		}
		run(owner, fixture.Exec(`INSERT INTO rp_temp_coll_root VALUES('a',1),('é',2)`, 2))
		run(peer, fixture.Exec(`INSERT INTO rp_temp_coll_sentinel VALUES(9)`, 1))
		data := fixture.Select(`SELECT k,payload FROM rp_temp_coll_root ORDER BY payload`, []string{"k", "payload"}, []string{"TEXT", "INT4"}, [][]any{{"a", int64(1)}, {"é", int64(2)}})
		run(owner, data)
		run(peer, fixture.Prepared(data))
		if b.PG {
			// PostgreSQL retains physical sessions in its pool and has no Dump API.
			// Explicit DROP exercises the portable provider closure; native below retires it.
			run(owner, fixture.Exec(`DROP COLLATION pg_temp.rp_temp_key_coll CASCADE`, 0))
		} else {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err == nil || dump.Len() != 0 {
				t.Fatal("persistent key with TEMP provider must reject Dump without output", err, dump.String())
			}
			run(owner, data)
			run(peer, data)
			held, err := owner.QueryContext(p.Ctx, data.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			if err := owner.Raw(func(raw any) error { return raw.(driver.Conn).Close() }); err != nil {
				t.Fatal(err)
			}
			fixture.DeleteReferenceConsume(t, b.PG, held, nil, data)
		}
		run(peer, fixture.Select(`SELECT relname FROM pg_catalog.pg_class WHERE relname='rp_temp_coll_root' OR relname='rp_temp_coll_mid' OR relname='rp_temp_coll_leaf' OR relname='rp_temp_coll_owned' OR relname='rp_temp_coll_leaf_ix' ORDER BY relname`, []string{"relname"}, []string{"NAME"}, [][]any{}))
		run(peer, fixture.Select(`SELECT collname FROM pg_catalog.pg_collation WHERE collname='rp_temp_key_coll'`, []string{"collname"}, []string{"NAME"}, [][]any{}))
		run(peer, fixture.Select(`SELECT k FROM rp_temp_coll_sentinel`, []string{"k"}, []string{"INT4"}, [][]any{{int64(9)}}))
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal("cleanup must restore portable Dump", err)
			}
			fresh := fixture.UpdateReturningNativeDestination(t)
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			witness := fixture.Select(`SELECT k FROM rp_temp_coll_sentinel`, []string{"k"}, []string{"INT4"}, [][]any{{int64(9)}})
			rows, err := fresh.QueryContext(p.Ctx, witness.Query)
			fixture.DeleteReferenceConsume(t, false, rows, err, witness)
		}
	})
}
