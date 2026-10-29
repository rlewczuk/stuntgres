package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeparsePartitionCurrentNames(t *testing.T) {
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE TABLE dp_names(k INT,txt TEXT) PARTITION BY RANGE(k)",
			"CREATE TABLE dp_names_leaf PARTITION OF dp_names FOR VALUES FROM(0) TO(10)",
			"CREATE TABLE dp_expr(k INT) PARTITION BY RANGE((k+1))",
			"CREATE TABLE dp_expr_leaf PARTITION OF dp_expr FOR VALUES FROM(0) TO(10)",
		} {
			run(fixture.Exec(q, 0))
		}
		for _, tc := range []struct{ table, function, before, after string }{
			{"dp_names", "pg_get_partkeydef", "RANGE (k)", `RANGE ("A"" select")`},
			{"dp_names_leaf", "pg_get_partition_constraintdef", "((k IS NOT NULL) AND (k >= 0) AND (k < 10))", `(("A"" select" IS NOT NULL) AND ("A"" select" >= 0) AND ("A"" select" < 10))`},
			{"dp_expr", "pg_get_partkeydef", "RANGE (((k + 1)))", `RANGE ((("A"" select" + 1)))`},
			{"dp_expr_leaf", "pg_get_partition_constraintdef", "(((k + 1) IS NOT NULL) AND ((k + 1) >= 0) AND ((k + 1) < 10))", `((("A"" select" + 1) IS NOT NULL) AND (("A"" select" + 1) >= 0) AND (("A"" select" + 1) < 10))`},
		} {
			t.Run(tc.table, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				id := fixture.DeparseDefinitionID(t, c, "SELECT '"+tc.table+"'::regclass::oid")
				q := "SELECT " + tc.function + "($1) AS definition"
				s, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				e := fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
				check := func(want string) {
					step := fixture.DeparseRelationsTextStep(t, b, q, want, id)
					fixture.RunStep(t, p.Ctx, e, b.Recorder, step)
					run(step)
				}
				check(tc.before)
				parent := "dp_names"
				if tc.table == "dp_expr" || tc.table == "dp_expr_leaf" {
					parent = "dp_expr"
				}
				run(fixture.Exec(`ALTER TABLE `+parent+` RENAME k TO "A"" select"`, 0))
				check(tc.after)
				if got := fixture.DeparseDefinitionID(t, c, "SELECT '"+tc.table+"'::regclass::oid"); got != id {
					t.Fatal("rename changed relation identity")
				}
				run(fixture.Exec(`ALTER TABLE `+parent+` RENAME "A"" select" TO k`, 0))
			})
		}
		run(fixture.Exec("SET quote_all_identifiers=on", 0))
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partkeydef('dp_names'::regclass) AS definition", `RANGE ("k")`))
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partition_constraintdef('dp_names_leaf'::regclass) AS definition", `(("k" IS NOT NULL) AND ("k" >= 0) AND ("k" < 10))`))
		run(fixture.Exec("RESET quote_all_identifiers", 0))
		for _, q := range []string{
			"CREATE TABLE dp_attach_parent(k INT,v INT) PARTITION BY RANGE((k+1))",
			"CREATE TABLE dp_attach_child(v INT,k INT) PARTITION BY RANGE(v)",
			"CREATE TABLE dp_attach_leaf(k INT,v INT)",
			"ALTER TABLE dp_attach_child ATTACH PARTITION dp_attach_leaf FOR VALUES FROM(7) TO(8)",
			"ALTER TABLE dp_attach_parent ATTACH PARTITION dp_attach_child FOR VALUES FROM(1) TO(11)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partition_constraintdef('dp_attach_leaf'::regclass) AS definition", "(((k + 1) IS NOT NULL) AND ((k + 1) >= 1) AND ((k + 1) < 11) AND (v IS NOT NULL) AND (v >= 7) AND (v < 8))"))
		for _, q := range []string{
			"CREATE OPERATOR public.## (LEFTARG=integer,RIGHTARG=integer,FUNCTION=pg_catalog.int4ge)",
			"CREATE OPERATOR CLASS public.dp_class FOR TYPE integer USING btree AS OPERATOR 1 pg_catalog.<,OPERATOR 2 pg_catalog.<=,OPERATOR 3 pg_catalog.=,OPERATOR 4 public.##,OPERATOR 5 pg_catalog.>,FUNCTION 1 pg_catalog.btint4cmp(integer,integer)",
			"CREATE COLLATION public.dp_coll FROM pg_catalog.\"C\"",
			"CREATE TABLE dp_authority(k INT,t TEXT) PARTITION BY RANGE(k public.dp_class)",
			"CREATE TABLE dp_authority_leaf PARTITION OF dp_authority FOR VALUES FROM(1) TO(4)",
			"CREATE TABLE dp_collated(t TEXT) PARTITION BY LIST(t COLLATE public.dp_coll)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partkeydef('dp_authority'::regclass) AS definition", "RANGE (k dp_class)"))
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partition_constraintdef('dp_authority_leaf'::regclass) AS definition", "((k IS NOT NULL) AND (k ## 1) AND (k < 4))"))
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partkeydef('dp_collated'::regclass) AS definition", "LIST (t COLLATE dp_coll)"))
		run(fixture.Exec("SET search_path=pg_catalog", 0))
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partkeydef('public.dp_authority'::regclass) AS definition", "RANGE (k public.dp_class)"))
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partition_constraintdef('public.dp_authority_leaf'::regclass) AS definition", "((k IS NOT NULL) AND (k OPERATOR(public.##) 1) AND (k < 4))"))
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partkeydef('public.dp_collated'::regclass) AS definition", "LIST (t COLLATE public.dp_coll)"))
		run(fixture.Exec("RESET search_path", 0))
		for _, q := range []string{
			"CREATE TYPE dp_color AS ENUM('red','green')",
			"CREATE DOMAIN dp_id AS BIGINT",
			"CREATE TABLE dp_enum(k dp_color) PARTITION BY LIST(k)",
			"CREATE TABLE dp_enum_leaf PARTITION OF dp_enum FOR VALUES IN('green')",
			"CREATE TABLE dp_domain(k dp_id) PARTITION BY LIST(k)",
			"CREATE TABLE dp_domain_leaf PARTITION OF dp_domain FOR VALUES IN(9)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partition_constraintdef('dp_enum_leaf'::regclass) AS definition", "((k IS NOT NULL) AND (k = 'green'::dp_color))"))
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partition_constraintdef('dp_domain_leaf'::regclass) AS definition", "((k IS NOT NULL) AND ((k)::bigint = '9'::dp_id))"))
	})
}

func TestReferenceDeparsePartitionHashPresentation(t *testing.T) {
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE dp_hash_names(k INT) PARTITION BY HASH(k)", 0))
		run(fixture.Exec("CREATE TABLE dp_hash_names_leaf PARTITION OF dp_hash_names FOR VALUES WITH(MODULUS 4,REMAINDER 1)", 0))
		parent := fixture.DeparseDefinitionID(t, c, "SELECT 'dp_hash_names'::regclass::oid")
		leaf := fixture.DeparseDefinitionID(t, c, "SELECT 'dp_hash_names_leaf'::regclass::oid")
		query := "SELECT pg_get_partition_constraintdef($1) AS definition"
		statement, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
		check := func(phase, function, typ, key string) {
			t.Helper()
			if !t.Run(phase, func(t *testing.T) {
				want := fmt.Sprintf("%s('%d'::%s, 4, 1, %s)", function, parent, typ, key)
				step := fixture.DeparseRelationsTextStep(t, b, query, want, leaf)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			}) {
				t.FailNow()
			}
		}
		check("initial", "satisfies_hash_partition", "oid", "k")
		run(fixture.Exec("SET quote_all_identifiers=on", 0))
		check("quoted_initial", `"satisfies_hash_partition"`, `"oid"`, `"k"`)
		run(fixture.Exec("RESET quote_all_identifiers", 0))
		run(fixture.Exec("CREATE FUNCTION public.satisfies_hash_partition(oid,integer,integer,integer) RETURNS boolean LANGUAGE SQL AS $$SELECT false$$", 0))
		run(fixture.Exec("SET search_path=public,pg_catalog", 0))
		check("function_shadow_public_first", "pg_catalog.satisfies_hash_partition", "oid", "k")
		run(fixture.Exec("SET quote_all_identifiers=on", 0))
		check("quoted_function_shadow_public_first", `"pg_catalog"."satisfies_hash_partition"`, `"oid"`, `"k"`)
		run(fixture.Exec("RESET quote_all_identifiers", 0))
		run(fixture.Exec("SET search_path=pg_catalog,public", 0))
		check("function_shadow_catalog_first", "pg_catalog.satisfies_hash_partition", "oid", "k")
		run(fixture.Exec("DROP FUNCTION public.satisfies_hash_partition(oid,integer,integer,integer)", 0))
		run(fixture.Exec("CREATE DOMAIN public.oid AS integer", 0))
		run(fixture.Exec("SET search_path=public,pg_catalog", 0))
		check("type_shadow_public_first", "satisfies_hash_partition", "pg_catalog.oid", "k")
		run(fixture.Exec("SET quote_all_identifiers=on", 0))
		check("quoted_type_shadow_public_first", `"satisfies_hash_partition"`, `"pg_catalog"."oid"`, `"k"`)
		run(fixture.Exec("RESET quote_all_identifiers", 0))
		run(fixture.Exec("CREATE FUNCTION public.satisfies_hash_partition(pg_catalog.oid,integer,integer,integer) RETURNS boolean LANGUAGE SQL AS $$SELECT false$$", 0))
		check("combined_shadows_public_first", "pg_catalog.satisfies_hash_partition", "pg_catalog.oid", "k")
		run(fixture.Exec("SET quote_all_identifiers=on", 0))
		check("quoted_combined_shadows_public_first", `"pg_catalog"."satisfies_hash_partition"`, `"pg_catalog"."oid"`, `"k"`)
		run(fixture.Exec("RESET quote_all_identifiers", 0))
		run(fixture.Exec("SET search_path=pg_catalog,public", 0))
		check("combined_shadows_catalog_first", "pg_catalog.satisfies_hash_partition", "oid", "k")
	})
}

