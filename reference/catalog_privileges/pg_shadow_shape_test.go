package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_0 system_views.sql and ruleutils.c independently specify this display.
// Root resolves the unobserved formatting against the pinned oracle, not native.
const pgShadowViewDefinition = " SELECT pg_authid.rolname AS usename,\n    pg_authid.oid AS usesysid,\n    pg_authid.rolcreatedb AS usecreatedb,\n    pg_authid.rolsuper AS usesuper,\n    pg_authid.rolreplication AS userepl,\n    pg_authid.rolbypassrls AS usebypassrls,\n    pg_authid.rolpassword AS passwd,\n    pg_authid.rolvaliduntil AS valuntil,\n    s.setconfig AS useconfig\n   FROM (pg_authid\n     LEFT JOIN pg_db_role_setting s ON (((pg_authid.oid = s.setrole) AND (s.setdatabase = (0)::oid))))\n  WHERE pg_authid.rolcanlogin;"

func pgShadowRow(name string, id int64, password, expiry any) []any {
	return []any{name, id, false, false, false, false, password, expiry, nil}
}

func pgShadowSelect(q string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, fixture.PGUserColumns, pgUserTypes, rows, args...)
	s.Bag = true
	return s
}

func TestReferencePGShadowShape(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgs_shape")
	pgUserScenario(t, []string{"pgs_shape_login"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{"SELECT * FROM pg_shadow WHERE FALSE", "SELECT * FROM pg_catalog.pg_shadow WHERE FALSE"} {
			s := pgShadowSelect(q, [][]any{})
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgUserMetadata(t, rows, b.PG) }
			pgUserBoth(t, p, b, s)
		}
		id := pgUserCreate(t, p, b, "pgs_shape_login")
		s := pgShadowSelect("SELECT * FROM pg_catalog.pg_shadow WHERE usesysid=$1", [][]any{pgShadowRow("pgs_shape_login", id, nil, nil)}, id)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgUserMetadata(t, rows, b.PG) }
		pgUserBoth(t, p, b, s)
		pgUserBoth(t, p, b, pgShadowSelect(s.Query, [][]any{}, nil))
		pgUserBoth(t, p, b, fixture.Select("SELECT useconfig,usename AS who,usesysid,usename,valuntil,passwd FROM pg_shadow WHERE usesysid=$1", []string{"useconfig", "who", "usesysid", "usename", "valuntil", "passwd"}, []string{"_TEXT", "NAME", "OID", "NAME", "TIMESTAMPTZ", "TEXT"}, [][]any{{nil, "pgs_shape_login", id, "pgs_shape_login", nil, nil}}, id))
		pgUserBoth(t, p, b, fixture.Select("SELECT relname,relkind,relnatts FROM pg_catalog.pg_class WHERE oid='pg_catalog.pg_shadow'::regclass", []string{"relname", "relkind", "relnatts"}, []string{"NAME", "CHAR", "INT2"}, [][]any{{"pg_shadow", "v", int64(9)}}))
		pgUserBoth(t, p, b, fixture.Select("SELECT viewname,definition FROM pg_catalog.pg_views WHERE schemaname='pg_catalog' AND viewname='pg_shadow'", []string{"viewname", "definition"}, []string{"NAME", "TEXT"}, [][]any{{"pg_shadow", pgShadowViewDefinition}}))
		pgUserBoth(t, p, b, fixture.Select("SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_shadow'", []string{"tablename"}, []string{"NAME"}, [][]any{}))
		// Type identities are discovered on each backend; ownership relationships,
		// not release-specific allocator sequences, are the public contract.
		pgUserBoth(t, p, b, fixture.Select("SELECT t.typname,a.typname AS array_name,t.typrelid=c.oid AS row_owner,a.typelem=t.oid AS array_element FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_shadow'::regclass", []string{"typname", "array_name", "row_owner", "array_element"}, []string{"NAME", "NAME", "BOOL", "BOOL"}, [][]any{{"pg_shadow", "_pg_shadow", true, true}}))
		var attrs [][]any
		for i, name := range fixture.PGUserColumns {
			oids := []int64{19, 26, 16, 16, 16, 16, 25, 1184, 1009}
			collations := []int64{950, 0, 0, 0, 0, 0, 950, 0, 950}
			attrs = append(attrs, []any{name, oids[i], int64(-1), false, collations[i]})
		}
		fixture.PGAttributeNameCastWitness(t, p, b, fixture.Select("SELECT attname::text AS name,atttypid::bigint AS type_oid,atttypmod::bigint AS typmod,attnotnull,attcollation::bigint AS collation FROM pg_catalog.pg_attribute WHERE attrelid='pg_catalog.pg_shadow'::regclass AND attnum>0 ORDER BY attnum", []string{"name", "type_oid", "typmod", "attnotnull", "collation"}, []string{"TEXT", "INT8", "INT8", "BOOL", "INT8"}, attrs))
		for _, q := range []string{"CREATE SCHEMA pgs_shape", "CREATE TABLE pgs_shape.pg_shadow(marker TEXT)", "SET search_path TO pgs_shape,pg_catalog"} { pgUserExec(t, p, b, q) }
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgs_shape.pg_shadow VALUES('ordinary table')", 1))
		pgUserBoth(t, p, b, fixture.Select("SELECT * FROM pg_shadow", []string{"marker"}, []string{"TEXT"}, [][]any{{"ordinary table"}}))
		pgUserBoth(t, p, b, s)
		pgUserExec(t, p, b, "RESET search_path")
		pgUserExec(t, p, b, "DROP ROLE pgs_shape_login")
	})
}
