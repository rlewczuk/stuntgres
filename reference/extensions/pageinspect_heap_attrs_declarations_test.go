package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePageinspectHeapAttrsDeclarations(t *testing.T) {
	attrsCase(t, "a integer,b integer", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			name, args, names, allargs, modes, parallel, language, source string
			result                                                        int64
			sets, body                                                    bool
			cost, rows                                                    float64
		}{
			{"tuple_data_split", "26 17 23 23 25", "{rel_oid,t_data,t_infomask,t_infomask2,t_bits}", "", "", "r", "c", "tuple_data_split", 1001, false, false, 1, 0},
			{"tuple_data_split", "26 17 23 23 25 16", "{rel_oid,t_data,t_infomask,t_infomask2,t_bits,do_detoast}", "", "", "r", "c", "tuple_data_split", 1001, false, false, 1, 0},
			{"heap_page_item_attrs", "17 2205", "{page,rel_oid,lp,lp_off,lp_flags,lp_len,t_xmin,t_xmax,t_field3,t_ctid,t_infomask2,t_infomask,t_hoff,t_bits,t_oid,t_attrs}", "{17,2205,21,21,21,21,28,28,23,27,23,23,21,25,26,1001}", "{i,i,o,o,o,o,o,o,o,o,o,o,o,o,o,o}", "r", "sql", "", 2249, true, true, 100, 1000},
			{"heap_page_item_attrs", "17 2205 16", "{page,rel_oid,do_detoast,lp,lp_off,lp_flags,lp_len,t_xmin,t_xmax,t_field3,t_ctid,t_infomask2,t_infomask,t_hoff,t_bits,t_oid,t_attrs}", "{17,2205,16,21,21,21,21,28,28,23,27,23,23,21,25,26,1001}", "{i,i,i,o,o,o,o,o,o,o,o,o,o,o,o,o,o}", "r", "sql", "", 2249, true, true, 100, 1000},
		} {
			var allargs, modes, binary any
			if tc.body {
				allargs, modes = tc.allargs, tc.modes
			} else {
				binary = "$libdir/pageinspect"
			}
			q := "SELECT p.prorettype::bigint AS result,p.proargnames::text AS names,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.procost AS cost,p.prorows AS rows,p.pronargdefaults::integer AS defaults,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language,p.prosqlbody IS NOT NULL AS body FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='heap_part' AND p.proname='" + tc.name + "' AND p.proargtypes::text='" + tc.args + "'"
			heapBoth(t, p, b, c, fixture.Select(q, []string{"result", "names", "allargs", "modes", "sets", "strict", "volatility", "parallel", "cost", "rows", "defaults", "source", "binary", "language", "body"}, []string{"INT8", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "FLOAT4", "FLOAT4", "INT4", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{tc.result, tc.names, allargs, modes, tc.sets, false, "v", tc.parallel, tc.cost, tc.rows, int64(0), tc.source, binary, tc.language, tc.body}}))
		}
		q := "SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='heap_part' AND p.proname IN ('tuple_data_split','heap_page_item_attrs')"
		heapBoth(t, p, b, c, fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
		// Both SQL-standard bodies carry executable member dependencies.
		for _, tc := range []struct{ caller, callee string }{
			{"heap_page_item_attrs(bytea,regclass)", "heap_page_item_attrs(bytea,regclass,boolean)"},
			{"heap_page_item_attrs(bytea,regclass,boolean)", "tuple_data_split(oid,bytea,integer,integer,text,boolean)"},
			{"heap_page_item_attrs(bytea,regclass,boolean)", "heap_page_items(bytea)"},
		} {
			q := "SELECT count(*) FROM pg_depend WHERE classid=1255 AND objid='heap_part." + tc.caller + "'::regprocedure AND refclassid=1255 AND refobjid='heap_part." + tc.callee + "'::regprocedure AND deptype='n'"
			heapBoth(t, p, b, c, fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		}
	})
}
