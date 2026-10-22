package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func pgDatabaseFacts(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor) {
	t.Helper()
	q := "SELECT datname=current_database() AS same_database,datname=current_catalog AS same_catalog,datdba=(SELECT oid FROM pg_roles WHERE rolname='" + pgTableViewsOwner(b) + "') AS same_owner,encoding,datlocprovider,dathasloginevt,datcollate,datctype,datlocale,daticurules,datcollversion FROM pg_database WHERE datname=current_database()"
	s := fixture.Select(q, []string{"same_database", "same_catalog", "same_owner", "encoding", "datlocprovider", "dathasloginevt", "datcollate", "datctype", "datlocale", "daticurules", "datcollversion"}, []string{"BOOL", "BOOL", "BOOL", "INT4", "CHAR", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{true, true, true, int64(6), "c", false, "C", "C", nil, nil, nil}})
	pgDatabaseCheck(t, p, b, db, s)
	pgDatabaseCheck(t, p, b, db, fixture.Prepared(s))
	q = "SELECT datistemplate,datallowconn,datconnlimit,datfrozenxid IS NULL AS no_freeze,datminmxid IS NULL AS no_multixact,dattablespace FROM pg_database WHERE datname=current_database()"
	want := [][]any{{nil, nil, nil, false, true, nil}}
	if b.PG {
		want = [][]any{{false, true, int64(-1), false, false, int64(1663)}}
	}
	s = fixture.Select(q, []string{"datistemplate", "datallowconn", "datconnlimit", "no_freeze", "no_multixact", "dattablespace"}, []string{"BOOL", "BOOL", "INT4", "BOOL", "BOOL", "OID"}, want)
	pgDatabaseCheck(t, p, b, db, s)
	pgDatabaseCheck(t, p, b, fixture.ViewHold(t, p, b, q), s)
	for _, column := range []string{"datistemplate", "datallowconn", "datconnlimit", "datminmxid", "dattablespace"} {
		pgDatabaseCheck(t, p, b, db, fixture.Select("SELECT "+column+" IS NULL AS unavailable FROM pg_database WHERE datname=current_database()", []string{"unavailable"}, []string{"BOOL"}, [][]any{{!b.PG}}))
	}
	pgDatabaseCheck(t, p, b, db, fixture.Select("SELECT datfrozenxid::text::bigint>=3 AS valid_freeze FROM pg_database WHERE datname=current_database()", []string{"valid_freeze"}, []string{"BOOL"}, [][]any{{true}}))
	var equal any
	var filtered [][]any
	if b.PG {
		equal = true
		filtered = [][]any{{int64(-1)}}
	}
	pgDatabaseCheck(t, p, b, db, fixture.Select("SELECT datconnlimit=-1 AS unlimited FROM pg_database WHERE datname=current_database()", []string{"unlimited"}, []string{"BOOL"}, [][]any{{equal}}))
	pgDatabaseCheck(t, p, b, db, fixture.Select("SELECT datconnlimit FROM pg_database WHERE datname=current_database() AND datconnlimit=-1", []string{"datconnlimit"}, []string{"INT4"}, filtered))
}

func pgDatabaseEdges(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	s := fixture.Select("SELECT oid,relnamespace,reltype,relkind,relnatts,relhasindex,relisshared,reltablespace FROM pg_class WHERE oid=1262::oid", []string{"oid", "relnamespace", "reltype", "relkind", "relnatts", "relhasindex", "relisshared", "reltablespace"}, []string{"OID", "OID", "OID", "CHAR", "INT2", "BOOL", "BOOL", "OID"}, [][]any{{int64(1262), int64(11), int64(1248), "r", int64(18), true, true, int64(1664)}})
	pgDatabaseCheck(t, p, b, b.DB, s)
	pgDatabaseCheck(t, p, b, fixture.ViewHold(t, p, b, s.Query), s)
	pgDatabaseCheck(t, p, b, b.DB, fixture.Select("SELECT schemaname,tablename,tableowner,tablespace,hasindexes,hasrules,hastriggers,rowsecurity FROM pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_database'", []string{"schemaname", "tablename", "tableowner", "tablespace", "hasindexes", "hasrules", "hastriggers", "rowsecurity"}, []string{"NAME", "NAME", "NAME", "NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"pg_catalog", "pg_database", pgTableViewsOwner(b), "pg_global", true, false, false, false}}))
}

func TestReferencePGDatabaseMetadata(t *testing.T) {
	pgDatabaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgDatabaseFacts(t, p, b, b.DB)
		pgDatabaseEdges(t, p, b)
		q := "SELECT * FROM pg_database WHERE datname=current_database()"
		rows, err := b.DB.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		pgDatabaseMetadata(t, rows, b.PG)
		if !rows.Next() {
			t.Fatal("current row absent", rows.Err())
		}
		cells := make([]any, 18)
		ptrs := make([]any, 18)
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		pgDatabaseXID(t, cells[9])
		if b.PG {
			pgDatabaseXID(t, cells[10])
		} else if cells[10] != nil {
			t.Fatal("fabricated native physical MultiXact horizon", cells[10])
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("current row multiplicity", rows.Err())
		}
	})
}

func pgDatabaseXID(t *testing.T, value any) {
	t.Helper()
	bytes, ok := value.([]byte)
	if !ok {
		t.Fatalf("physical XID carrier: %T %#v", value, value)
	}
	if _, err := strconv.ParseUint(string(bytes), 10, 32); err != nil {
		t.Fatal("physical XID value", value, err)
	}
}

func TestReferencePGDatabaseSmoke(t *testing.T) {
	t.Run("CurrentMetadata", func(t *testing.T) {
		pgDatabaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			pgDatabaseFacts(t, p, b, b.DB)
			pgDatabaseEdges(t, p, b)
			s := fixture.Select("SELECT * FROM pg_catalog.pg_database WHERE FALSE", pgDatabaseColumns, pgDatabaseTypes, nil)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgDatabaseMetadata(t, rows, b.PG) }
			pgDatabaseCheck(t, p, b, fixture.ViewHold(t, p, b, s.Query), s)
			pgDatabaseMissing(t, p, b)
		})
	})
	// Reuse the complete behavioral phases, with independently owned fixtures
	// so database ACL/role teardown occurs before the next smoke phase.
	t.Run("ACLPublication", TestReferencePGDatabaseACL)
	t.Run("OrdinaryReader", TestReferencePGDatabaseVisibility)
	t.Run("NativeReplacement", TestReferencePGDatabaseControls)
	t.Run("Identity", TestReferencePGDatabaseIdentity)
}
