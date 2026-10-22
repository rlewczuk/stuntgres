package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoKeyColumnUsageRows(t *testing.T) {
	kcuScenario(t, func(c *kcuCase) {
		c.exec(
			"CREATE TABLE kcu_parent(a integer,dead text,b integer,CONSTRAINT kcu_u UNIQUE(a,b))",
			"ALTER TABLE kcu_parent DROP COLUMN dead",
			"CREATE TABLE kcu_pk(z integer,a integer,CONSTRAINT kcu_pk_key PRIMARY KEY(a,z))",
			"CREATE TABLE kcu_child(x integer,y integer,CONSTRAINT kcu_fk FOREIGN KEY(x,y) REFERENCES kcu_parent(b,a))",
			"CREATE TABLE kcu_child_two(x integer,y integer,CONSTRAINT kcu_fk FOREIGN KEY(y,x) REFERENCES kcu_parent(a,b))",
			"CREATE TABLE kcu_index_parent(a integer,b integer,payload text)",
			"CREATE UNIQUE INDEX kcu_provider ON kcu_index_parent(b,a) INCLUDE(payload)",
			"CREATE TABLE kcu_index_child(x integer,y integer,CONSTRAINT kcu_index_fk FOREIGN KEY(x,y) REFERENCES kcu_index_parent(a,b))",
			"CREATE TABLE kcu_none(a integer NOT NULL,CONSTRAINT kcu_check CHECK(a>0))",
			"CREATE TABLE kcu_exclusion(a integer,CONSTRAINT kcu_ex EXCLUDE USING btree(a WITH =))",
			"CREATE DOMAIN kcu_domain AS integer CHECK(VALUE>0)",
			"CREATE TABLE kcu_domain_table(a kcu_domain)",
			"CREATE TABLE kcu_part(a integer,b integer,CONSTRAINT kcu_part_key UNIQUE(b,a)) PARTITION BY RANGE(a)",
			"CREATE TABLE kcu_leaf PARTITION OF kcu_part FOR VALUES FROM(0) TO(10)",
		)
		rows := [][]any{
			c.pub("kcu_u", "kcu_parent", "a", 1, nil), c.pub("kcu_u", "kcu_parent", "b", 2, nil),
			c.pub("kcu_pk_key", "kcu_pk", "a", 1, nil), c.pub("kcu_pk_key", "kcu_pk", "z", 2, nil),
			c.pub("kcu_fk", "kcu_child", "x", 1, int64(2)), c.pub("kcu_fk", "kcu_child", "y", 2, int64(1)),
			c.pub("kcu_fk", "kcu_child_two", "y", 1, int64(1)), c.pub("kcu_fk", "kcu_child_two", "x", 2, int64(2)),
			c.pub("kcu_index_fk", "kcu_index_child", "x", 1, int64(2)), c.pub("kcu_index_fk", "kcu_index_child", "y", 2, int64(1)),
			c.pub("kcu_part_key", "kcu_part", "b", 1, nil), c.pub("kcu_part_key", "kcu_part", "a", 2, nil),
			c.pub("kcu_leaf_b_a_key", "kcu_leaf", "b", 1, nil), c.pub("kcu_leaf_b_a_key", "kcu_leaf", "a", 2, nil),
		}
		c.check(c.conn, "kcu_%", rows...)
		held := c.hold(c.conn, kcuQuery)
		c.check(held, "kcu_%", rows...)
		bag := c.step("kcu_%", rows)
		bag.Query = strings.Split(kcuQuery, " ORDER BY")[0]
		bag.Bag = true
		bag.CheckScan = nil
		c.run(c.conn, bag)
		c.run(c.conn, fixture.Prepared(bag))
		c.check(held, "kcu_absent")
		c.check(held, nil)
		c.run(c.conn, fixture.Exec("INSERT INTO kcu_none VALUES(1),(2)", 2))
		c.check(c.conn, "kcu_%", rows...)
		c.run(c.conn, fixture.Exec("DELETE FROM kcu_none", 2))
		c.check(c.conn, "kcu_%", rows...)
		q := "SELECT r.relname,p.relname,i.relname,c.conkey::text,c.confkey::text,pg_get_indexdef(c.conindid,1,false) AS key_one,pg_get_indexdef(c.conindid,2,false) AS key_two,pg_get_indexdef(c.conindid,3,false) AS include_three,n.nspname,c.conindid=x.indexrelid AS same_index,c.confrelid=x.indrelid AS same_parent FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_class p ON p.oid=c.confrelid JOIN pg_class i ON i.oid=c.conindid JOIN pg_index x ON x.indexrelid=i.oid JOIN pg_namespace n ON n.oid=p.relnamespace WHERE r.relname IN ('kcu_child','kcu_child_two','kcu_index_child') AND n.nspname='public' ORDER BY r.relname"
		c.run(c.conn, fixture.Select(q, []string{"relname", "relname", "relname", "conkey", "confkey", "key_one", "key_two", "include_three", "nspname", "same_index", "same_parent"}, []string{"NAME", "NAME", "NAME", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "NAME", "BOOL", "BOOL"}, [][]any{
			{"kcu_child", "kcu_parent", "kcu_u", "{1,2}", "{3,1}", "a", "b", "", "public", true, true},
			{"kcu_child_two", "kcu_parent", "kcu_u", "{2,1}", "{1,3}", "a", "b", "", "public", true, true},
			{"kcu_index_child", "kcu_index_parent", "kcu_provider", "{1,2}", "{1,2}", "b", "a", "payload", "public", true, true},
		}))
	})
}
