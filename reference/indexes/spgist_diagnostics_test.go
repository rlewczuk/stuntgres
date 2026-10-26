package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSPGiSTDiagnostics(t *testing.T) {
	for _, tc := range []struct{ clause, code, message, detail, hint string }{
		{"UNIQUE INDEX spgist_bad ON spgist_diag USING spgist(p)", "0A000", `access method "spgist" does not support unique indexes`, "", ""},
		{"INDEX spgist_bad ON spgist_diag USING spgist(p,p)", "0A000", `access method "spgist" does not support multicolumn indexes`, "", ""},
		{"INDEX spgist_bad ON spgist_diag USING spgist(p ASC)", "0A000", `access method "spgist" does not support ASC/DESC options`, "", ""},
		{"INDEX spgist_bad ON spgist_diag USING spgist(p DESC)", "0A000", `access method "spgist" does not support ASC/DESC options`, "", ""},
		{"INDEX spgist_bad ON spgist_diag USING spgist(p NULLS FIRST)", "0A000", `access method "spgist" does not support NULLS FIRST/LAST options`, "", ""},
		{"INDEX spgist_bad ON spgist_diag USING spgist(p NULLS LAST)", "0A000", `access method "spgist" does not support NULLS FIRST/LAST options`, "", ""},
		{"INDEX spgist_bad ON spgist_diag USING spgist(i)", "42704", `data type integer has no default operator class for access method "spgist"`, "", "You must specify an operator class for the index or define a default operator class for the data type."},
		{"INDEX spgist_bad ON spgist_diag USING spgist(p point_ops)", "42704", `operator class "point_ops" does not exist for access method "spgist"`, "", ""},
		{"INDEX spgist_bad ON spgist_diag USING spgist(i text_ops)", "42804", `operator class "text_ops" does not accept data type integer`, "", ""},
		{"INDEX spgist_bad ON spgist_diag USING spgist(s text_ops(siglen=32))", "22023", `operator class text_ops has no options`, "", ""},
	} {
		t.Run(tc.clause, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				run(fixture.Exec("CREATE TABLE spgist_diag(i integer,p point,s text)", 0))
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Detail, d.Hint = tc.detail, tc.hint
				classRoutes(t, p, b, conn, fixture.ErrorDiagnostic("CREATE "+tc.clause, d))
				if tc.code == "42704" && tc.hint != "" {
					q := "CREATE " + tc.clause
					held := classHeld(t, p, conn, q)
					step := fixture.ErrorDiagnostic(q, d)
					step.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
				}
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='spgist_bad'", []string{"relname"}, []string{"NAME"}, nil))
				run(fixture.Exec("CREATE INDEX spgist_bad ON spgist_diag USING spgist(p)", 0))
			})
		})
	}
	for _, tc := range []struct{ typ, name string }{{"circle", "circle"}, {"integer[]", "integer[]"}, {"int4multirange", "int4multirange"}, {"character", "character"}} {
		t.Run(tc.typ, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				run(fixture.Exec("CREATE TABLE spgist_unsupported(v "+tc.typ+")", 0))
				d := fixture.ControlsReferenceDiagnostic("42704", "data type "+tc.name+` has no default operator class for access method "spgist"`)
				d.Hint = "You must specify an operator class for the index or define a default operator class for the data type."
				classRoutes(t, p, b, conn, fixture.ErrorDiagnostic("CREATE INDEX spgist_bad ON spgist_unsupported USING spgist(v)", d))
			})
		})
	}
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, _ := classConn(t, p, b)
		for _, prefix := range []string{"", "/*é*/ "} {
			d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: text <-> text")
			d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
			d.Position = fixture.Position(18)
			if prefix != "" {
				d.Position = fixture.Position(24)
			}
			step := fixture.ErrorDiagnostic(prefix+"SELECT 'x'::text <-> 'y'::text", d)
			step.SelectRows = true
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, step)
			step = fixture.Prepared(step)
			step.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, step)
		}
	})
}

func TestReferenceSPGiSTOptions(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE spgist_options(p point)", 0))
		for _, value := range []string{"10", "100", "80"} {
			run(fixture.Exec("CREATE INDEX spgist_options_i ON spgist_options USING spgist(p) WITH(fillfactor="+value+")", 0))
			classRoutes(t, p, b, conn, fixture.Select("SELECT reloptions::text AS options FROM pg_class WHERE oid='spgist_options_i'::regclass", []string{"options"}, []string{"TEXT"}, [][]any{{"{fillfactor=" + value + "}"}}))
			run(fixture.Exec("DROP INDEX spgist_options_i", 0))
		}
		run(fixture.Exec("CREATE INDEX spgist_options_i ON spgist_options USING spgist(p) WITH(fillfactor=80)", 0))
		for _, tc := range []struct{ options, message, detail string }{
			{"fillfactor=9", `value 9 out of bounds for option "fillfactor"`, `Valid values are between "10" and "100".`},
			{"fillfactor=101", `value 101 out of bounds for option "fillfactor"`, `Valid values are between "10" and "100".`},
			{"fillfactor=no", `invalid value for integer option "fillfactor": no`, ""},
			{"fillfactor=80,fillfactor=90", `parameter "fillfactor" specified more than once`, ""},
			{"buffering=on", `unrecognized parameter "buffering"`, ""}, {"deduplicate_items=on", `unrecognized parameter "deduplicate_items"`, ""}, {"pages_per_range=8", `unrecognized parameter "pages_per_range"`, ""},
		} {
			d := fixture.ControlsReferenceDiagnostic("22023", tc.message)
			d.Detail = tc.detail
			classRoutes(t, p, b, conn, fixture.ErrorDiagnostic("CREATE INDEX spgist_invalid ON spgist_options USING spgist(p) WITH("+tc.options+")", d))
			if tc.options == "buffering=on" {
				q := "CREATE INDEX spgist_invalid ON spgist_options USING spgist(p) WITH(buffering=on)"
				held := classHeld(t, p, conn, q)
				step := fixture.ErrorDiagnostic(q, d)
				step.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			}
			classRoutes(t, p, b, conn, fixture.ErrorDiagnostic("ALTER INDEX spgist_options_i SET("+tc.options+")", d))
			run(fixture.Select("SELECT reloptions::text AS options FROM pg_class WHERE oid='spgist_options_i'::regclass", []string{"options"}, []string{"TEXT"}, [][]any{{"{fillfactor=80}"}}))
		}
		d := fixture.ControlsReferenceDiagnostic("22023", `unrecognized parameter namespace "toast"`)
		classRoutes(t, p, b, conn, fixture.ErrorDiagnostic("CREATE INDEX spgist_invalid ON spgist_options USING spgist(p) WITH(toast.fillfactor=80)", d))
		run(fixture.Exec("ALTER INDEX spgist_options_i SET(fillfactor=10)", 0))
		run(fixture.Exec("ALTER INDEX spgist_options_i RESET(fillfactor)", 0))
		classRoutes(t, p, b, conn, fixture.Select("SELECT reloptions::text AS options FROM pg_class WHERE oid='spgist_options_i'::regclass", []string{"options"}, []string{"TEXT"}, [][]any{{nil}}))
	})
}
