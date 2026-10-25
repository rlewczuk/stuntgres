package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTablespaceInfoComposition(t *testing.T) {
	p := fixture.NewScenario(t)
	tablespaceInfoBackends(t, p, func(t *testing.T, b fixture.Backend) {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, rec, s) }
		oid := tablespaceInfoCurrent(t, p, c)
		for _, prepared := range []bool{false, true} {
			for _, s := range []fixture.Step{
				fixture.Select("SELECT loc,n FROM pg_tablespace_location(NULL::oid) WITH ORDINALITY AS t(loc,n)", []string{"loc", "n"}, []string{"TEXT", "INT8"}, [][]any{{nil, int64(1)}}),
				fixture.Select("SELECT db,n FROM pg_tablespace_databases(NULL::oid) WITH ORDINALITY AS t(db,n)", []string{"db", "n"}, []string{"OID", "INT8"}, nil),
				fixture.Select("SELECT db,n FROM ROWS FROM(pg_tablespace_databases(NULL::oid),generate_series(1,2)) AS t(db,n) ORDER BY n", []string{"db", "n"}, []string{"OID", "INT4"}, [][]any{{nil, int64(1)}, {nil, int64(2)}}),
				fixture.Select("SELECT a,b FROM ROWS FROM(pg_tablespace_databases(NULL::oid),pg_tablespace_databases(NULL::oid)) AS t(a,b)", []string{"a", "b"}, []string{"OID", "OID"}, nil),
			} {
				s.Prepared = prepared
				run(s)
			}
		}
		// Ordinality is invocation order, not a portable ordering of database OIDs.
		expected := int64(1)
		if b.PG {
			expected = 4
		}
		run(fixture.Select("SELECT count(*) AS total,min(n) AS first,max(n) AS last,count(DISTINCT n) AS distinct_n FROM pg_tablespace_databases(1663) WITH ORDINALITY AS t(db,n)", []string{"total", "first", "last", "distinct_n"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{expected, int64(1), expected, expected}}))
		if !b.PG {
			run(fixture.Select("SELECT db,n FROM ROWS FROM(pg_tablespace_databases(1663),generate_series(1,2)) AS t(db,n) ORDER BY n", []string{"db", "n"}, []string{"OID", "INT4"}, [][]any{{oid, int64(1)}, {nil, int64(2)}}))
		}
		run(fixture.Exec("CREATE TABLE tsi_inputs(id integer,spc oid)", 0))
		run(fixture.Exec("INSERT INTO tsi_inputs VALUES(1,1663),(2,NULL),(3,1664),(4,4294967295)", 4))
		for _, join := range []string{", pg_tablespace_databases(i.spc) AS t(db)", " CROSS JOIN LATERAL pg_tablespace_databases(i.spc) AS t(db)"} {
			notices = nil
			run(fixture.Select("SELECT i.id,db FROM tsi_inputs i"+join+" WHERE db=(SELECT oid FROM pg_database WHERE datname=current_database()) ORDER BY i.id", []string{"id", "db"}, []string{"INT4", "OID"}, [][]any{{int64(1), tablespaceInfoOID(b, oid)}}))
			tablespaceInfoNotices(t, notices, "global tablespace never has databases", "4294967295 is not a tablespace OID")
		}
		notices = nil
		run(fixture.Select("SELECT i.id,db FROM tsi_inputs i LEFT JOIN LATERAL pg_tablespace_databases(i.spc) AS t(db) ON true WHERE i.id>1 ORDER BY i.id", []string{"id", "db"}, []string{"INT4", "OID"}, [][]any{{int64(2), nil}, {int64(3), nil}, {int64(4), nil}}))
		tablespaceInfoNotices(t, notices, "global tablespace never has databases", "4294967295 is not a tablespace OID")
		run(fixture.Exec("CREATE TABLE tsi_composed AS SELECT pg_tablespace_location(0) AS loc", 1))
		run(fixture.Exec("INSERT INTO tsi_composed SELECT pg_tablespace_location(1663)", 1))
		run(fixture.Select("SELECT loc FROM tsi_composed", []string{"loc"}, []string{"TEXT"}, [][]any{{""}, {""}}))
		run(fixture.Exec("CREATE TABLE tsi_set_consumer AS SELECT pg_tablespace_databases(NULL::oid) AS db", 0))
		run(fixture.Exec("INSERT INTO tsi_set_consumer SELECT pg_tablespace_databases(1663)", expected))
		run(fixture.Select("SELECT db FROM tsi_set_consumer WHERE db=(SELECT oid FROM pg_database WHERE datname=current_database())", []string{"db"}, []string{"OID"}, [][]any{{tablespaceInfoOID(b, oid)}}))
	})
}
