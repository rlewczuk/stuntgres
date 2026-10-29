package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func ftsInspectionSignatures(t *testing.T, debug bool) {
	fixture.RoleReferenceCleanup(t, []string{"debug_stat_reader"})
	t.Helper()
	ftsParserScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		where := "oid IN (3689,3690)"
		want := [][]any{
			{int64(3689), "ts_stat", "25", "{25,25,23,23}", "{i,o,o,o}", "{query,word,ndoc,nentry}", int64(2249), true, true, "v", "u", float64(10), float64(10000), int64(12), "ts_stat1", false},
			{int64(3690), "ts_stat", "25 25", "{25,25,25,23,23}", "{i,i,o,o,o}", "{query,weights,word,ndoc,nentry}", int64(2249), true, true, "v", "u", float64(10), float64(10000), int64(12), "ts_stat2", false},
		}
		if debug {
			where = "oid IN (6183,6184)"
			want = [][]any{
				{int64(6183), "ts_debug", "3734 25", "{3734,25,25,25,25,3770,3769,1009}", "{i,i,o,o,o,o,o,o}", "{config,document,alias,description,token,dictionaries,dictionary,lexemes}", int64(2249), true, true, "s", "s", float64(100), float64(1000), int64(14), "", true},
				{int64(6184), "ts_debug", "25", "{25,25,25,25,3770,3769,1009}", "{i,o,o,o,o,o,o}", "{document,alias,description,token,dictionaries,dictionary,lexemes}", int64(2249), true, true, "s", "s", float64(100), float64(1000), int64(14), "", true},
			}
		}
		ftsParserSelect(t, p, b, "SELECT oid::bigint,proname,proargtypes::text,proallargtypes::text,proargmodes::text,proargnames::text,prorettype::bigint,proisstrict,proretset,provolatile,proparallel,procost,prorows,prolang::bigint,prosrc,prosqlbody IS NOT NULL AS has_body FROM pg_proc WHERE "+where+" ORDER BY oid", []string{"oid", "proname", "proargtypes", "proallargtypes", "proargmodes", "proargnames", "prorettype", "proisstrict", "proretset", "provolatile", "proparallel", "procost", "prorows", "prolang", "prosrc", "has_body"}, []string{"INT8", "NAME", "TEXT", "TEXT", "TEXT", "TEXT", "INT8", "BOOL", "BOOL", "CHAR", "CHAR", "FLOAT4", "FLOAT4", "INT8", "TEXT", "BOOL"}, want)
		if debug {
			ftsParserSelect(t, p, b, "SELECT token,lexemes FROM pg_catalog.ts_debug(document => 'Rats',config => 'english')", []string{"token", "lexemes"}, []string{"TEXT", "_TEXT"}, [][]any{{"Rats", "{rat}"}})
		} else {
			ftsParserSelect(t, p, b, "SELECT * FROM pg_catalog.ts_stat(weights => 'a',query => 'SELECT ''cat:1A''::tsvector')", ftsStatColumns, ftsStatTypes, [][]any{{"cat", int64(1), int64(1)}})
		}
		c := ftsInspectionConnection(t, p, b)
		run := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
		run("CREATE ROLE debug_stat_reader")
		signature, query, name := "pg_catalog.ts_stat(text)", "SELECT * FROM ts_stat('SELECT ''cat:1''::tsvector')", "ts_stat"
		columns, types, rows := ftsStatColumns, ftsStatTypes, [][]any{{"cat", int64(1), int64(1)}}
		if debug {
			signature, query, name = "pg_catalog.ts_debug(text)", "SELECT * FROM ts_debug('Rats')", "ts_debug"
			columns, types, rows = ftsDebugColumns, ftsDebugTypes, [][]any{{"asciiword", "Word, all ASCII", "Rats", "{simple}", "simple", "{rats}"}}
			run("SET default_text_search_config=simple")
			defer c.ExecContext(p.Ctx, "RESET default_text_search_config")
		}
		defer func() {
			for _, q := range []string{"RESET ROLE", "REVOKE EXECUTE ON FUNCTION " + signature + " FROM debug_stat_reader", "GRANT EXECUTE ON FUNCTION " + signature + " TO PUBLIC"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error("restore routine privileges:", err)
				}
			}
		}()
		run("REVOKE EXECUTE ON FUNCTION " + signature + " FROM PUBLIC")
		run("SET ROLE debug_stat_reader")
		ftsInspectionDiagnostic(t, p, b, c, query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+name), false)
		run("RESET ROLE")
		run("GRANT EXECUTE ON FUNCTION " + signature + " TO debug_stat_reader")
		run("SET ROLE debug_stat_reader")
		ftsInspectionBoth(t, p, b, c, fixture.Select(query, columns, types, rows))
		run("RESET ROLE")
	})
}
func TestReferenceFTSDebugSignatures(t *testing.T) { ftsInspectionSignatures(t, true) }
func TestReferenceFTSStatSignatures(t *testing.T)  { ftsInspectionSignatures(t, false) }
