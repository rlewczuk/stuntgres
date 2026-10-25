package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnsPopulation(t *testing.T) {
	icConfiguredScenario(t, icConfigureForeign, func(c *icCase) {
		t := c.t
		c.exec("CREATE TABLE ic_scope.base(a integer)", "CREATE VIEW ic_scope.simple AS SELECT a FROM ic_scope.base", "CREATE TABLE ic_scope.parent(a integer) PARTITION BY RANGE(a)", "CREATE TABLE ic_scope.child PARTITION OF ic_scope.parent FOR VALUES FROM(0) TO(10)", "CREATE SEQUENCE ic_scope.seq", "CREATE INDEX idx ON ic_scope.base(a)", "CREATE MATERIALIZED VIEW ic_scope.mat AS SELECT a FROM ic_scope.base", "CREATE TYPE ic_scope.comp AS(a integer)", "CREATE TABLE ic_scope.zero()")
		if c.b.PG {
			c.exec("CREATE EXTENSION postgres_fdw", "CREATE SERVER ic_live FOREIGN DATA WRAPPER postgres_fdw OPTIONS(host '127.0.0.1',port '5432',dbname 'stuntgres_reference')", "CREATE USER MAPPING FOR CURRENT_USER SERVER ic_live OPTIONS(user 'stuntgres',password 'stuntgres')")
		} else {
			c.exec("CREATE FOREIGN DATA WRAPPER ic_fdw HANDLER public.ic_both", "CREATE SERVER ic_live FOREIGN DATA WRAPPER ic_fdw", "CREATE USER MAPPING FOR CURRENT_USER SERVER ic_live")
		}
		// Both actual handlers provide UPDATE and DELETE. Metadata must not
		// access this deliberately absent remote relation or invoke Go I/O.
		c.exec("CREATE FOREIGN TABLE ic_scope.foreign_t(a integer) SERVER ic_live OPTIONS(schema_name 'public',table_name 'absent_remote')")
		var want [][]any
		for _, name := range []string{"base", "child", "foreign_t", "parent", "simple"} {
			row := c.integer(name, "a", 1)
			want = append(want, row)
		}
		for _, prepared := range []bool{false, true} {
			s := icProjection(c.b.PG, "table_schema='ic_scope'", want)
			s.Prepared = prepared
			c.run(c.conn, s)
		}
		for _, name := range []string{"seq", "idx", "mat", "comp", "zero", "absent"} {
			c.check(name)
		}
		// Real catalogs and real provider-backed views; the rest of PG's much
		// larger inventory is not presumed to exist in the embedded engine.
		// Existing native CatalogAttributes mark nspacl NOT NULL:
		// pgcatalog/catalog_attribute_facts.go:74 (no namespace override).
		// Columns preserves that authority; PostgreSQL's field is nullable.
		nspaclNullable := "NO"
		if c.b.PG {
			nspaclNullable = "YES"
		}
		c.run(c.conn, icProjection(c.b.PG, "table_schema='pg_catalog' AND table_name='pg_namespace'", [][]any{
			icRow(c.database, "pg_catalog", "pg_namespace", "oid", 1, "oid", "pg_catalog", "oid", map[string]any{"is_nullable": "NO"}),
			icRow(c.database, "pg_catalog", "pg_namespace", "nspname", 2, "name", "pg_catalog", "name", map[string]any{"is_nullable": "NO", "collation_catalog": c.database, "collation_schema": "pg_catalog", "collation_name": "C"}),
			icRow(c.database, "pg_catalog", "pg_namespace", "nspowner", 3, "oid", "pg_catalog", "oid", map[string]any{"is_nullable": "NO"}),
			icRow(c.database, "pg_catalog", "pg_namespace", "nspacl", 4, "ARRAY", "pg_catalog", "_aclitem", map[string]any{"is_nullable": nspaclNullable}),
		}))
		c.run(c.conn, icProjection(c.b.PG, "table_schema='information_schema' AND table_name='enabled_roles'", [][]any{icRow(c.database, "information_schema", "enabled_roles", "role_name", 1, "name", "pg_catalog", "name", map[string]any{"domain_catalog": c.database, "domain_schema": "information_schema", "domain_name": "sql_identifier", "collation_catalog": c.database, "collation_schema": "pg_catalog", "collation_name": "C", "is_updatable": "NO"})}))
		peer, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		c.exec("CREATE TEMP TABLE ic_temp_own(a integer)")
		defer c.conn.ExecContext(c.p.Ctx, "DROP TABLE IF EXISTS ic_temp_own")
		c.run(peer, fixture.Exec("CREATE TEMP TABLE ic_temp_peer(a integer)", 0))
		defer peer.ExecContext(c.p.Ctx, "DROP TABLE IF EXISTS ic_temp_peer")
		var ownSchema, peerSchema string
		q := "SELECT n.nspname FROM pg_class r JOIN pg_namespace n ON n.oid=r.relnamespace WHERE r.oid=$1::regclass::oid"
		if err := c.conn.QueryRowContext(c.p.Ctx, q, "ic_temp_own").Scan(&ownSchema); err != nil {
			t.Fatal(err)
		}
		if err := peer.QueryRowContext(c.p.Ctx, q, "ic_temp_peer").Scan(&peerSchema); err != nil {
			t.Fatal(err)
		}
		if ownSchema == peerSchema {
			t.Fatal("TEMP namespaces must be distinct")
		}
		for _, x := range []struct {
			executor      fixture.Executor
			schema, table string
		}{{c.conn, ownSchema, "ic_temp_own"}, {peer, peerSchema, "ic_temp_peer"}} {
			row := icRow(c.database, x.schema, x.table, "a", 1, "integer", "pg_catalog", "int4", icIntFields())
			c.run(x.executor, icProjection(c.b.PG, "table_name IN ('ic_temp_own','ic_temp_peer')", [][]any{row}))
		}
	})
	t.Run("providers", func(t *testing.T) { icScenario(t, icProviderInventory) })
}
