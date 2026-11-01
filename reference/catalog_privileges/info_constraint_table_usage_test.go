package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoConstraintTableUsageRows(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ctu_target", "ctu_child")
	ctuScenario(t, func(c *ctuCase) {
		c.exec("CREATE SCHEMA ctu_target", "CREATE SCHEMA ctu_child",
			"CREATE TABLE ctu_target.parent(a integer,dead text,b integer,CONSTRAINT ctu_parent_u UNIQUE(a,b))",
			"ALTER TABLE ctu_target.parent DROP COLUMN dead",
			"CREATE TABLE ctu_target.pk(z integer,a integer,CONSTRAINT ctu_parent_pk PRIMARY KEY(a,z))",
			"CREATE TABLE ctu_child.child(x integer,y integer,CONSTRAINT ctu_same FOREIGN KEY(x,y) REFERENCES ctu_target.parent(b,a))",
			"CREATE TABLE ctu_child.child_two(x integer,y integer,CONSTRAINT ctu_same FOREIGN KEY(y,x) REFERENCES ctu_target.parent(a,b))",
			"CREATE TABLE ctu_target.index_parent(a integer,b integer,payload text)",
			"CREATE UNIQUE INDEX ctu_provider ON ctu_target.index_parent(b,a) INCLUDE(payload)",
			"CREATE TABLE ctu_child.index_child(x integer,y integer,CONSTRAINT ctu_index_fk FOREIGN KEY(x,y) REFERENCES ctu_target.index_parent(a,b))",
			"CREATE TABLE ctu_target.check_only(a integer NOT NULL,CONSTRAINT ctu_check CHECK(a>0),CONSTRAINT ctu_constant CHECK(true))",
			"CREATE TABLE ctu_target.none(a integer,b text)")
		want := [][]any{c.row("ctu_target", "index_parent", "ctu_child", "ctu_index_fk"), c.row("ctu_target", "parent", "ctu_child", "ctu_same"), c.row("ctu_target", "parent", "ctu_child", "ctu_same"), c.row("ctu_target", "parent", "ctu_target", "ctu_parent_u"), c.row("ctu_target", "pk", "ctu_target", "ctu_parent_pk")}
		query := "SELECT * FROM information_schema.constraint_table_usage WHERE table_schema=$1 ORDER BY table_catalog,table_schema,table_name,constraint_catalog,constraint_schema,constraint_name"
		s := c.step("ctu_target", want)
		s.Query = query
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		held := c.hold(c.conn, query)
		c.run(held, s)
		bag := s
		bag.Query = strings.Split(query, " ORDER BY")[0]
		bag.Bag, bag.CheckScan = true, nil
		c.run(c.conn, bag)
		c.run(c.conn, fixture.Prepared(bag))
		for _, schema := range []string{"ctu_target", "ctu_absent"} {
			var expected [][]any
			if schema == "ctu_target" {
				expected = want
			}
			for _, e := range []fixture.Executor{c.conn, held} {
				rows, err := e.QueryContext(c.p.Ctx, query, schema)
				if err != nil {
					c.t.Fatal(err)
				}
				ctuAssertRows(c.t, rows, expected, c.b.PG)
			}
		}
		for _, predicate := range []string{"table_schema='ctu_absent'", "table_schema='ctu_target' AND table_name='absent'", "table_schema='ctu_target' AND table_name='check_only'", "table_schema='ctu_target' AND table_name='none'", "table_name LIKE NULL", "table_name=NULL"} {
			empty := c.step(nil, nil)
			empty.Query, empty.Args = "SELECT * FROM information_schema.constraint_table_usage WHERE "+predicate, nil
			c.run(c.conn, empty)
			c.run(c.conn, fixture.Prepared(empty))
		}
		for _, field := range fixture.CTUColumns {
			for _, isNull := range []bool{true, false} {
				boundary := s
				null := " IS NOT NULL"
				if isNull {
					null, boundary.Rows, boundary.CheckScan = " IS NULL", nil, nil
				}
				boundary.Query = strings.Replace(query, " WHERE table_schema=$1", " WHERE table_schema=$1 AND "+field+null, 1)
				c.run(c.conn, boundary)
				c.run(c.conn, fixture.Prepared(boundary))
			}
		}
		c.run(c.conn, fixture.Exec("INSERT INTO ctu_target.none VALUES(1,'unused'),(2,NULL)", 2))
		c.run(c.conn, s)
		c.run(c.conn, fixture.Exec("DELETE FROM ctu_target.none", 2))
		c.run(c.conn, fixture.Prepared(s))
		ctuRelationships(c)
		ctuCatalogMetadata(c)
	})
	t.Run("excluded_and_partition_kinds", func(t *testing.T) {
		ctuScenario(t, func(c *ctuCase) {
			c.exec("CREATE TABLE ctu_kind_implicit(a integer PRIMARY KEY)",
				"CREATE TABLE ctu_kind_checks(a integer NOT NULL CHECK(a>0),CHECK(true))",
				"CREATE TABLE ctu_kind_index(a integer)", "CREATE UNIQUE INDEX ctu_kind_index_u ON ctu_kind_index(a)",
				"CREATE TABLE ctu_kind_exclusion(a integer,CONSTRAINT ctu_kind_ex EXCLUDE USING btree(a WITH =))",
				"CREATE DOMAIN ctu_kind_domain AS integer NOT NULL CHECK(VALUE>0)", "CREATE TABLE ctu_kind_domain_use(a ctu_kind_domain)",
				"CREATE VIEW ctu_kind_view AS SELECT a FROM ctu_kind_checks",
				"CREATE FOREIGN DATA WRAPPER ctu_kind_fdw", "CREATE SERVER ctu_kind_server FOREIGN DATA WRAPPER ctu_kind_fdw",
				"CREATE FOREIGN TABLE ctu_kind_foreign(a integer NOT NULL CHECK(a>0)) SERVER ctu_kind_server",
				"CREATE TABLE ctu_kind_part(a integer,b integer,CONSTRAINT ctu_kind_part_u UNIQUE(b,a)) PARTITION BY RANGE(a)",
				"CREATE TABLE ctu_kind_leaf PARTITION OF ctu_kind_part FOR VALUES FROM(0) TO(10)")
			defer c.conn.ExecContext(c.p.Ctx, "DROP SERVER ctu_kind_server CASCADE")
			defer c.conn.ExecContext(c.p.Ctx, "DROP FOREIGN DATA WRAPPER ctu_kind_fdw CASCADE")
			// Assert real released DDL descriptors before using the leaf identity.
			facts := fixture.Select("SELECT r.relname,r.relkind,c.conname,c.contype,c.conparentid<>0 AS inherited FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid WHERE r.relname IN ('ctu_kind_implicit','ctu_kind_part','ctu_kind_leaf') AND c.contype IN ('p','u') ORDER BY r.relname", []string{"relname", "relkind", "conname", "contype", "inherited"}, []string{"NAME", "CHAR", "NAME", "CHAR", "BOOL"}, [][]any{{"ctu_kind_implicit", "r", "ctu_kind_implicit_pkey", "p", false}, {"ctu_kind_leaf", "r", "ctu_kind_leaf_b_a_key", "u", true}, {"ctu_kind_part", "p", "ctu_kind_part_u", "u", false}})
			c.run(c.conn, facts)
			c.run(c.conn, fixture.Prepared(facts))
			want := [][]any{c.pub("ctu_kind_implicit", "ctu_kind_implicit_pkey"), c.pub("ctu_kind_part", "ctu_kind_part_u"), c.pub("ctu_kind_leaf", "ctu_kind_leaf_b_a_key")}
			c.routes(c.hold(c.conn, ctuQuery), "ctu_kind_%", want...)
		})
	})
}

