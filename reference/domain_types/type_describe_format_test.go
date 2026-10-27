package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTypeDescribeBuiltins(t *testing.T) {
	cases := []struct {
		oid  int
		name string
	}{
		{16, "boolean"}, {17, "bytea"}, {18, `"char"`}, {19, "name"}, {20, "bigint"}, {21, "smallint"}, {22, "int2vector"}, {23, "integer"}, {24, "regproc"}, {25, "text"}, {26, "oid"}, {27, "tid"}, {28, "xid"}, {29, "cid"}, {30, "oidvector"},
		{114, "json"}, {142, "xml"}, {600, "point"}, {601, "lseg"}, {602, "path"}, {603, "box"}, {604, "polygon"}, {628, "line"}, {650, "cidr"}, {700, "real"}, {701, "double precision"}, {705, "unknown"}, {718, "circle"}, {790, "money"}, {829, "macaddr"}, {869, "inet"},
		{1000, "boolean[]"}, {1002, `"char"[]`}, {1003, "name[]"}, {1005, "smallint[]"}, {1006, "int2vector[]"}, {1007, "integer[]"}, {1009, "text[]"}, {1013, "oidvector[]"}, {1014, "character[]"}, {1015, "character varying[]"}, {1016, "bigint[]"}, {1028, "oid[]"},
		{1042, "character"}, {1043, "character varying"}, {1082, "date"}, {1083, "time without time zone"}, {1114, "timestamp without time zone"}, {1184, "timestamp with time zone"}, {1186, "interval"}, {1266, "time with time zone"}, {1560, "bit"}, {1562, "bit varying"}, {1700, "numeric"}, {1790, "refcursor"},
		{2202, "regprocedure"}, {2203, "regoper"}, {2204, "regoperator"}, {2205, "regclass"}, {2206, "regtype"}, {2249, "record"}, {2276, `"any"`}, {2278, "void"}, {2950, "uuid"}, {3220, "pg_lsn"}, {3614, "tsvector"}, {3615, "tsquery"}, {3802, "jsonb"}, {3904, "int4range"}, {3906, "numrange"}, {3908, "tsrange"}, {3910, "tstzrange"}, {3912, "daterange"}, {3926, "int8range"}, {4451, "int4multirange"}, {4532, "nummultirange"}, {4533, "tsmultirange"}, {4534, "tstzmultirange"}, {4535, "datemultirange"}, {4536, "int8multirange"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				typeDescribeValue(run, fmt.Sprintf("pg_catalog.format_type(%d::oid,NULL)", tc.oid), "TEXT", tc.name)
			})
		})
	}
	typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		for _, tc := range []struct {
			expression string
			want       any
		}{
			{"format_type(NULL::oid,0)", nil}, {"format_type(NULL::oid,NULL)", nil}, {"format_type(0::oid,NULL)", "-"}, {"format_type(4294967295::oid,NULL)", "???"},
		} {
			typeDescribeValue(run, tc.expression, "TEXT", tc.want)
		}
	})
}

func TestReferenceTypeDescribeTypmods(t *testing.T) {
	cases := []struct {
		oid, mod int
		want     string
	}{
		{23, -1, "integer"}, {23, 7, "integer"}, {1042, -1, "bpchar"}, {1042, 0, "character"}, {1042, 1, "character"}, {1042, 2, "character"}, {1042, 3, "character"}, {1042, 4, "character"}, {1042, 7, "character(3)"},
		{1043, -1, "character varying"}, {1043, 0, "character varying"}, {1043, 4, "character varying"}, {1043, 36, "character varying(32)"},
		{1700, -1, "numeric"}, {1700, 0, "numeric"}, {1700, 1, "numeric"}, {1700, 2, "numeric"}, {1700, 3, "numeric"}, {1700, 327686, "numeric(5,2)"}, {1700, 329730, "numeric(5,-2)"}, {1700, 328707, "numeric(5,1023)"}, {1700, 328708, "numeric(5,-1024)"},
		{1560, -1, `"bit"`}, {1560, 0, "bit(0)"}, {1560, 7, "bit(7)"}, {1562, -1, "bit varying"}, {1562, 7, "bit varying(7)"},
		{1083, 3, "time(3) without time zone"}, {1266, 7, "time(7) with time zone"}, {1114, 3, "timestamp(3) without time zone"}, {1184, 3, "timestamp(3) with time zone"}, {1114, 7, "timestamp(7) without time zone"}, {25, 7, "text(7)"},
	}
	// INTERVAL masks are dt.h field bits; suffix order follows intervaltypmodout.
	for _, fields := range []struct {
		mask   int
		suffix string
	}{
		{4, " year"}, {2, " month"}, {8, " day"}, {1024, " hour"}, {2048, " minute"}, {4096, " second"}, {6, " year to month"}, {1032, " day to hour"}, {3080, " day to minute"}, {7176, " day to second"}, {3072, " hour to minute"}, {7168, " hour to second"}, {6144, " minute to second"}, {32767, ""},
	} {
		cases = append(cases, struct {
			oid, mod int
			want     string
		}{1186, (fields.mask << 16) | 65535, "interval" + fields.suffix})
		cases = append(cases, struct {
			oid, mod int
			want     string
		}{1186, (fields.mask << 16) | 3, "interval" + fields.suffix + "(3)"})
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d_%d", tc.oid, tc.mod), func(t *testing.T) {
			typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				typeDescribeValue(run, fmt.Sprintf("format_type(%d::oid,%d)", tc.oid, tc.mod), "TEXT", tc.want)
			})
		})
	}
	typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE public.td_modifiers(n numeric(5,2),v varchar(3),b character(3),t timestamp(3),z timestamp(3) with time zone,i interval day to second(2))", 0))
		typeDescribeBoth(run, fixture.Select("SELECT attname,format_type(atttypid,atttypmod) AS declaration FROM pg_attribute WHERE attrelid='public.td_modifiers'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "declaration"}, []string{"NAME", "TEXT"}, [][]any{{"n", "numeric(5,2)"}, {"v", "character varying(3)"}, {"b", "character(3)"}, {"t", "timestamp(3) without time zone"}, {"z", "timestamp(3) with time zone"}, {"i", "interval day to second(2)"}}))
		d := fixture.ControlsReferenceDiagnostic("XX000", "invalid INTERVAL typmod: 0x0")
		typeDescribeFailure(run, "SELECT format_type(1186::oid,0)", d, false)
	})
}
