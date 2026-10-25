package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoConstraintColumnUsageRows(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ccu_rows_child_schema")
	ccuScenario(t, func(c *ccuCase) {
		c.exec(
			"CREATE TABLE ccu_parent(a integer,dead text,b integer,CONSTRAINT ccu_same UNIQUE(a,b))",
			"ALTER TABLE ccu_parent DROP COLUMN dead",
			"CREATE TABLE ccu_pk(z integer CONSTRAINT ccu_pk_z_nn NOT NULL,a integer CONSTRAINT ccu_pk_a_nn NOT NULL,CONSTRAINT ccu_pk_key PRIMARY KEY(a,z))",
			"CREATE TABLE ccu_implicit(a integer CONSTRAINT ccu_implicit_p PRIMARY KEY)",
			"CREATE TABLE ccu_child(x integer,y integer,CONSTRAINT ccu_fk FOREIGN KEY(x,y) REFERENCES ccu_parent(b,a))",
			"CREATE TABLE ccu_child_two(x integer,y integer,CONSTRAINT ccu_fk FOREIGN KEY(y,x) REFERENCES ccu_parent(a,b))",
			"CREATE TABLE ccu_checks(a integer CONSTRAINT ccu_named_nn NOT NULL,b integer,CONSTRAINT ccu_same CHECK(a>0 AND (b>a OR a=0)),CONSTRAINT ccu_constant CHECK(true))",
			"CREATE TABLE ccu_index_parent(a integer,b integer,payload text)",
			"CREATE UNIQUE INDEX ccu_provider ON ccu_index_parent(b,a) INCLUDE(payload)",
			"CREATE TABLE ccu_index_child(x integer,y integer,CONSTRAINT ccu_index_fk FOREIGN KEY(x,y) REFERENCES ccu_index_parent(a,b))",
			"CREATE TABLE ccu_index_only(a integer,payload text)",
			"CREATE UNIQUE INDEX ccu_index_only_u ON ccu_index_only(a) INCLUDE(payload)",
			"CREATE TABLE ccu_exclusion(a integer,CONSTRAINT ccu_ex EXCLUDE USING btree(a WITH =))",
			"CREATE DOMAIN ccu_domain AS integer NOT NULL CHECK(VALUE>0)",
			"CREATE TABLE ccu_domain_table(a ccu_domain)",
			"CREATE TABLE ccu_none(a integer,b text)",
			"CREATE TABLE ccu_other(a integer,b integer,CONSTRAINT ccu_other_u UNIQUE(b,a))",
			"CREATE TABLE ccu_part(a integer,b integer,CONSTRAINT ccu_part_u UNIQUE(b,a)) PARTITION BY RANGE(a)",
			"CREATE TABLE ccu_leaf PARTITION OF ccu_part FOR VALUES FROM(0) TO(10)",
			"CREATE SCHEMA ccu_rows_child_schema",
			"CREATE TABLE ccu_rows_child_schema.ccu_cross(x integer,y integer,CONSTRAINT ccu_cross_fk FOREIGN KEY(x,y) REFERENCES public.ccu_parent(b,a))",
		)
		rows := [][]any{
			c.pub("ccu_parent", "a", "ccu_same"), c.pub("ccu_parent", "b", "ccu_same"),
			c.pub("ccu_parent", "a", "ccu_fk"), c.pub("ccu_parent", "b", "ccu_fk"),
			c.pub("ccu_parent", "a", "ccu_fk"), c.pub("ccu_parent", "b", "ccu_fk"),
			c.row("public", "ccu_parent", "a", "ccu_rows_child_schema", "ccu_cross_fk"),
			c.row("public", "ccu_parent", "b", "ccu_rows_child_schema", "ccu_cross_fk"),
			c.pub("ccu_pk", "a", "ccu_pk_key"), c.pub("ccu_pk", "z", "ccu_pk_key"),
			c.pub("ccu_pk", "a", "ccu_pk_a_nn"), c.pub("ccu_pk", "z", "ccu_pk_z_nn"),
			c.pub("ccu_implicit", "a", "ccu_implicit_p"), c.pub("ccu_implicit", "a", "ccu_implicit_a_not_null"),
			c.pub("ccu_checks", "a", "ccu_named_nn"), c.pub("ccu_checks", "a", "ccu_same"), c.pub("ccu_checks", "b", "ccu_same"),
			c.pub("ccu_index_parent", "a", "ccu_index_fk"), c.pub("ccu_index_parent", "b", "ccu_index_fk"),
			c.pub("ccu_other", "a", "ccu_other_u"), c.pub("ccu_other", "b", "ccu_other_u"),
			c.pub("ccu_part", "a", "ccu_part_u"), c.pub("ccu_part", "b", "ccu_part_u"),
			c.pub("ccu_leaf", "a", "ccu_leaf_b_a_key"), c.pub("ccu_leaf", "b", "ccu_leaf_b_a_key"),
		}
		c.check(c.conn, "ccu_%", rows...)
		held := c.hold(c.conn, ccuQuery)
		c.check(held, "ccu_%", rows...)
		bag := c.step("ccu_%", rows)
		bag.Query = strings.Split(ccuQuery, " ORDER BY")[0]
		bag.Bag = true
		bag.CheckScan = nil
		c.run(c.conn, bag)
		c.run(c.conn, fixture.Prepared(bag))
		c.check(held, "ccu_absent")
		c.check(held, nil)
		c.run(c.conn, fixture.Exec("INSERT INTO ccu_none VALUES(1,'unused'),(2,NULL)", 2))
		c.check(c.conn, "ccu_%", rows...)
		c.run(c.conn, fixture.Exec("DELETE FROM ccu_none", 2))
		c.check(c.conn, "ccu_%", rows...)
		q := "SELECT r.relname,p.relname,i.relname,c.conkey::text,c.confkey::text,pg_get_indexdef(c.conindid,1,false) AS key_one,pg_get_indexdef(c.conindid,2,false) AS key_two,pg_get_indexdef(c.conindid,3,false) AS include_three,c.conindid=x.indexrelid AS same_index,c.confrelid=x.indrelid AS same_parent FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_class p ON p.oid=c.confrelid JOIN pg_class i ON i.oid=c.conindid JOIN pg_index x ON x.indexrelid=i.oid WHERE r.relname IN ('ccu_child','ccu_child_two','ccu_index_child') ORDER BY r.relname"
		s := fixture.Select(q, []string{"relname", "relname", "relname", "conkey", "confkey", "key_one", "key_two", "include_three", "same_index", "same_parent"}, []string{"NAME", "NAME", "NAME", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{
			{"ccu_child", "ccu_parent", "ccu_same", "{1,2}", "{3,1}", "a", "b", "", true, true},
			{"ccu_child_two", "ccu_parent", "ccu_same", "{2,1}", "{1,3}", "a", "b", "", true, true},
			{"ccu_index_child", "ccu_index_parent", "ccu_provider", "{1,2}", "{1,2}", "b", "a", "payload", true, true},
		})
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
	})
}
