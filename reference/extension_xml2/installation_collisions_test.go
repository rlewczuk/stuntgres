package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func xml2InstallationCollisions(t *testing.T) {
	t.Helper()
	for _, tc := range []struct {
		name, args, setup, call, source, context string
		value                                    bool
	}{
		{
			name: "xml_valid", args: "25",
			setup: "CREATE FUNCTION xml_valid(text) RETURNS boolean LANGUAGE SQL IMMUTABLE CALLED ON NULL INPUT AS 'SELECT false'",
			call:  "SELECT xml_valid('<r/>') AS original", source: "SELECT false", value: false,
			context: "SQL statement \"CREATE FUNCTION xml_valid(text) RETURNS bool\nAS 'xml_is_well_formed'\nLANGUAGE INTERNAL STRICT STABLE PARALLEL SAFE\"\nextension script file \"xml2--1.1.sql\", near line 9",
		},
		{
			name: "xpath_bool", args: "25 25",
			setup: "CREATE FUNCTION xpath_bool(text,text) RETURNS boolean LANGUAGE SQL IMMUTABLE CALLED ON NULL INPUT AS 'SELECT true'",
			call:  "SELECT xpath_bool('<r/>','/missing') AS original", source: "SELECT true", value: true,
			context: "SQL statement \"CREATE FUNCTION xpath_bool(text,text) RETURNS boolean\nAS '$libdir/pgxml'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"xml2--1.1.sql\", near line 29",
		},
	} {
		t.Run("collision_"+tc.name, func(t *testing.T) {
			xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(tc.setup, 0))
				var oid, owner int64
				q := "SELECT p.oid::bigint,p.proowner::bigint FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public' AND p.proname=$1 AND p.proargtypes::text=$2"
				if err := c.QueryRowContext(p.Ctx, q, tc.name, tc.args).Scan(&oid, &owner); err != nil || oid <= 0 || owner <= 0 {
					t.Fatal("original loose routine identity", oid, owner, err)
				}
				inventory := "SELECT p.oid::bigint AS oid,p.proname,p.proargtypes::text AS args,p.prorettype::bigint AS result_oid,p.proisstrict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,l.lanname,p.prosrc,p.probin,p.proowner::bigint AS owner_oid FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='public' AND p.proname IN (" + xml2Names + ") ORDER BY p.proname"
				unchanged := fixture.Select(inventory,
					[]string{"oid", "proname", "args", "result_oid", "proisstrict", "volatility", "parallel", "lanname", "prosrc", "probin", "owner_oid"},
					[]string{"INT8", "NAME", "TEXT", "INT8", "BOOL", "TEXT", "TEXT", "NAME", "TEXT", "TEXT", "INT8"},
					[][]any{{oid, tc.name, tc.args, int64(16), false, "i", "u", "sql", tc.source, nil, owner}})
				call := fixture.Select(tc.call, []string{"original"}, []string{"BOOL"}, [][]any{{tc.value}})
				xml2Both(t, p, b, c, unchanged)
				xml2Both(t, p, b, c, call)
				d := xml2Error("42723", "function \""+tc.name+"\" already exists with same argument types")
				d.Context = tc.context
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("CREATE EXTENSION xml2", d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					// Exact old OID/owner and one selected row prove no replacement
					// or partial publication, including the late declaration failure.
					xml2Both(t, p, b, c, unchanged)
					xml2Both(t, p, b, c, call)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname,extversion FROM pg_extension WHERE extname='xml2'", []string{"extname", "extversion"}, []string{"NAME", "TEXT"}, nil))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1255 AND d.refclassid=3079 AND d.deptype='e' AND e.extname='xml2'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
			})
		})
	}
}
