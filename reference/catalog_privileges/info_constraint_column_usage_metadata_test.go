package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoConstraintColumnUsageMetadata(t *testing.T) {
	ccuScenario(t, func(c *ccuCase) {
		c.exec("CREATE TABLE ccu_meta_parent(a integer,b integer,CONSTRAINT ccu_meta_u UNIQUE(a,b))", "CREATE TABLE ccu_meta_child(x integer,y integer,CONSTRAINT ccu_meta_fk FOREIGN KEY(x,y) REFERENCES ccu_meta_parent(b,a))")
		want := [][]any{c.pub("ccu_meta_parent", "a", "ccu_meta_u"), c.pub("ccu_meta_parent", "b", "ccu_meta_u"), c.pub("ccu_meta_parent", "a", "ccu_meta_fk"), c.pub("ccu_meta_parent", "b", "ccu_meta_fk")}
		held := c.hold(c.conn, ccuQuery)
		c.check(c.conn, "ccu_meta_%", want...)
		c.check(held, "ccu_meta_%", want...)
		c.check(c.conn, "ccu_meta_missing")
		c.check(held, "ccu_meta_missing")
		for _, pattern := range []string{"ccu_meta_%", "ccu_meta_missing"} {
			for _, prepared := range []bool{false, true} {
				var e fixture.Executor = c.conn
				if prepared {
					e = held
				}
				rows, err := e.QueryContext(c.p.Ctx, ccuQuery, pattern)
				if err != nil {
					c.t.Fatal(err)
				}
				saved := ccuMetadata(c.t, rows, c.b.PG)
				var expected [][]any
				if pattern == "ccu_meta_%" {
					expected = want
				}
				ccuAssertRows(c.t, rows, expected)
				ccuColumnTypes(c.t, saved, c.b.PG)
				if err := rows.Close(); err != nil {
					c.t.Fatal(err)
				}
				ccuColumnTypes(c.t, saved, c.b.PG)
			}
		}
		for _, boundary := range []struct {
			predicate string
			arg       any
			rows      [][]any
		}{
			{"table_name LIKE $1 AND column_name IS NULL", "ccu_meta_%", nil},
			{"table_name LIKE $1 AND column_name IS NOT NULL", "ccu_meta_%", want},
			{"table_name LIKE 'ccu_meta_%' AND column_name=$1", nil, nil},
			{"table_name LIKE $1", nil, nil},
		} {
			s := c.step(boundary.arg, boundary.rows)
			s.Query = strings.Replace(ccuQuery, "table_name LIKE $1", boundary.predicate, 1)
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
			c.run(c.hold(c.conn, s.Query), s)
		}
	})
}

func TestReferenceInfoConstraintColumnUsageIdentity(t *testing.T) {
	ccuScenario(t, func(c *ccuCase) {
		q := `SELECT a.attnum,a.attname,tn.nspname,t.typname,bn.nspname,b.typname,b.oid::bigint,a.attcollation::bigint,a.atttypmod,a.attndims,a.attnotnull,t.typnotnull FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='constraint_column_usage' JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace tn ON tn.oid=t.typnamespace JOIN pg_type b ON b.oid=t.typbasetype JOIN pg_namespace bn ON bn.oid=b.typnamespace ORDER BY a.attnum`
		want := make([][]any, 7)
		for i, name := range fixture.CCUColumns {
			want[i] = []any{int64(i + 1), name, "information_schema", "sql_identifier", "pg_catalog", "name", int64(19), int64(950), int64(-1), int64(0), false, false}
		}
		s := fixture.Select(q, []string{"attnum", "attname", "nspname", "typname", "nspname", "typname", "oid", "attcollation", "atttypmod", "attndims", "attnotnull", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "NAME", "NAME", "INT8", "INT8", "INT4", "INT2", "BOOL", "BOOL"}, want)
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		q = `SELECT c.relkind,c.reltype=t.oid AS row_link,t.typrelid=c.oid AS relation_link,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link,t.typnamespace=c.relnamespace AS row_namespace,a.typnamespace=t.typnamespace AS array_namespace,c.oid<>t.oid AS distinct_row,t.oid<>a.oid AS distinct_array FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='constraint_column_usage' JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray`
		s = fixture.Select(q, []string{"relkind", "row_link", "relation_link", "element_link", "array_link", "row_namespace", "array_namespace", "distinct_row", "distinct_array"}, []string{"CHAR", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", true, true, true, true, true, true, true, true}})
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		c.exec("CREATE TABLE ccu_identity_one(a integer CONSTRAINT ccu_identity_same CHECK(a>0))", "CREATE TABLE ccu_identity_two(a integer CONSTRAINT ccu_identity_same CHECK(a>0))")
		c.check(c.conn, "ccu_identity_%", c.pub("ccu_identity_one", "a", "ccu_identity_same"), c.pub("ccu_identity_two", "a", "ccu_identity_same"))
		q = `SELECT x.conname,x.oid<>y.oid AS distinct_constraint,x.conrelid<>y.conrelid AS distinct_relation FROM pg_constraint x JOIN pg_constraint y ON x.conname=y.conname JOIN pg_class a ON a.oid=x.conrelid JOIN pg_class b ON b.oid=y.conrelid WHERE a.relname='ccu_identity_one' AND b.relname='ccu_identity_two' AND x.conname='ccu_identity_same'`
		s = fixture.Select(q, []string{"conname", "distinct_constraint", "distinct_relation"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"ccu_identity_same", true, true}})
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
	})
}
