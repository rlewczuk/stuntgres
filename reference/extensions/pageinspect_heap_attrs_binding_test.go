package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferencePageinspectHeapAttrsBinding(t *testing.T) {
	attrsCase(t, "a integer,b integer", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("SET search_path TO heap_part,public", 0))
		attrsDecorations(t, p, b, c)
		for _, q := range []string{
			"SELECT tuple_data_split('attrs_schema'::regclass::oid,$1,0::smallint,2::smallint,NULL) AS attrs",
			"SELECT heap_part.tuple_data_split(t_bits=>NULL,t_infomask2=>2,t_data=>$1,rel_oid=>'attrs_schema'::regclass::oid,t_infomask=>0) AS attrs",
			"SELECT heap_part.tuple_data_split(do_detoast=>false,t_bits=>NULL,t_infomask2=>2,t_data=>$1,rel_oid=>'attrs_schema'::regclass::oid,t_infomask=>0) AS attrs",
			"SELECT heap_part.tuple_data_split('attrs_schema'::regclass::oid,$1,t_infomask=>0,t_infomask2=>2,t_bits=>NULL,do_detoast=>false) AS attrs",
			"SELECT * FROM heap_part.tuple_data_split('attrs_schema'::regclass::oid,$1,0,2,NULL) AS attrs",
		} {
			heapHeld(t, p, b, c, q, []fixture.Step{attrsValue(q, attrsArray("01020304", "05060708"), attrsBytes("0102030405060708")), attrsValue(q, nil, nil)})
		}
		for _, q := range []string{
			"SELECT * FROM heap_part.heap_page_item_attrs(rel_oid=>'attrs_schema'::regclass,page=>$1) ORDER BY lp",
			"SELECT * FROM heap_part.heap_page_item_attrs(do_detoast=>false,rel_oid=>'attrs_schema'::regclass,page=>$1) ORDER BY lp",
		} {
			heapBoth(t, p, b, c, attrsRows(q, [][]any{attrsRow(32, 32, 0, 2, 24, nil, attrsArray("01020304", "05060708"))}, attrsImage(attrsBytes("0102030405060708"), 0, 2, nil, false)))
		}
		for _, tc := range []struct{ q, message string }{
			{"SELECT heap_part.tuple_data_split()", "function heap_part.tuple_data_split() does not exist"},
			{"SELECT heap_part.tuple_data_split(0::oid,NULL::bytea,0::bigint,0,NULL::text)", "function heap_part.tuple_data_split(oid, bytea, bigint, integer, text) does not exist"},
			{"SELECT heap_part.tuple_data_split(0::oid,NULL::bytea,0,0,NULL::text,true,true)", "function heap_part.tuple_data_split(oid, bytea, integer, integer, text, boolean, boolean) does not exist"},
			{"SELECT heap_part.tuple_data_split(rel_oid=>0::oid,t_data=>NULL::bytea,t_infomask=>0,t_infomask2=>0,wrong=>NULL::text)", "function heap_part.tuple_data_split(rel_oid => oid, t_data => bytea, t_infomask => integer, t_infomask2 => integer, wrong => text) does not exist"},
		} {
			heapBoth(t, p, b, c, heapLookup(tc.q, tc.message, 8))
		}
		q := "SELECT /*é*/ heap_part.tuple_data_split(rel_oid=>0::oid,t_data=>NULL::bytea,t_infomask=>0,t_infomask2=>0,t_bits=>NULL,t_bits=>NULL)"
		at := strings.LastIndex(q, "t_bits=>")
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "argument name \"t_bits\" used more than once", Position: fixture.Position(utf8.RuneCountInString(q[:at]) + 1)})
		s.SelectRows = true
		heapBoth(t, p, b, c, s)
		// Each volatile occurrence contributes exactly once. SQL does not promise
		// cross-argument evaluation order, including reordered named arguments.
		run(fixture.Exec("CREATE SEQUENCE attrs_eval START 1", 0))
		q = "SELECT heap_part.tuple_data_split(t_bits=>CASE WHEN nextval('attrs_eval')>0 THEN NULL::text ELSE 'bad' END,t_infomask2=>(nextval('attrs_eval')*0+2)::integer,t_data=>$1,rel_oid=>'attrs_schema'::regclass::oid,t_infomask=>(nextval('attrs_eval')*0)::integer,do_detoast=>nextval('attrs_eval')>0) AS attrs"
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec("ALTER SEQUENCE attrs_eval RESTART WITH 1", 0))
			s := attrsValue(q, attrsArray("01020304", "05060708"), attrsBytes("0102030405060708"))
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
			run(fixture.Select("SELECT last_value FROM attrs_eval", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(4)}}))
		}
		q = "SELECT heap_part.tuple_data_split('attrs_schema'::regclass::oid,NULL::bytea,1/$1::integer,0,NULL::text)"
		heapBoth(t, p, b, c, heapError(q, "22012", "division by zero", 0))
	})
}
