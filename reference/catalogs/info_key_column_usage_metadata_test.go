package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoKeyColumnUsageMetadata(t *testing.T) {
	kcuScenario(t, func(c *kcuCase) {
		t := c.t
		c.exec("CREATE TABLE kcu_meta_parent(a integer,b integer,CONSTRAINT kcu_meta_u UNIQUE(a,b))", "CREATE TABLE kcu_meta_child(x integer,y integer,CONSTRAINT kcu_meta_fk FOREIGN KEY(x,y) REFERENCES kcu_meta_parent(b,a))", "CREATE TABLE kcu_meta_pk(a integer CONSTRAINT kcu_meta_p PRIMARY KEY)")
		want := [][]any{c.pub("kcu_meta_u", "kcu_meta_parent", "a", 1, nil), c.pub("kcu_meta_u", "kcu_meta_parent", "b", 2, nil), c.pub("kcu_meta_fk", "kcu_meta_child", "x", 1, int64(2)), c.pub("kcu_meta_fk", "kcu_meta_child", "y", 2, int64(1)), c.pub("kcu_meta_p", "kcu_meta_pk", "a", 1, nil)}
		held := c.hold(c.conn, kcuQuery)
		c.check(c.conn, "kcu_meta_%", want...)
		c.check(held, "kcu_meta_%", want...)
		c.check(c.conn, "kcu_meta_missing")
		c.check(held, "kcu_meta_missing")
		for _, pattern := range []string{"kcu_meta_%", "kcu_meta_missing"} {
			for _, prepared := range []bool{false, true} {
				var e fixture.Executor = c.conn
				if prepared {
					e = held
				}
				rows, err := e.QueryContext(c.p.Ctx, kcuQuery, pattern)
				if err != nil {
					t.Fatal(err)
				}
				saved := kcuMetadata(t, rows, c.b.PG)
				for rows.Next() {
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				kcuColumnTypes(t, saved, c.b.PG)
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				kcuColumnTypes(t, saved, c.b.PG)
			}
		}
	})
}

func TestReferenceInfoKeyColumnUsageIdentity(t *testing.T) {
	kcuScenario(t, func(c *kcuCase) {
		q := `SELECT a.attnum,a.attname,tn.nspname,t.typname,bn.nspname,b.typname,b.oid::bigint,a.attcollation::bigint,a.atttypmod,a.attndims,a.attnotnull,t.typnotnull FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='key_column_usage' JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace tn ON tn.oid=t.typnamespace JOIN pg_type b ON b.oid=t.typbasetype JOIN pg_namespace bn ON bn.oid=b.typnamespace ORDER BY a.attnum`
		want := make([][]any, 9)
		for i, name := range kcuColumns {
			domain, base, oid, collation := "sql_identifier", "name", int64(19), int64(950)
			if i >= 7 {
				domain, base, oid, collation = "cardinal_number", "int4", 23, 0
			}
			want[i] = []any{int64(i + 1), name, "information_schema", domain, "pg_catalog", base, oid, collation, int64(-1), int64(0), false, false}
		}
		s := fixture.Select(q, []string{"attnum", "attname", "nspname", "typname", "nspname", "typname", "oid", "attcollation", "atttypmod", "attndims", "attnotnull", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "NAME", "NAME", "INT8", "INT8", "INT4", "INT2", "BOOL", "BOOL"}, want)
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		q = `SELECT c.reltype=t.oid AS row_link,t.typrelid=c.oid AS relation_link,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link,t.typnamespace=c.relnamespace AS row_namespace,a.typnamespace=t.typnamespace AS array_namespace,c.oid<>t.oid AS distinct_row,t.oid<>a.oid AS distinct_array FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='key_column_usage' JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray`
		c.run(c.conn, fixture.Select(q, []string{"row_link", "relation_link", "element_link", "array_link", "row_namespace", "array_namespace", "distinct_row", "distinct_array"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true, true, true}}))
	})
}
