package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectAddressAuthorityRelations(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE TABLE addr_rel(a INT,b TEXT)",
			"CREATE INDEX addr_idx ON addr_rel(a)",
			"CREATE TABLE addr_part(a INT) PARTITION BY RANGE(a)",
			"CREATE TABLE addr_child PARTITION OF addr_part FOR VALUES FROM (0) TO (10)",
			"CREATE SEQUENCE addr_seq",
			"CREATE VIEW addr_view AS SELECT a,b FROM addr_rel",
			"CREATE MATERIALIZED VIEW addr_mat AS SELECT a FROM addr_rel",
			"CREATE TYPE addr_composite AS (a INT,b TEXT)",
		} {
			objectAddressRun(t, p, b, c, fixture.Exec(q, 0))
		}
		partitionIndexSQL := "CREATE INDEX addr_part_idx ON addr_part(a)"
		if b.PG {
			objectAddressRun(t, p, b, c, fixture.Exec(partitionIndexSQL, 0))
		} else {
			// The address owner supports existing indexes; partitioned-index
			// creation is a separate, explicitly unsupported native DDL route.
			s := fixture.ImplicitError(partitionIndexSQL, fixture.Diagnostic{Code: "0A000", Message: "partitioned indexes are unsupported"})
			s.ErrorStage = "execute"
			objectAddressRun(t, p, b, c, s)
			objectAddressRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='addr_part_idx'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		for _, tc := range []struct{ name, kind string }{
			{"addr_rel", "table"}, {"addr_part", "table"}, {"addr_child", "table"}, {"addr_idx", "index"}, {"addr_part_idx", "index"}, {"addr_seq", "sequence"}, {"addr_view", "view"}, {"addr_mat", "materialized view"}, {"addr_composite", "composite type"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if tc.name == "addr_part_idx" && !b.PG {
					return // Native capability/atomicity is asserted above.
				}
				// Composite relations use output-only kind text; inverse type is
				// tested separately in AuthorityTypes, not faked as table lookup.
				if tc.kind == "composite type" {
					var id int64
					if err := c.QueryRowContext(p.Ctx, "SELECT 'addr_composite'::regclass::oid").Scan(&id); err != nil {
						t.Fatal(err)
					}
					objectAddressNumeric(t, p, b, c, objectAddressValue{1259, id, 0}, tc.kind, "public", tc.name, "public."+tc.name, "{public,"+tc.name+"}", "{}", "composite type "+tc.name)
					return
				}
				objectAddressWitness(t, p, b, c, 1259, tc.kind, []string{"public", tc.name}, nil, "public", tc.name, "public."+tc.name, tc.kind+" "+tc.name, "SELECT '"+tc.name+"'::regclass::oid")
			})
		}
		objectAddressWitness(t, p, b, c, 1259, "table", []string{"pg_catalog", "pg_class"}, nil, "pg_catalog", "pg_class", "pg_catalog.pg_class", "table pg_class", "SELECT 1259")
		for _, tc := range []struct {
			sub  int64
			name string
		}{{-1, "ctid"}, {-2, "xmin"}, {-3, "cmin"}, {-4, "xmax"}, {-5, "cmax"}, {-6, "tableoid"}, {1, "a"}, {2, "b"}} {
			a := objectAddressResolve(t, p, c, "table column", []string{"public", "addr_rel", tc.name}, nil)
			if a.sub != tc.sub {
				t.Fatal("attribute authority", a, tc)
			}
			objectAddressNumeric(t, p, b, c, a, "table column", "public", "addr_rel", "public.addr_rel."+tc.name, "{public,addr_rel,"+tc.name+"}", "{}", "column "+tc.name+" of table addr_rel")
		}
		for _, tc := range []struct{ table, kind, col string }{{"addr_view", "view column", "a"}, {"addr_mat", "materialized view column", "a"}, {"addr_idx", "index column", "a"}, {"addr_seq", "sequence column", "last_value"}} {
			var id int64
			if err := c.QueryRowContext(p.Ctx, "SELECT '"+tc.table+"'::regclass::oid").Scan(&id); err != nil {
				t.Fatal(err)
			}
			objectAddressNumeric(t, p, b, c, objectAddressValue{1259, id, 1}, tc.kind, "public", tc.table, "public."+tc.table+"."+tc.col, "{public,"+tc.table+","+tc.col+"}", "{}", "column "+tc.col+" of "+map[string]string{"addr_view": "view", "addr_mat": "materialized view", "addr_idx": "index", "addr_seq": "sequence"}[tc.table]+" "+tc.table)
		}
		objectAddressRun(t, p, b, c, fixture.Exec("CREATE TEMP SEQUENCE addr_temp", 0))
		var nsID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT relnamespace FROM pg_class WHERE oid='addr_temp'::regclass::oid").Scan(&nsID); err != nil {
			t.Fatal(err)
		}
		var ns string
		if err := c.QueryRowContext(p.Ctx, "SELECT nspname FROM pg_namespace WHERE oid=$1::oid", nsID).Scan(&ns); err != nil {
			t.Fatal(err)
		}
		actual := objectAddressResolve(t, p, c, "schema", []string{ns}, nil)
		if actual != (objectAddressValue{2615, nsID, 0}) {
			t.Fatal("temporary namespace ownership", actual, nsID)
		}
		objectAddressNumeric(t, p, b, c, actual, "schema", nil, ns, "pg_temp", "{pg_temp}", "{}", "schema "+ns)
		objectAddressDiagnostics(t, p, b, c, []objectAddressDiagnosticCase{{"temporary_alias_inverse", "SELECT pg_get_object_address('schema','{pg_temp}','{}')", fixture.Diagnostic{Code: "3F000", Message: `schema "pg_temp" does not exist`}, true}})
	})
}

