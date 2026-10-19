package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDefaultACLMetadata(t *testing.T) {
	pgDefaultACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		steps := []fixture.Step{
			fixture.Select("SELECT 'pg_catalog.pg_default_acl'::regclass::oid AS oid", []string{"oid"}, []string{"OID"}, [][]any{{int64(826)}}),
			fixture.Select("SELECT oid,relnamespace,relowner,relkind,relpersistence,relnatts,relhasindex,relisshared,reltablespace FROM pg_class WHERE oid=826::oid", []string{"oid", "relnamespace", "relowner", "relkind", "relpersistence", "relnatts", "relhasindex", "relisshared", "reltablespace"}, []string{"OID", "OID", "OID", "CHAR", "CHAR", "INT2", "BOOL", "BOOL", "OID"}, [][]any{{int64(826), int64(11), int64(10), "r", "p", int64(5), true, false, int64(0)}}),
			fixture.Select("SELECT schemaname,tablename,tableowner,tablespace,hasindexes,hasrules,hastriggers,rowsecurity FROM pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_default_acl'", pgTableColumns, pgTableTypes, [][]any{{"pg_catalog", "pg_default_acl", pgParameterACLOwner(b), nil, true, false, false, false}}),
			fixture.Select("SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS same_element,a.oid='pg_catalog.pg_default_acl[]'::regtype::oid AS same_array FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid=826::oid", []string{"same_type", "same_relation", "same_element", "same_array"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}),
		}
		attrs := make([][]any, 5)
		for i, typ := range []int64{26, 26, 26, 18, 1034} {
			dims := int64(0)
			if i == 4 {
				dims = 1
			}
			attrs[i] = []any{int64(826), int64(i + 1), pgDefaultACLColumns[i], typ, true, dims, int64(-1), int64(0), false}
		}
		steps = append(steps, fixture.Select("SELECT attrelid,attnum,attname,atttypid,attnotnull,attndims,atttypmod,attcollation,attisdropped FROM pg_attribute WHERE attrelid=826::oid AND attnum>0 ORDER BY attnum", []string{"attrelid", "attnum", "attname", "atttypid", "attnotnull", "attndims", "atttypmod", "attcollation", "attisdropped"}, []string{"OID", "INT2", "NAME", "OID", "BOOL", "INT2", "INT4", "OID", "BOOL"}, attrs))
		for _, s := range steps {
			pgDatabaseCheck(t, p, b, b.DB, s)
			pgDatabaseCheck(t, p, b, b.DB, fixture.Prepared(s))
			pgDatabaseCheck(t, p, b, fixture.ViewHold(t, p, b, s.Query), s)
		}
	})
}
