package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoleTableGrantsShape(t *testing.T) {
	rtgScenario(t, func(c *rtgCase) {
		c.exec("CREATE TABLE rtg_shape(a integer)")
		want := c.baseline(c.owner, "rtg_shape")
		held := c.hold(c.conn, rtgQuery)
		c.check(c.conn, "rtg_shape", c.owner, want...)
		c.check(held, "rtg_shape", c.owner, want...)
		rows, err := held.QueryContext(c.p.Ctx, rtgQuery, "rtg_shape", c.owner)
		if err != nil {
			c.t.Fatal(err)
		}
		rtgConsume(c.t, rows, c.b.PG, want)
		q := "SELECT grantor,grantee,table_catalog,table_schema,table_name,privilege_type,is_grantable,with_hierarchy FROM information_schema.role_table_grants WHERE table_name=$1 AND grantee=$2"
		c.run(c.conn, c.step(q, want, "rtg_shape", c.owner))
		c.run(c.conn, fixture.Prepared(c.step(q, want, "rtg_shape", c.owner)))
		for _, q := range []string{
			"SELECT * FROM information_schema.role_table_grants WHERE false",
			"SELECT * FROM information_schema.role_table_grants WHERE table_name='rtg_missing'",
			"SELECT * FROM information_schema.role_table_grants WHERE grantee IS NULL",
			"SELECT * FROM information_schema.role_table_grants WHERE grantee=$1",
			"SELECT * FROM information_schema.role_table_grants WHERE grantee=''",
		} {
			var args []any
			if q == "SELECT * FROM information_schema.role_table_grants WHERE grantee=$1" {
				args = []any{nil}
			}
			s := c.step(q, nil, args...)
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
			boundary := c.hold(c.conn, q)
			c.run(boundary, s)
			rows, err := boundary.QueryContext(c.p.Ctx, q, args...)
			if err != nil {
				c.t.Fatal(err)
			}
			rtgConsume(c.t, rows, c.b.PG, nil)
		}
		q = "SELECT g.* FROM (SELECT 1 AS witness) w LEFT JOIN information_schema.role_table_grants g ON g.table_name='rtg_missing'"
		s := fixture.Select(q, rtgColumns, rtgTypes, [][]any{make([]any, 8)})
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { rtgMetadata(t, rows, c.b.PG) }
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		q = `SELECT a.attnum,a.attname,tn.nspname,t.typname,bn.nspname,b.typname,b.oid::bigint,a.attcollation::bigint,a.atttypmod,a.attndims,a.attnotnull,t.typnotnull,t.typtypmod FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='role_table_grants' JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace tn ON tn.oid=t.typnamespace JOIN pg_type b ON b.oid=t.typbasetype JOIN pg_namespace bn ON bn.oid=b.typnamespace ORDER BY a.attnum`
		logical := make([][]any, 8)
		for i, name := range rtgColumns {
			domain, base, oid, modifier := "sql_identifier", "name", int64(19), int64(-1)
			if i == 5 {
				domain, base, oid = "character_data", "varchar", 1043
			}
			if i >= 6 {
				domain, base, oid, modifier = "yes_or_no", "varchar", 1043, 7
			}
			logical[i] = []any{int64(i + 1), name, "information_schema", domain, "pg_catalog", base, oid, int64(950), int64(-1), int64(0), false, false, modifier}
		}
		s = fixture.Select(q, []string{"attnum", "attname", "nspname", "typname", "nspname", "typname", "oid", "attcollation", "atttypmod", "attndims", "attnotnull", "typnotnull", "typtypmod"}, []string{"INT2", "NAME", "NAME", "NAME", "NAME", "NAME", "INT8", "INT8", "INT4", "INT2", "BOOL", "BOOL", "INT4"}, logical)
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		q = `SELECT c.reltype=t.oid AS row_link,t.typrelid=c.oid AS relation_link,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link,t.typnamespace=c.relnamespace AS row_namespace,a.typnamespace=t.typnamespace AS array_namespace,c.oid<>t.oid AS distinct_row,t.oid<>a.oid AS distinct_array FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='role_table_grants' JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray`
		s = fixture.Select(q, []string{"row_link", "relation_link", "element_link", "array_link", "row_namespace", "array_namespace", "distinct_row", "distinct_array"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true, true, true}})
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		var columns [][]any
		for i, name := range rtgColumns {
			columns = append(columns, []any{name, int64(i + 1), "YES", "information_schema", []string{"sql_identifier", "sql_identifier", "sql_identifier", "sql_identifier", "sql_identifier", "character_data", "yes_or_no", "yes_or_no"}[i]})
		}
		s = fixture.Select("SELECT column_name,ordinal_position,is_nullable,domain_schema,domain_name FROM information_schema.columns WHERE table_schema='information_schema' AND table_name='role_table_grants' ORDER BY ordinal_position", []string{"column_name", "ordinal_position", "is_nullable", "domain_schema", "domain_name"}, []string{"NAME", "INT4", "VARCHAR", "NAME", "NAME"}, columns)
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
	})
}
