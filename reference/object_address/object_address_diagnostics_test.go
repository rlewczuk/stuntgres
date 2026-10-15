package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

type objectAddressDiagnosticCase struct {
	name, query string
	want        fixture.Diagnostic
	runtime     bool
}

func objectAddressDiagnostics(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, cases []objectAddressDiagnosticCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
					s := fixture.ImplicitError(tc.query, tc.want)
					s.SelectRows = true
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						if !tc.runtime {
							s.ErrorStage = "prepare"
						}
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				})
			}
		})
	}
}
func objectAddressPosition(q, token string) string {
	return strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, token)]) + 1)
}

func TestReferenceObjectAddressDiagnostics(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		var cases []objectAddressDiagnosticCase
		for _, fn := range []string{"pg_identify_object", "pg_identify_object_as_address", "pg_get_object_address", "pg_describe_object"} {
			for _, tc := range []struct{ name, args, types string }{
				{"zero", "", ""}, {"two", "1259,0", "integer, integer"}, {"four", "1259,0,0,0", "integer, integer, integer, integer"},
				{"typed_text", "'1259'::text,0,0", "text, integer, integer"}, {"typed_bool", "NULL,TRUE,0", "unknown, boolean, integer"},
				{"typed_array", "ARRAY[1259],0,0", "integer[], integer, integer"},
			} {
				q := "SELECT " + fn + "(" + tc.args + ")"
				cases = append(cases, objectAddressDiagnosticCase{fn + "_" + tc.name, q, fixture.Diagnostic{Code: "42883", Message: "function " + fn + "(" + tc.types + ") does not exist", Hint: hint, Position: "8"}, false})
			}
			args := "1259,0,0"
			types := "integer, integer, integer"
			if fn == "pg_get_object_address" {
				args = "'schema','{public}','{}'"
				types = "unknown, unknown, unknown"
			}
			for _, prefix := range []string{"public.", "\"" + strings.ToUpper(fn) + "\""} {
				spelling := prefix + fn
				if strings.HasPrefix(prefix, "\"") {
					spelling = prefix
				}
				messageName := strings.ReplaceAll(spelling, "\"", "")
				cases = append(cases, objectAddressDiagnosticCase{fn + prefix, "SELECT " + spelling + "(" + args + ")", fixture.Diagnostic{Code: "42883", Message: "function " + messageName + "(" + types + ") does not exist", Hint: hint, Position: "8"}, false})
			}
			for _, tc := range []struct{ suffix, decoration string }{
				{"distinct", "DISTINCT " + args}, {"order", args + " ORDER BY 1"},
			} {
				cases = append(cases, objectAddressDiagnosticCase{fn + tc.suffix, "SELECT " + fn + "(" + tc.decoration + ")", fixture.Diagnostic{Code: "42809", Message: map[string]string{"distinct": "DISTINCT", "order": "ORDER BY"}[tc.suffix] + " specified, but " + fn + " is not an aggregate function", Position: "8"}, false})
			}
			cases = append(cases,
				objectAddressDiagnosticCase{fn + "filter", "SELECT " + fn + "(" + args + ") FILTER (WHERE TRUE)", fixture.Diagnostic{Code: "42809", Message: "FILTER specified, but " + fn + " is not an aggregate function", Position: "8"}, false},
				objectAddressDiagnosticCase{fn + "over", "SELECT " + fn + "(" + args + ") OVER ()", fixture.Diagnostic{Code: "42809", Message: "OVER specified, but " + fn + " is not a window function nor an aggregate function", Position: "8"}, false},
				objectAddressDiagnosticCase{fn + "star", "SELECT " + fn + "(*)", fixture.Diagnostic{Code: "42883", Message: "function " + fn + "() does not exist", Hint: hint, Position: "8"}, false},
				objectAddressDiagnosticCase{fn + "many", "SELECT " + fn + "(" + strings.Repeat("0,", 100) + "0)", fixture.Diagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}, false},
			)
		}
		for _, fn := range []string{"pg_identify_object", "pg_identify_object_as_address", "pg_describe_object"} {
			for _, tc := range []struct{ name, args, code, message, token string }{
				{"oid", "'bad',0,0", "22P02", `invalid input syntax for type oid: "bad"`, "'bad'"},
				{"oid_range", "'4294967296',0,0", "22003", `value "4294967296" is out of range for type oid`, "'4294967296'"},
				{"int4", "1259,0,'bad'", "22P02", `invalid input syntax for type integer: "bad"`, "'bad'"},
				{"int4_range", "1259,0,'2147483648'", "22003", `value "2147483648" is out of range for type integer`, "'2147483648'"},
				{"strict_bad_sibling", "NULL,0,'bad'", "22P02", `invalid input syntax for type integer: "bad"`, "'bad'"},
			} {
				q := "SELECT " + fn + "(" + tc.args + ")"
				cases = append(cases, objectAddressDiagnosticCase{fn + tc.name, q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: objectAddressPosition(q, tc.token)}, false})
			}
			cases = append(cases, objectAddressDiagnosticCase{fn + "unknown_class", "SELECT " + fn + "(999999,0,0)", fixture.Diagnostic{Code: "XX000", Message: "unsupported object class: 999999"}, true})
		}
		for _, fn := range []string{"pg_identify_object", "pg_identify_object_as_address"} {
			q := "SELECT " + fn + "(classid=>2615,classid=>2200,objsubid=>0)"
			cases = append(cases, objectAddressDiagnosticCase{fn + "duplicate", q, fixture.Diagnostic{Code: "42601", Message: `argument name "classid" used more than once`, Position: strconv.Itoa(strings.LastIndex(q, "classid") + 1)}, false})
			q = "SELECT " + fn + "(classid=>2615,2200,0)"
			cases = append(cases, objectAddressDiagnosticCase{fn + "positional", q, fixture.Diagnostic{Code: "42601", Message: "positional argument cannot follow named argument", Position: objectAddressPosition(q, "2200")}, false})
			q = "SELECT " + fn + "(objsubid=>'bad_sub',classid=>'bad_oid',objid=>0)"
			cases = append(cases, objectAddressDiagnosticCase{fn + "written_order", q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad_sub"`, Position: objectAddressPosition(q, "'bad_sub'")}, false})
			for _, label := range []string{"wrong", "\"Classid\""} {
				q = "SELECT " + fn + "(" + label + "=>2615,objid=>2200,objsubid=>0)"
				cases = append(cases, objectAddressDiagnosticCase{fn + label, q, fixture.Diagnostic{Code: "42883", Message: "function " + fn + "(" + strings.Trim(label, "\"") + " => integer, objid => integer, objsubid => integer) does not exist", Hint: hint, Position: "8"}, false})
			}
		}
		cases = append(cases,
			objectAddressDiagnosticCase{"describe_named", "SELECT pg_describe_object(classid=>2615,objid=>2200,objsubid=>0)", fixture.Diagnostic{Code: "42883", Message: "function pg_describe_object(classid => integer, objid => integer, objsubid => integer) does not exist", Hint: hint, Position: "8"}, false},
			objectAddressDiagnosticCase{"utf8_position", "SELECT /*é*/ pg_identify_object(1259,0)", fixture.Diagnostic{Code: "42883", Message: "function pg_identify_object(integer, integer) does not exist", Hint: hint, Position: "14"}, false},
			objectAddressDiagnosticCase{"redundant_out", "SELECT * FROM pg_identify_object(2615,2200,0) AS x(t text,s text,n text,i text)", fixture.Diagnostic{Code: "42601", Message: "a column definition list is redundant for a function with OUT parameters", Position: "52"}, false},
			objectAddressDiagnosticCase{"inverse_bool", "SELECT pg_get_object_address('schema',TRUE,'{}')", fixture.Diagnostic{Code: "42883", Message: "function pg_get_object_address(unknown, boolean, unknown) does not exist", Hint: hint, Position: "8"}, false},
			objectAddressDiagnosticCase{"inverse_int_array", "SELECT pg_get_object_address('schema',ARRAY[1],'{}')", fixture.Diagnostic{Code: "42883", Message: "function pg_get_object_address(unknown, integer[], unknown) does not exist", Hint: hint, Position: "8"}, false},
		)
		duplicate := "SELECT pg_get_object_address(type=>'schema',type=>'schema',object_names=>'{public}')"
		positional := "SELECT pg_get_object_address(type=>'schema','{public}','{}')"
		cases = append(cases,
			objectAddressDiagnosticCase{"inverse_duplicate", duplicate, fixture.Diagnostic{Code: "42601", Message: `argument name "type" used more than once`, Position: strconv.Itoa(strings.LastIndex(duplicate, "type=>") + 1)}, false},
			objectAddressDiagnosticCase{"inverse_positional", positional, fixture.Diagnostic{Code: "42601", Message: "positional argument cannot follow named argument", Position: objectAddressPosition(positional, "'{public}'")}, false},
			objectAddressDiagnosticCase{"inverse_wrong_label", "SELECT pg_get_object_address(wrong=>'schema',object_names=>'{public}',object_args=>'{}')", fixture.Diagnostic{Code: "42883", Message: "function pg_get_object_address(wrong => unknown, object_names => unknown, object_args => unknown) does not exist", Hint: hint, Position: "8"}, false},
		)
		objectAddressDiagnostics(t, p, b, c, cases)
	})
}

func TestReferenceObjectAddressDiagnosticParameters(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var publicID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='public'").Scan(&publicID); err != nil {
			t.Fatal(err)
		}
		for _, fn := range []string{"pg_identify_object", "pg_identify_object_as_address", "pg_describe_object"} {
			t.Run(fn, func(t *testing.T) {
				q := "SELECT " + fn + "($1,$2,$3) AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, tc := range []struct {
					args []any
					want fixture.Diagnostic
				}{
					{[]any{"bad", int64(2200), int64(0)}, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Context: "unnamed portal parameter $1 = '...'"}},
					{[]any{int64(2615), int64(2200), "2147483648"}, fixture.Diagnostic{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Context: "unnamed portal parameter $3 = '...'"}},
					{[]any{nil, int64(2200), "bad"}, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $3 = '...'"}},
				} {
					s := fixture.ImplicitError(q, tc.want, tc.args...)
					s.SelectRows = true
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				typ, want := "RECORD", any("(schema,,public,public)")
				if fn == "pg_identify_object_as_address" {
					want = "(schema,{public},{})"
				}
				if fn == "pg_describe_object" {
					typ = "TEXT"
					want = "schema public"
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{want}}, int64(2615), publicID, int64(0)))
			})
		}
	})
}
