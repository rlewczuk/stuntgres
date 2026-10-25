package reference_test

import (
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func pgClassHistoryWitnessScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func TestReferencePgClassBuiltinIndexHistory(t *testing.T) {
	pgClassHistoryWitnessScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		// Every represented table's REL_18_0 catalog header declares at least
		// one index. This fact is independent of pg_tables summary inclusion.
		// https://github.com/postgres/postgres/tree/REL_18_0/src/include/catalog
		tables := []string{"pg_aggregate", "pg_attrdef", "pg_attribute", "pg_cast", "pg_class", "pg_collation", "pg_constraint", "pg_conversion", "pg_database", "pg_db_role_setting", "pg_depend", "pg_description", "pg_enum", "pg_extension", "pg_index", "pg_namespace", "pg_operator", "pg_proc", "pg_rewrite", "pg_sequence", "pg_shdescription", "pg_tablespace", "pg_ts_dict", "pg_ts_template"}
		// Released system_views.sql / information_schema.sql define views,
		// which have no relation indexes. Keep exact negative controls too.
		views := []string{"pg_available_extension_versions", "pg_available_extensions", "pg_cursors", "pg_group", "pg_indexes", "pg_matviews", "pg_prepared_xacts", "pg_roles", "pg_sequences", "pg_tables", "pg_timezone_abbrevs", "pg_timezone_names", "pg_user", "pg_views"}
		query := "SELECT relname,relkind,relhasindex FROM pg_catalog.pg_class WHERE oid=$1::regclass::oid"
		held := fixture.ViewHold(t, p, b, query)
		check := func(t *testing.T, name, schema, kind string, indexed bool) {
			t.Helper()
			s := fixture.Select(query, []string{"relname", "relkind", "relhasindex"}, []string{"NAME", "CHAR", "BOOL"}, [][]any{{name, kind, indexed}}, schema+"."+name)
			pgClassCheck(t, p, b, b.DB, s)
			pgClassCheck(t, p, b, b.DB, fixture.Prepared(s))
			pgClassCheck(t, p, b, held, s)
		}
		for _, name := range tables {
			t.Run(name, func(t *testing.T) {
				check(t, name, "pg_catalog", "r", true)
				if !b.PG {
					// Compatibility flags must not fabricate missing native index
					// rows or expand another feature's relation ownership.
					pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT indexrelid FROM pg_index WHERE indrelid=$1::regclass::oid", []string{"indexrelid"}, []string{"OID"}, nil, "pg_catalog."+name))
				}
			})
		}
		for _, name := range views {
			check(t, name, "pg_catalog", "v", false)
		}
		check(t, "table_privileges", "information_schema", "v", false)
	})
}

