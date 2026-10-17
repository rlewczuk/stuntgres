package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGAMShape(t *testing.T) {
	pgAMScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, prepared := range []bool{false, true} {
			pgAMRows(t, p, b, b.DB, "SELECT * FROM pg_am WHERE FALSE", nil, prepared)
			pgAMRows(t, p, b, b.DB, pgAMProbe, nil, prepared, int64(0))
			pgAMRows(t, p, b, b.DB, pgAMProbe, nil, prepared, nil)
			pgAMRows(t, p, b, b.DB, pgAMProbe, pgAMLookup(int64(2), false), prepared, int64(2))
		}
		rows, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_am ORDER BY oid")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		pgAMMetadata(t, rows, b.PG)
		saved, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		pgAMSavedMetadata(t, saved, b.PG, false)
		checks := []fixture.Step{
			fixture.Select("SELECT oid,relnamespace,reltype,relnatts,relkind,relpersistence,relhasindex,relam FROM pg_class WHERE oid=2601", []string{"oid", "relnamespace", "reltype", "relnatts", "relkind", "relpersistence", "relhasindex", "relam"}, []string{"OID", "OID", "OID", "INT2", "CHAR", "CHAR", "BOOL", "OID"}, [][]any{{int64(2601), int64(11), int64(10015), int64(4), "r", "p", true, int64(2)}}),
			fixture.Select("SELECT 'pg_catalog.pg_am'::regtype::oid AS rowtype,'pg_catalog.pg_am[]'::regtype::oid AS arraytype", []string{"rowtype", "arraytype"}, []string{"OID", "OID"}, [][]any{{int64(10015), int64(10014)}}),
			fixture.Select("SELECT * FROM pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_am'", pgTableColumns, pgTableTypes, [][]any{pgTableRow("pg_catalog", "pg_am", pgTableViewsOwner(b), true)}),
			fixture.Select("SELECT viewname FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_am'", []string{"viewname"}, []string{"NAME"}, nil),
		}
		attrs := [][]any{
			{int64(1), "oid", int64(26), true, int64(-1), int64(0), int64(0)},
			{int64(2), "amname", int64(19), true, int64(-1), int64(950), int64(0)},
			{int64(3), "amhandler", int64(24), true, int64(-1), int64(0), int64(0)},
			{int64(4), "amtype", int64(18), true, int64(-1), int64(0), int64(0)},
		}
		checks = append(checks, fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attcollation,attndims FROM pg_attribute WHERE attrelid=2601 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "OID", "INT2"}, attrs))
		checks = append(checks, fixture.Select("SELECT oid,typrelid,typarray,typelem FROM pg_type WHERE oid IN (10014,10015) ORDER BY oid", []string{"oid", "typrelid", "typarray", "typelem"}, []string{"OID", "OID", "OID", "OID"}, [][]any{{int64(10014), int64(0), int64(0), int64(10015)}, {int64(10015), int64(2601), int64(10014), int64(0)}}))
		for _, s := range checks {
			fixture.PgDescriptionRows(t, p, b, s)
			fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
		}
	})
}
