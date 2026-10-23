package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// These complete candidates are derived from REL_18_6 ruleutils.c source
// namespaces, get_rte_alias and get_rule_windowclause, not native replay SQL.
// The pinned oracle resolves CREATE RULE admission and exact display spelling.
func TestReferencePGRulesSourceDefinitions(t *testing.T) {
	cases := []struct {
		name, ddl, definition string
		quotedDefinition      string
	}{
		{
			name:       "full_join_using_merged_column",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO SELECT id,a.v,b.v FROM pgr_lookup a FULL JOIN pgr_other b USING(id)",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  SELECT id,\n    a.v,\n    b.v\n   FROM (pgr_lookup a\n     FULL JOIN pgr_other b USING (id));",
		},
		{
			name:       "unaliased_ordinary_function",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO SELECT generate_series FROM generate_series(1,2)",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  SELECT generate_series.generate_series\n   FROM generate_series(1, 2) generate_series(generate_series);",
		},
		{
			name:       "unaliased_values",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO SELECT column1 FROM (VALUES(1),(2))",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  SELECT unnamed_subquery.column1\n   FROM ( VALUES (1), (2)) unnamed_subquery;",
		},
		{
			name:       "unaliased_derived_select",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO SELECT id FROM (SELECT id FROM pgr_lookup)",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  SELECT unnamed_subquery.id\n   FROM ( SELECT pgr_lookup.id\n           FROM pgr_lookup) unnamed_subquery;",
		},
		{
			name:       "unaliased_derived_lexical_collision",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO SELECT lookup_id,unnamed_subquery.v FROM pgr_other unnamed_subquery CROSS JOIN (SELECT id AS lookup_id FROM pgr_lookup)",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  SELECT unnamed_subquery_1.lookup_id,\n    unnamed_subquery.v\n   FROM (pgr_other unnamed_subquery\n     CROSS JOIN ( SELECT pgr_lookup.id AS lookup_id\n           FROM pgr_lookup) unnamed_subquery_1);",
		},
		{
			name:       "unaliased_derived_before_explicit_collision",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO SELECT lookup_id,unnamed_subquery.v FROM (SELECT id AS lookup_id FROM pgr_lookup) CROSS JOIN pgr_other unnamed_subquery",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  SELECT unnamed_subquery.lookup_id,\n    unnamed_subquery_1.v\n   FROM (( SELECT pgr_lookup.id AS lookup_id\n           FROM pgr_lookup) unnamed_subquery\n     CROSS JOIN pgr_other unnamed_subquery_1);",
		},
		{
			name:             "named_window_identifiers",
			ddl:              "CREATE RULE r AS ON INSERT TO pgr_src DO SELECT row_number() OVER w FROM pgr_lookup WINDOW w AS(ORDER BY id)",
			definition:       "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  SELECT row_number() OVER w AS row_number\n   FROM pgr_lookup\n  WINDOW w AS (ORDER BY pgr_lookup.id);",
			quotedDefinition: "CREATE RULE \"r\" AS\n    ON INSERT TO \"public\".\"pgr_src\" DO  SELECT \"row_number\"() OVER \"w\" AS \"row_number\"\n   FROM \"pgr_lookup\"\n  WINDOW \"w\" AS (ORDER BY \"pgr_lookup\".\"id\");",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
					for _, q := range []string{
						"CREATE TABLE pgr_src(id INT,v INT)",
						"CREATE TABLE pgr_log(id INT,v INT)",
						"CREATE TABLE pgr_lookup(id INT,v INT)",
						"CREATE TABLE pgr_other(id INT,v INT)",
						c.ddl,
					} {
						run(fixture.Exec(q, 0))
					}
					check := func(definition string) {
						pgRulesCheck(t, p, b, conn, pgRulesProbe, [][]any{{"public", "pgr_src", "r", definition}}, "public", "pgr_src", "r")
					}
					check(c.definition)
					if c.quotedDefinition != "" {
						run(fixture.Exec("SET quote_all_identifiers TO on", 0))
						check(c.quotedDefinition)
						run(fixture.Exec("RESET quote_all_identifiers", 0))
						check(c.definition)
					}
					// Catalog reads must not execute the event or alter any action source.
					for _, table := range []string{"pgr_src", "pgr_log", "pgr_lookup", "pgr_other"} {
						run(fixture.Select("SELECT count(*) FROM "+table, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					}
				})
			}
		})
	}
}
