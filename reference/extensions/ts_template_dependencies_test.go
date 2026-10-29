package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTSTemplateDependencies(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		templateDefinition(t, p, c, r, "public.template_dep")
		templateDefinition(t, p, c, r, "public.template_unused")
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY public.template_dep_dict (TEMPLATE=public.template_dep,STOPWORDS=english)", 0))
		id := templateIdentity(t, p, c, "template_dep")
		// recordMultipleDependencies omits references to pinned stock callbacks.
		// Their executable identity links remain in pg_ts_template.
		edges := fixture.Select("SELECT refclassid::bigint,refobjid::bigint,deptype FROM pg_depend WHERE classid=3764 AND objid=$1 ORDER BY refclassid,refobjid", []string{"refclassid","refobjid","deptype"}, []string{"INT8","INT8","CHAR"}, [][]any{{int64(2615),id.namespace,"n"}}, id.oid)
		run(edges)
		run(fixture.Prepared(edges))
		fixture.TSTemplateRestrict(t, p, c, r, "template_dep", "template_dep_dict")
		templateDiagnostic(t, p, c, r, "DROP TEXT SEARCH TEMPLATE template_unused,template_missing CASCADE", "42704", `text search template "template_missing" does not exist`)
		_ = templateIdentity(t, p, c, "template_unused")
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.template_dep_config (PARSER=pg_catalog.default)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.template_dep_config ADD MAPPING FOR asciiword WITH public.template_dep_dict", 0))
		run(fixture.Exec("CREATE TABLE public.template_dep_table (d regdictionary DEFAULT 'public.template_dep_dict')", 0))
		run(fixture.Exec("CREATE VIEW public.template_dep_view AS SELECT 'public.template_dep_dict'::regdictionary AS d", 0))
		run(fixture.Exec("DROP TEXT SEARCH TEMPLATE public.template_dep CASCADE", 0))
		templateAbsent(t, p, c, r, "template_dep")
		run(fixture.Select("SELECT count(*) FROM pg_ts_dict WHERE dictname='template_dep_dict'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT count(*) FROM pg_ts_config_map m JOIN pg_ts_config c ON c.oid=m.mapcfg WHERE c.cfgname='template_dep_config'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='template_dep_view'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("INSERT INTO public.template_dep_table DEFAULT VALUES", 1))
		run(fixture.Select("SELECT d FROM public.template_dep_table", []string{"d"}, []string{"REGDICTIONARY"}, [][]any{{nil}}))
		// Released dependency.c::findDependentObjects visits descending OIDs
		// and retains the first reached parent of a shared dependent. A full
		// callback pair therefore makes the reported helper depend on member
		// allocation order. Isolate each real helper for exact RESTRICT
		// diagnostics; retain the executable pair and dictionary for CASCADE.
		run(fixture.Exec("CREATE SCHEMA ext_addon", 0))
		run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA ext_addon", 0))
		for _, probe := range []struct {
			name, options, function string
		}{
			{"template_lexize_dep", "LEXIZE=ext_addon.unaccent_lexize", "ext_addon.unaccent_lexize(internal,internal,internal,internal)"},
			{"template_init_dep", "INIT=ext_addon.unaccent_init,LEXIZE=pg_catalog.dsimple_lexize", "ext_addon.unaccent_init(internal)"},
		} {
			// These are definition-only probes: never execute no-INIT or
			// mixed-provider callback combinations on either backend.
			run(fixture.Exec("CREATE TEXT SEARCH TEMPLATE public."+probe.name+" ("+probe.options+")", 0))
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("DROP EXTENSION unaccent RESTRICT", fixture.Diagnostic{Code:"2BP01", Message:"cannot drop extension unaccent because other objects depend on it", Detail:"text search template "+probe.name+" depends on function "+probe.function, Hint:"Use DROP ... CASCADE to drop the dependent objects too."})
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
			}
			_ = templateIdentity(t, p, c, probe.name)
			run(fixture.Exec("DROP TEXT SEARCH TEMPLATE public."+probe.name, 0))
		}
		run(fixture.Exec("CREATE TEXT SEARCH TEMPLATE public.template_helper_dep (INIT=ext_addon.unaccent_init,LEXIZE=ext_addon.unaccent_lexize)", 0))
		helper := templateIdentity(t, p, c, "template_helper_dep")
		edges = fixture.Select("SELECT refclassid::bigint,refobjid::bigint,deptype FROM pg_depend WHERE classid=3764 AND objid=$1", []string{"refclassid","refobjid","deptype"}, []string{"INT8","INT8","CHAR"}, [][]any{{int64(1255),helper.init,"n"},{int64(1255),helper.lexize,"n"},{int64(2615),helper.namespace,"n"}}, helper.oid)
		edges.Bag = true
		run(edges)
		run(fixture.Prepared(edges))
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY public.template_helper_dict (TEMPLATE=public.template_helper_dep,RULES=unaccent)", 0))
		fixture.TSTemplateRestrict(t, p, c, r, "template_helper_dep", "template_helper_dict")
		fixture.TSTemplateLexemes(t, p, c, r, pg, "SELECT ts_lexize('public.template_helper_dict','Hôtel') AS lexemes", "{Hotel}")
		run(fixture.Exec("DROP EXTENSION unaccent CASCADE", 0))
		templateAbsent(t, p, c, r, "template_helper_dep")
		run(fixture.Select("SELECT count(*) FROM pg_ts_dict WHERE dictname='template_helper_dict'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}
