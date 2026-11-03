package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceWALNamesCatalog(t *testing.T) {
	walNamesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		q := `SELECT oid::bigint AS oid,proname,pronamespace::bigint AS namespace,proowner::bigint AS owner,prolang::bigint AS language,prokind::text AS kind,prorettype::bigint AS result,proretset,proisstrict,provolatile::text AS volatility,proparallel::text AS parallel,procost,prorows,pronargs,pronargdefaults,provariadic::bigint AS variadic,prosecdef,proleakproof,prosrc,proargtypes::text AS inputs,proallargtypes::text AS alltypes,proargmodes::text AS modes,proargnames::text AS names,proacl::text AS acl FROM pg_proc WHERE oid IN (2850,2851,6213) ORDER BY oid`
		names := []string{"oid", "proname", "namespace", "owner", "language", "kind", "result", "proretset", "proisstrict", "volatility", "parallel", "procost", "prorows", "pronargs", "pronargdefaults", "variadic", "prosecdef", "proleakproof", "prosrc", "inputs", "alltypes", "modes", "names", "acl"}
		types := []string{"INT8", "NAME", "INT8", "INT8", "INT8", "TEXT", "INT8", "BOOL", "BOOL", "TEXT", "TEXT", "FLOAT4", "FLOAT4", "INT2", "INT2", "INT8", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}
		want := [][]any{
			{int64(2850), "pg_walfile_name_offset", int64(11), int64(10), int64(12), "f", int64(2249), false, true, "i", "s", float64(1), float64(0), int64(1), int64(0), int64(0), false, false, "pg_walfile_name_offset", "3220", "{3220,25,23}", "{i,o,o}", "{lsn,file_name,file_offset}", nil},
			{int64(2851), "pg_walfile_name", int64(11), int64(10), int64(12), "f", int64(25), false, true, "i", "s", float64(1), float64(0), int64(1), int64(0), int64(0), false, false, "pg_walfile_name", "3220", nil, nil, nil, nil},
			{int64(6213), "pg_split_walfile_name", int64(11), int64(10), int64(12), "f", int64(2249), false, true, "s", "s", float64(1), float64(0), int64(1), int64(0), int64(0), false, false, "pg_split_walfile_name", "25", "{25,1700,20}", "{i,o,o}", "{file_name,segment_number,timeline_id}", nil},
		}
		walNamesRoutes(t, p, b, fixture.Select(q, names, types, want))
		for _, tc := range []struct{ oid, args, result string }{
			{"2851", "pg_lsn", "text"},
			{"2850", "lsn pg_lsn, OUT file_name text, OUT file_offset integer", "record"},
			{"6213", "file_name text, OUT segment_number numeric, OUT timeline_id bigint", "record"},
		} {
			walNamesRoutes(t, p, b, fixture.Select("SELECT pg_get_function_arguments("+tc.oid+") AS args,pg_get_function_result("+tc.oid+") AS result", []string{"args", "result"}, []string{"TEXT", "TEXT"}, [][]any{{tc.args, tc.result}}))
		}
		walNamesRoutes(t, p, b, fixture.Select("SHOW wal_segment_size", []string{"wal_segment_size"}, []string{"TEXT"}, [][]any{{"16MB"}}))
		walNamesRoutes(t, p, b, fixture.Select("SELECT setting,boot_val,reset_val,unit,min_val,max_val,context FROM pg_settings WHERE name='wal_segment_size'", []string{"setting", "boot_val", "reset_val", "unit", "min_val", "max_val", "context"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"16777216", "16777216", "16777216", "B", "1048576", "1073741824", "internal"}}))
		walNamesRoutes(t, p, b, fixture.Select("SELECT current_setting('wal_segment_size') AS size,(pg_walfile_name_offset('0/0'::pg_lsn + (setting::numeric-1))).file_offset AS off FROM pg_settings WHERE name='wal_segment_size'", []string{"size", "off"}, []string{"TEXT", "INT4"}, [][]any{{"16MB", int64(16777215)}}))
		walNamesRoutes(t, p, b, fixture.Select("SELECT (pg_walfile_name_offset('0/0'::pg_lsn + setting::numeric)).file_name AS n,(pg_walfile_name_offset('0/0'::pg_lsn + setting::numeric)).file_offset AS off FROM pg_settings WHERE name='wal_segment_size'", []string{"n", "off"}, []string{"TEXT", "INT4"}, [][]any{{"000000010000000000000001", int64(0)}}))
		for _, q := range []string{"SET wal_segment_size='32MB'", "RESET wal_segment_size", "SELECT set_config('wal_segment_size','32MB',false)"} {
			d := fixture.ControlsReferenceDiagnostic("55P02", `parameter "wal_segment_size" cannot be changed`)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, d)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		walNamesRoutes(t, p, b, fixture.Select("SHOW wal_segment_size", []string{"wal_segment_size"}, []string{"TEXT"}, [][]any{{"16MB"}}))
	})
}