func TestReferenceObjectAddressAuthorityDefinitions(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE TABLE addr_parent(id INT CONSTRAINT addr_pk PRIMARY KEY)",
			"CREATE TABLE addr_defs(a INT CONSTRAINT addr_nn NOT NULL,b INT DEFAULT 3,c INT GENERATED ALWAYS AS (b+1) STORED,d INT GENERATED ALWAYS AS (b+2) VIRTUAL,CONSTRAINT addr_check CHECK(a>0),CONSTRAINT addr_unique UNIQUE(a),CONSTRAINT addr_fk FOREIGN KEY(a) REFERENCES addr_parent(id),CONSTRAINT addr_excl EXCLUDE USING btree(b WITH =))",
			"CREATE DOMAIN addr_domain AS INT CONSTRAINT addr_domain_check CHECK(VALUE>0)",
			"CREATE RULE addr_rule AS ON INSERT TO addr_defs DO ALSO NOTHING",
			"CREATE POLICY addr_policy ON addr_defs USING (a>0)",
			"CREATE VIEW addr_def_view AS SELECT a FROM addr_defs",
			"CREATE TABLE addr_inherit(a INT CONSTRAINT addr_inherited_check CHECK(a>0))",
			"CREATE TABLE addr_inherited() INHERITS(addr_inherit)",
			"CREATE TABLE addr_partition(a INT CONSTRAINT addr_partition_check CHECK(a>0)) PARTITION BY RANGE(a)",
			"CREATE TABLE addr_partition_child PARTITION OF addr_partition FOR VALUES FROM(0) TO(10)",
		} {
			objectAddressRun(t, p, b, c, fixture.Exec(q, 0))
		}
		for _, name := range []string{"addr_nn", "addr_check", "addr_unique", "addr_fk", "addr_excl"} {
			objectAddressWitness(t, p, b, c, 2606, "table constraint", []string{"public", "addr_defs", name}, nil, "public", nil, name+" on public.addr_defs", "constraint "+name+" on table addr_defs", "SELECT oid FROM pg_constraint WHERE conrelid='addr_defs'::regclass::oid AND conname='"+name+"'")
		}
		for _, tc := range []struct{ table, name string }{{"addr_parent", "addr_pk"}, {"addr_inherit", "addr_inherited_check"}, {"addr_inherited", "addr_inherited_check"}, {"addr_partition", "addr_partition_check"}, {"addr_partition_child", "addr_partition_check"}} {
			objectAddressWitness(t, p, b, c, 2606, "table constraint", []string{"public", tc.table, tc.name}, nil, "public", nil, tc.name+" on public."+tc.table, "constraint "+tc.name+" on table "+tc.table, "SELECT oid FROM pg_constraint WHERE conrelid='"+tc.table+"'::regclass::oid AND conname='"+tc.name+"'")
		}
		objectAddressWitness(t, p, b, c, 2606, "domain constraint", []string{"public.addr_domain"}, []string{"addr_domain_check"}, "public", nil, "addr_domain_check on public.addr_domain", "constraint addr_domain_check", "")
		for i, col := range []string{"b", "c", "d"} {
			objectAddressWitness(t, p, b, c, 2604, "default value", []string{"public", "addr_defs", col}, nil, nil, nil, "for public.addr_defs."+col, "default value for column "+col+" of table addr_defs", fmt.Sprintf("SELECT oid FROM pg_attrdef WHERE adrelid='addr_defs'::regclass::oid AND adnum=%d", i+2))
		}
		objectAddressWitness(t, p, b, c, 2618, "rule", []string{"public", "addr_defs", "addr_rule"}, nil, nil, nil, "addr_rule on public.addr_defs", "rule addr_rule on table addr_defs", "SELECT oid FROM pg_rewrite WHERE ev_class='addr_defs'::regclass::oid AND rulename='addr_rule'")
		objectAddressWitness(t, p, b, c, 2618, "rule", []string{"public", "addr_def_view", "_RETURN"}, nil, nil, nil, `"_RETURN" on public.addr_def_view`, `rule _RETURN on view addr_def_view`, "SELECT oid FROM pg_rewrite WHERE ev_class='addr_def_view'::regclass::oid AND rulename='_RETURN'")
		policyOID := ""
		if b.PG {
			policyOID = "SELECT oid FROM pg_policy WHERE polrelid='addr_defs'::regclass::oid AND polname='addr_policy'"
		}
		objectAddressWitness(t, p, b, c, 3256, "policy", []string{"public", "addr_defs", "addr_policy"}, nil, nil, nil, "addr_policy on public.addr_defs", "policy addr_policy on table addr_defs", policyOID)
	})
}
