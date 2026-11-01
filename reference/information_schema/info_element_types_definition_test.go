package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Only independently specified lookup effects transform the literal display:
// domains/helper become visible on the information_schema path, or actual
// shadowed objects require qualification. No output under test builds a golden.
func etDefinitionFor(path string, quote bool) string {
	want := etDefinition
	if quote {
		want = etQuoteDefinition
	}
	if path == "information_schema" {
		if quote {
			return strings.ReplaceAll(want, `"information_schema".`, "")
		}
		return strings.ReplaceAll(want, "information_schema.", "")
	}
	if path != "shadow" {
		return want
	}
	for _, name := range []string{"pg_namespace", "pg_type", "pg_class", "pg_attribute", "pg_proc", "pg_collation"} {
		for _, prefix := range []string{"FROM ", "JOIN ", "    ", "("} {
			if quote {
				want = strings.ReplaceAll(want, prefix+`"`+name+`" `, prefix+`"pg_catalog"."`+name+`" `)
			} else {
				want = strings.ReplaceAll(want, prefix+name+" ", prefix+"pg_catalog."+name+" ")
			}
		}
	}
	for _, name := range []string{"current_database", "format_type", "nameconcatoid"} {
		if quote {
			want = strings.ReplaceAll(want, `"`+name+`"(`, `"pg_catalog"."`+name+`"(`)
		} else {
			want = strings.ReplaceAll(want, name+"(", "pg_catalog."+name+"(")
		}
	}
	return want
}

// The generic builtin inventory shadows only these three catalog relations,
// unlike the feature scenario's additional type/collation/routine shadows.
func etInventoryShadowDefinition(quote bool) string {
	want := etDefinition
	if quote {
		want = etQuoteDefinition
	}
	for _, name := range []string{"pg_namespace", "pg_class", "pg_attribute"} {
		if quote {
			want = strings.ReplaceAll(want, `"`+name+`" `, `"pg_catalog"."`+name+`" `)
		} else {
			want = strings.ReplaceAll(want, name+" ", "pg_catalog."+name+" ")
		}
	}
	return want
}

func TestReferenceInfoElementTypesDefinition(t *testing.T) {
	etScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		etExec(t, p, b, c,
			"CREATE ROLE et_reader", "CREATE SCHEMA et_shadow",
			"GRANT USAGE ON SCHEMA et_shadow TO et_reader",
			"CREATE TABLE et_shadow.pg_namespace(v integer)", "CREATE TABLE et_shadow.pg_type(v integer)",
			"CREATE TABLE et_shadow.pg_class(v integer)", "CREATE TABLE et_shadow.pg_attribute(v integer)",
			"CREATE TABLE et_shadow.pg_proc(v integer)", "CREATE TABLE et_shadow.pg_collation(v integer)",
			"CREATE FUNCTION et_shadow.current_database() RETURNS name LANGUAGE SQL AS $$SELECT 'shadow'::name$$",
			"CREATE FUNCTION et_shadow.format_type(oid,integer) RETURNS text LANGUAGE SQL AS $$SELECT 'shadow'::text$$",
			"CREATE FUNCTION et_shadow.nameconcatoid(name,oid) RETURNS name LANGUAGE SQL AS $$SELECT 'shadow'::name$$",
		)
		queries := []string{
			"SELECT pg_catalog.pg_get_viewdef('information_schema.element_types'::regclass) AS definition",
			"SELECT definition FROM pg_catalog.pg_views WHERE schemaname='information_schema' AND viewname='element_types'",
		}
		var held []func(fixture.Step)
		for _, query := range queries {
			held = append(held, etHeld(t, p, b, c, query))
		}
		for _, path := range []struct{ name, setting string }{
			{"default", "public"}, {"information_schema", "public,information_schema,pg_catalog"}, {"shadow", "et_shadow,pg_catalog,public"},
		} {
			for _, quote := range []struct {
				name string
				on   bool
			}{{"off", false}, {"on", true}} {
				t.Run(path.name+"/quote_"+quote.name, func(t *testing.T) {
					etExec(t, p, b, c, "SET search_path="+path.setting, "SET quote_all_identifiers="+quote.name)
					want := etDefinitionFor(path.name, quote.on)
					for i, query := range queries {
						s := fixture.Select(query, []string{"definition"}, []string{"TEXT"}, [][]any{{want}})
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
						held[i](s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT v.definition=pg_catalog.pg_get_viewdef('information_schema.element_types'::regclass) AS same_definition FROM pg_catalog.pg_views v WHERE v.schemaname='information_schema' AND v.viewname='element_types'`, []string{"same_definition"}, []string{"BOOL"}, [][]any{{true}}))
					etExec(t, p, b, c, "SET ROLE et_reader")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{"et_reader"}}))
					for i, query := range queries {
						held[i](fixture.Select(query, []string{"definition"}, []string{"TEXT"}, [][]any{{want}}))
					}
					etExec(t, p, b, c, "RESET ROLE")
				})
			}
		}
	})
}