func TestReferencePgClassFirstChildRollback(t *testing.T) {
	pgClassHistoryWitnessScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		owner, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.PGClassExec(t, p, b, owner, "CREATE TABLE pc18_first_root(n integer) PARTITION BY RANGE(n)", "CREATE TABLE pc18_first_child(n integer)")
		query := "SELECT relhassubclass FROM pg_class WHERE relname='pc18_first_root'"
		ownerStmt, err := owner.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer ownerStmt.Close()
		peerStmt, err := peer.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer peerStmt.Close()
		ownerHeld := fixture.HeldStatement{Executor: owner, Statement: ownerStmt, SQL: query}
		peerHeld := fixture.HeldStatement{Executor: peer, Statement: peerStmt, SQL: query}
		check := func(ownerFlag, peerFlag bool) {
			s := fixture.Select(query, []string{"relhassubclass"}, []string{"BOOL"}, [][]any{{ownerFlag}})
			pgClassCheck(t, p, b, owner, s)
			pgClassCheck(t, p, b, ownerHeld, s)
			s.Rows = [][]any{{peerFlag}}
			pgClassCheck(t, p, b, peer, s)
			pgClassCheck(t, p, b, peerHeld, s)
		}
		membership := func(db fixture.Executor, member bool) {
			pgClassCheck(t, p, b, db, fixture.Select("SELECT relispartition,relpartbound IS NULL AS no_bound FROM pg_class WHERE relname='pc18_first_child'", []string{"relispartition", "no_bound"}, []string{"BOOL", "BOOL"}, [][]any{{member, !member}}))
		}
		attach := "ALTER TABLE pc18_first_root ATTACH PARTITION pc18_first_child FOR VALUES FROM(0) TO(10)"
		check(false, false)
		membership(owner, false)
		fixture.PGClassExec(t, p, b, owner, "BEGIN", "SAVEPOINT pc18_first_save", attach)
		defer owner.ExecContext(p.Ctx, "ROLLBACK")
		check(true, false)
		membership(owner, true)
		membership(peer, false)
		fixture.PGClassExec(t, p, b, owner, "ROLLBACK TO pc18_first_save")
		check(false, false)
		membership(owner, false)
		membership(peer, false)
		// A successful second private publication is also discarded by full
		// transaction rollback, not merely by the savepoint restoration.
		fixture.PGClassExec(t, p, b, owner, attach)
		check(true, false)
		fixture.PGClassExec(t, p, b, owner, "ROLLBACK")
		check(false, false)
		membership(owner, false)
		membership(peer, false)
	})
}

func TestReferencePgClassInvalidIndexHistory(t *testing.T) {
	pgClassHistoryWitnessScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		builder := fixture.ConcurrentIndexConn(t, p, b)
		table, index := "pc18_invalid_heap", "pc18_invalid_idx"
		fixture.ConcurrentIndexExec(t, p, b, builder, "CREATE TABLE "+table+"(n integer)", 0)
		fixture.ConcurrentIndexExec(t, p, b, builder, "INSERT INTO "+table+" VALUES(1),(1)", 2)
		parentQuery := "SELECT relhasindex,relhassubclass FROM pg_class WHERE relname='" + table + "'"
		indexQuery := "SELECT c.relname,c.relkind,i.indisready,i.indisvalid,i.indislive FROM pg_class c JOIN pg_index i ON i.indexrelid=c.oid WHERE c.relname='" + index + "'"
		parentHeld := fixture.ViewHold(t, p, b, parentQuery)
		indexHeld := fixture.ViewHold(t, p, b, indexQuery)
		parent := func(indexed bool) {
			s := fixture.Select(parentQuery, []string{"relhasindex", "relhassubclass"}, []string{"BOOL", "BOOL"}, [][]any{{indexed, false}})
			pgClassCheck(t, p, b, builder, s)
			pgClassCheck(t, p, b, parentHeld, s)
		}
		indexRows := func(present bool) {
			var want [][]any
			if present {
				want = [][]any{{index, "i", false, false, true}}
			}
			s := fixture.Select(indexQuery, []string{"relname", "relkind", "indisready", "indisvalid", "indislive"}, []string{"NAME", "CHAR", "BOOL", "BOOL", "BOOL"}, want)
			pgClassCheck(t, p, b, builder, s)
			pgClassCheck(t, p, b, indexHeld, s)
		}
		parent(false)
		indexRows(false)
		// The existing first-scan fixture deterministically retains a live
		// invalid descriptor after a duplicate-data concurrent unique build.
		failure := fixture.ErrorDiagnostic("CREATE UNIQUE INDEX CONCURRENTLY "+index+" ON "+table+"(n)", fixture.ConcurrentIndexUnique(table, index, "Key (n)=(1) is duplicated.", true))
		failure.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, builder, b.Recorder, failure)
		fixture.ConcurrentIndexWait(t, p, b, index, stuntgres.IndexState{Live: true}, 0, 0, "")
		indexRows(true)
		parent(true)
		fixture.ConcurrentIndexExec(t, p, b, builder, "DROP INDEX "+index, 0)
		indexRows(false)
		parent(true)
	})
}