func ctuRelationships(c *ctuCase) {
	c.t.Helper()
	kcu := fixture.Select("SELECT table_schema,table_name,column_name,ordinal_position,position_in_unique_constraint FROM information_schema.key_column_usage WHERE table_schema IN ('ctu_target','ctu_child') ORDER BY table_schema,table_name,ordinal_position", []string{"table_schema", "table_name", "column_name", "ordinal_position", "position_in_unique_constraint"}, []string{"NAME", "NAME", "NAME", "INT4", "INT4"}, [][]any{{"ctu_child", "child", "x", int64(1), int64(2)}, {"ctu_child", "child", "y", int64(2), int64(1)}, {"ctu_child", "child_two", "y", int64(1), int64(1)}, {"ctu_child", "child_two", "x", int64(2), int64(2)}, {"ctu_child", "index_child", "x", int64(1), int64(2)}, {"ctu_child", "index_child", "y", int64(2), int64(1)}, {"ctu_target", "parent", "a", int64(1), nil}, {"ctu_target", "parent", "b", int64(2), nil}, {"ctu_target", "pk", "a", int64(1), nil}, {"ctu_target", "pk", "z", int64(2), nil}})
	rc := fixture.Select("SELECT constraint_catalog,constraint_schema,constraint_name,unique_constraint_catalog,unique_constraint_schema,unique_constraint_name,match_option,update_rule,delete_rule FROM information_schema.referential_constraints WHERE constraint_schema='ctu_child' ORDER BY constraint_name", []string{"constraint_catalog", "constraint_schema", "constraint_name", "unique_constraint_catalog", "unique_constraint_schema", "unique_constraint_name", "match_option", "update_rule", "delete_rule"}, []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "VARCHAR", "VARCHAR", "VARCHAR"}, [][]any{{c.database, "ctu_child", "ctu_index_fk", nil, nil, nil, "NONE", "NO ACTION", "NO ACTION"}, {c.database, "ctu_child", "ctu_same", c.database, "ctu_target", "ctu_parent_u", "NONE", "NO ACTION", "NO ACTION"}, {c.database, "ctu_child", "ctu_same", c.database, "ctu_target", "ctu_parent_u", "NONE", "NO ACTION", "NO ACTION"}})
	vectors := fixture.Select("SELECT r.relname,p.relname,i.relname,c.conkey::text,c.confkey::text,pg_get_indexdef(c.conindid,1,false) AS key_one,pg_get_indexdef(c.conindid,2,false) AS key_two,pg_get_indexdef(c.conindid,3,false) AS include_three,c.conindid=x.indexrelid AS same_index,c.confrelid=x.indrelid AS same_parent FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_class p ON p.oid=c.confrelid JOIN pg_class i ON i.oid=c.conindid JOIN pg_index x ON x.indexrelid=i.oid JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname='ctu_child' ORDER BY r.relname", []string{"relname", "relname", "relname", "conkey", "confkey", "key_one", "key_two", "include_three", "same_index", "same_parent"}, []string{"NAME", "NAME", "NAME", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{"child", "parent", "ctu_parent_u", "{1,2}", "{3,1}", "a", "b", "", true, true}, {"child_two", "parent", "ctu_parent_u", "{2,1}", "{1,3}", "a", "b", "", true, true}, {"index_child", "index_parent", "ctu_provider", "{1,2}", "{1,2}", "b", "a", "payload", true, true}})
	for _, s := range []fixture.Step{kcu, rc, vectors} {
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		c.run(c.hold(c.conn, s.Query), s)
	}
}

