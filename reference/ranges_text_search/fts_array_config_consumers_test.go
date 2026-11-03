package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func ftsArrayConfigInstall(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { ftsArrayConfigRun(t, p, b, c, s) }
	run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.array_cfg_owned (COPY=pg_catalog.simple)", 0))
	run(fixture.Exec("SET default_text_search_config='public.array_cfg_owned'", 0))
	run(fixture.Exec("CREATE TABLE public.array_cfg_saved(id integer,a text[],v tsvector DEFAULT array_to_tsvector(ARRAY['b','a','a']),cfg regconfig DEFAULT get_current_ts_config())", 0))
	run(fixture.Exec("CREATE VIEW public.array_cfg_view AS SELECT id,array_to_tsvector(a) AS constructed,get_current_ts_config() AS live FROM public.array_cfg_saved", 0))
	run(fixture.Exec("INSERT INTO public.array_cfg_saved(id,a) VALUES(1,ARRAY['b','a','a'])", 1))
}

func ftsArrayConfigStored() fixture.Step {
	return fixture.Select("SELECT id,a,v,cfg,cfg::oid='public.array_cfg_owned'::regconfig::oid AS actual FROM public.array_cfg_saved ORDER BY id",
		[]string{"id", "a", "v", "cfg", "actual"}, []string{"INT4", "_TEXT", "TSVECTOR", "REGCONFIG", "BOOL"},
		[][]any{{int64(1), "{b,a,a}", "'a' 'b'", "array_cfg_owned", true}})
}

func ftsArrayConfigView(name string) fixture.Step {
	return fixture.Select("SELECT id,constructed,live FROM public.array_cfg_view ORDER BY id", []string{"id", "constructed", "live"}, []string{"INT4", "TSVECTOR", "REGCONFIG"}, [][]any{{int64(1), "'a' 'b'", name}})
}

func ftsArrayConfigAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := "INSERT INTO public.array_cfg_saved(id,a,v) SELECT id,a,array_to_tsvector(a) FROM (VALUES(2,'{good}'::text[]),(3,$1::text[])) AS input(id,a) ORDER BY id"
	for _, tc := range []struct{ input, code, message string }{
		{`{bad,""}`, "2200F", "lexeme array may not contain empty strings"},
		{"{bad,NULL}", "22004", "lexeme array may not contain nulls"},
	} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic(tc.code, tc.message), tc.input)
			s.Prepared, s.ErrorStage = prepared, "execute"
			ftsArrayConfigRun(t, p, b, c, s)
			ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigStored())
		}
	}
}

func TestReferenceFTSArrayConfigConsumers(t *testing.T) {
	ftsArrayConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ftsArrayConfigInstall(t, p, b, c)
		ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigStored())
		ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigView("array_cfg_owned"))
		ftsArrayConfigAtomicity(t, p, b, c)
		ftsArrayConfigRun(t, p, b, c, fixture.Exec("SET default_text_search_config='english'", 0))
		ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigView("english"))
		ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigStored())
		ftsArrayConfigRun(t, p, b, c, fixture.Exec("INSERT INTO public.array_cfg_saved(id,a) VALUES(4,'{z,z}')", 1))
		ftsArrayConfigBoth(t, p, b, c, fixture.Select("SELECT id,v,cfg FROM public.array_cfg_saved ORDER BY id", []string{"id", "v", "cfg"}, []string{"INT4", "TSVECTOR", "REGCONFIG"},
			[][]any{{int64(1), "'a' 'b'", "array_cfg_owned"}, {int64(4), "'a' 'b'", "english"}}))
		ftsArrayConfigBoth(t, p, b, c, fixture.Select("SELECT id,constructed,live FROM public.array_cfg_view ORDER BY id", []string{"id", "constructed", "live"}, []string{"INT4", "TSVECTOR", "REGCONFIG"},
			[][]any{{int64(1), "'a' 'b'", "english"}, {int64(4), "'z'", "english"}}))
		// The stable identity function is not immutable merely because arity is
		// zero. Test the established stored generated/index admission boundaries.
		ftsArrayConfigErrorRoutes(t, p, b, c, fixture.ErrorDiagnostic("CREATE TABLE array_cfg_generated(a integer,c regconfig GENERATED ALWAYS AS (get_current_ts_config()) STORED)", fixture.ControlsReferenceDiagnostic("42P17", "generation expression is not immutable")), false)
		ftsArrayConfigErrorRoutes(t, p, b, c, fixture.ErrorDiagnostic("CREATE INDEX array_cfg_bad_index ON public.array_cfg_saved((get_current_ts_config()))", fixture.ControlsReferenceDiagnostic("42P17", "functions in index expression must be marked IMMUTABLE")), false)
	})
}

func TestReferenceFTSArrayConfigPersistence(t *testing.T) {
	ftsArrayConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ftsArrayConfigInstall(t, p, b, c)
		ftsArrayConfigRun(t, p, b, c, fixture.Exec("SET default_text_search_config='simple'", 0))
		ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigStored())
		ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigView("simple"))
		ftsArrayConfigAtomicity(t, p, b, c)
		if !b.PG {
			ftsArrayConfigControls(t, p, b, c)
		}
		ftsArrayConfigRun(t, p, b, c, fixture.Exec("SET default_text_search_config='english'", 0))
		ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigStored())
		ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigView("english"))
		ftsArrayConfigRun(t, p, b, c, fixture.Exec("INSERT INTO public.array_cfg_saved(id,a) VALUES(2,'{}')", 1))
		ftsArrayConfigBoth(t, p, b, c, fixture.Select("SELECT v,cfg FROM public.array_cfg_saved WHERE id=2", []string{"v", "cfg"}, []string{"TSVECTOR", "REGCONFIG"}, [][]any{{"'a' 'b'", "english"}}))
	})
}
