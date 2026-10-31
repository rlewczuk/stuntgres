package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func heapIdentities(schema, heap, flags string) fixture.Step {
	return fixture.Select("SELECT h.oid::bigint>0 AS heap_valid,f.oid::bigint>0 AS flags_valid,h.oid<>f.oid AS distinct_ids,h.proowner=f.proowner AS same_owner,dh.refobjid=e.oid AS heap_member,df.refobjid=e.oid AS flags_member FROM pg_proc h JOIN pg_namespace n ON n.oid=h.pronamespace JOIN pg_proc f ON f.pronamespace=n.oid JOIN pg_depend dh ON dh.objid=h.oid JOIN pg_depend df ON df.objid=f.oid JOIN pg_extension e ON e.oid=dh.refobjid WHERE n.nspname='"+schema+"' AND h.proname='"+heap+"' AND h.proargtypes::text='17' AND f.proname='"+flags+"' AND f.proargtypes::text='23 23' AND dh.classid=1255 AND dh.refclassid=3079 AND dh.deptype='e' AND df.classid=1255 AND df.refclassid=3079 AND df.deptype='e' AND e.extname='pageinspect'", []string{"heap_valid", "flags_valid", "distinct_ids", "same_owner", "heap_member", "flags_member"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true}})
}

func TestReferencePageinspectHeapBinding(t *testing.T) {
	heapCase(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ q, message string }{
			{"SELECT heap_page_items(NULL::bytea)", "function heap_page_items(bytea) does not exist"},
			{"SELECT heap_tuple_infomask_flags(0,0)", "function heap_tuple_infomask_flags(integer, integer) does not exist"},
		} {
			heapBoth(t, p, b, c, heapLookup(tc.q, tc.message, 8))
		}
		for _, q := range []string{"CREATE SCHEMA heap_part", "CREATE EXTENSION pageinspect SCHEMA heap_part", "SET search_path TO heap_part,public"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET search_path")
		for _, q := range []string{"SELECT * FROM heap_page_items($1)", "SELECT * FROM heap_part.heap_page_items(page=>$1)"} {
			heapHeld(t, p, b, c, q, []fixture.Step{heapStep(q, heapImage(), heapRow(b.PG)), heapStep(q, nil), heapStep(q, make([]byte, 24))})
		}
		for _, q := range []string{"SELECT * FROM heap_tuple_infomask_flags($1,$2)", "SELECT * FROM heap_part.heap_tuple_infomask_flags(t_infomask=>$1,t_infomask2=>$2)"} {
			heapHeld(t, p, b, c, q, []fixture.Step{heapFlagsStep(q, int64(1), int64(0), "{HEAP_HASNULL}", "{}"), heapFlagsStep(q, int64(0), int64(32768), "{HEAP_ONLY_TUPLE}", "{}"), heapFlagsStep(q, nil, int64(0), nil, nil)})
		}
		q := "SELECT * FROM heap_part.heap_tuple_infomask_flags(t_infomask2=>$1,t_infomask=>$2)"
		heapHeld(t, p, b, c, q, []fixture.Step{
			heapFlagsStep(q, int64(32768), int64(1), "{HEAP_HASNULL,HEAP_ONLY_TUPLE}", "{}"),
			heapFlagsStep(q, int64(0), int64(80), "{HEAP_XMAX_KEYSHR_LOCK,HEAP_XMAX_EXCL_LOCK}", "{HEAP_XMAX_SHR_LOCK}"),
			heapFlagsStep(q, nil, int64(1), nil, nil),
			heapFlagsStep(q, int64(8192), int64(0), "{HEAP_KEYS_UPDATED}", "{}"),
		})
		heapBoth(t, p, b, c, fixture.Select("SELECT * FROM heap_part.heap_tuple_infomask_flags('1','32768')", heapFlagsNames, heapFlagsTypes, [][]any{{"{HEAP_HASNULL,HEAP_ONLY_TUPLE}", "{}"}}))
		heapBoth(t, p, b, c, fixture.Select("SELECT * FROM heap_part.heap_page_items('\\x000000000000000000000000000000000000000000000000')", heapNames, heapTypes, nil))
		heapBoth(t, p, b, c, fixture.Select("SELECT * FROM heap_part.heap_tuple_infomask_flags(1::smallint,0::smallint)", heapFlagsNames, heapFlagsTypes, [][]any{{"{HEAP_HASNULL}", "{}"}}))
		heapBoth(t, p, b, c, fixture.Select("SELECT (heap_part.heap_tuple_infomask_flags(1,0)).raw_flags AS raw", []string{"raw"}, []string{"_TEXT"}, [][]any{{"{HEAP_HASNULL}"}}))
		heapBoth(t, p, b, c, fixture.Select("SELECT (heap_part.heap_tuple_infomask_flags(0,0)).*", heapFlagsNames, heapFlagsTypes, [][]any{{"{}", "{}"}}))
		owner := "postgres"
		if b.PG {
			owner = "stuntgres"
		}
		heapBoth(t, p, b, c, heapDeclaration(false, "heap_page_items", "heap_part", owner))
		heapBoth(t, p, b, c, heapDeclaration(true, "heap_tuple_infomask_flags", "heap_part", owner))
		heapBoth(t, p, b, c, heapIdentities("heap_part", "heap_page_items", "heap_tuple_infomask_flags"))
		heapHeld(t, p, b, c, "SELECT (heap_part.heap_page_items($1)).lp AS lp", []fixture.Step{
			fixture.Select("SELECT (heap_part.heap_page_items($1)).lp AS lp", []string{"lp"}, []string{"INT2"}, [][]any{{int64(1)}}, heapImage()),
			fixture.Select("SELECT (heap_part.heap_page_items($1)).lp AS lp", []string{"lp"}, []string{"INT2"}, nil, nil),
		})
		heapBoth(t, p, b, c, fixture.Select("SELECT count(*) FROM pg_operator o JOIN pg_proc p ON p.oid=o.oprcode JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='heap_part' AND p.proname IN ('heap_page_items','heap_tuple_infomask_flags')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		heapBoth(t, p, b, c, fixture.Select("SELECT p.proname::text AS name,p.proargtypes::text AS args FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='heap_part' AND p.proname IN ('heap_page_items','heap_tuple_infomask_flags') ORDER BY p.proname,p.proargtypes::text", []string{"name", "args"}, []string{"TEXT", "TEXT"}, [][]any{{"heap_page_items", "17"}, {"heap_tuple_infomask_flags", "23 23"}}))
		for _, q := range []string{"CREATE SCHEMA heap_moved", "ALTER FUNCTION heap_part.heap_page_items(bytea) RENAME TO decoded", "ALTER FUNCTION heap_part.decoded(bytea) SET SCHEMA heap_moved", "ALTER FUNCTION heap_part.heap_tuple_infomask_flags(integer,integer) RENAME TO decoded_flags", "ALTER FUNCTION heap_part.decoded_flags(integer,integer) SET SCHEMA heap_moved", "SET search_path TO heap_moved,public"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		heapBoth(t, p, b, c, heapStep("SELECT * FROM decoded(page=>$1)", heapImage(), heapRow(b.PG)))
		heapBoth(t, p, b, c, heapFlagsStep("SELECT * FROM decoded_flags(t_infomask2=>$2,t_infomask=>$1)", int64(1), int64(0), "{HEAP_HASNULL}", "{}"))
	})
}