func ctuCatalogMetadata(c *ctuCase) {
	c.t.Helper()
	var attrs, columns [][]any
	for i, name := range fixture.CTUColumns {
		attrs = append(attrs, []any{int64(i + 1), name, "information_schema", "sql_identifier", "d", int64(19), int64(-1), false, int64(950), int64(0), false})
		columns = append(columns, []any{name, int64(i + 1), "YES", "name", c.database, "information_schema", "sql_identifier", "pg_catalog", "name"})
	}
	facts := fixture.Select("SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype::bigint AS typbasetype,a.atttypmod,a.attnotnull,a.attcollation::bigint AS attcollation,a.attndims,t.typnotnull FROM pg_attribute a JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.constraint_table_usage'::regclass AND a.attnum>0 ORDER BY a.attnum", []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "INT8", "INT4", "BOOL", "INT8", "INT2", "BOOL"}, attrs)
	info := fixture.Select("SELECT column_name,ordinal_position,is_nullable,data_type,domain_catalog,domain_schema,domain_name,udt_schema,udt_name FROM information_schema.columns WHERE table_schema='information_schema' AND table_name='constraint_table_usage' ORDER BY ordinal_position", []string{"column_name", "ordinal_position", "is_nullable", "data_type", "domain_catalog", "domain_schema", "domain_name", "udt_schema", "udt_name"}, []string{"NAME", "INT4", "VARCHAR", "VARCHAR", "NAME", "NAME", "NAME", "NAME", "NAME"}, columns)
	links := fixture.Select("SELECT c.relkind,c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.constraint_table_usage'::regclass", []string{"relkind", "same_type", "same_relation", "array_element", "array_link"}, []string{"CHAR", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", true, true, true, true}})
	for _, s := range []fixture.Step{facts, info, links} {
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		c.run(c.hold(c.conn, s.Query), s)
	}
}