func TestReferenceDeparsePartitionCollationPresentation(t *testing.T) {
	for _, tc := range []struct {
		name, strategy, bound, constraint string
	}{
		{"list", "LIST", "IN('a')", "((%[1]s IS NOT NULL) AND ((%[1]s)::%[2]s = 'a'::%[2]s COLLATE %[3]s))"},
		{"range", "RANGE", "FROM('a') TO('b')", "((%[1]s IS NOT NULL) AND ((%[1]s)::%[2]s >= 'a'::%[2]s COLLATE %[3]s) AND ((%[1]s)::%[2]s < 'b'::%[2]s COLLATE %[3]s))"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(`CREATE COLLATION public.dp_coll FROM pg_catalog."C"`, 0))
				run(fixture.Exec("CREATE TABLE dp_collated(t TEXT) PARTITION BY "+tc.strategy+"(t COLLATE public.dp_coll)", 0))
				run(fixture.Exec("CREATE TABLE dp_collated_leaf PARTITION OF dp_collated FOR VALUES "+tc.bound, 0))
				leaf := fixture.DeparseDefinitionID(t, c, "SELECT 'dp_collated_leaf'::regclass::oid")
				query := "SELECT pg_get_partition_constraintdef($1) AS definition"
				statement, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer statement.Close()
				held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
				check := func(phase, key, typ, collation string) {
					t.Helper()
					if !t.Run(phase, func(t *testing.T) {
						want := fmt.Sprintf(tc.constraint, key, typ, collation)
						step := fixture.DeparseRelationsTextStep(t, b, query, want, leaf)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
					}) {
						t.FailNow()
					}
				}
				run(fixture.Exec("SET search_path=public,pg_catalog", 0))
				check("visible", "t", "text", "dp_coll")
				run(fixture.Exec("SET quote_all_identifiers=on", 0))
				check("quoted_visible", `"t"`, `"text"`, `"dp_coll"`)
				run(fixture.Exec("SET search_path=pg_catalog", 0))
				check("quoted_hidden", `"t"`, `"text"`, `"public"."dp_coll"`)
				run(fixture.Exec("RESET quote_all_identifiers", 0))
				check("hidden", "t", "text", "public.dp_coll")
				run(fixture.Exec("SET search_path=public,pg_catalog", 0))
				check("visible_again", "t", "text", "dp_coll")
			})
		})
	}
}
