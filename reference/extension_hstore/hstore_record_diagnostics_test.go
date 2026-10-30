package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreRecordDiagnostics(t *testing.T) {
	hstoreRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreRecordSetup(t, p, b, c)
		for _, q := range []string{
			"CREATE TYPE hs_record_short AS (v varchar(2))",
			"CREATE TYPE hs_record_numeric AS (n numeric(4,1))",
			"CREATE TYPE hs_record_order AS (z integer,a boolean)",
		} {
			hstoreRun(t, p, b, c, fixture.Exec(q, 0))
		}
		// All unlisted diagnostic fields are independently predicted absent.
		// Body conversion (including constant-folded calls) is predicted execute-stage.
		for _, tc := range []struct{ name, typ, source, code, message, detail string }{
			{"integer", "hs_record_pair", "i=>oops", "22P02", `invalid input syntax for type integer: "oops"`, ""},
			{"empty_integer", "hs_record_pair", `i=>""`, "22P02", `invalid input syntax for type integer: ""`, ""},
			{"text_NULL_integer", "hs_record_pair", `i=>"NULL"`, "22P02", `invalid input syntax for type integer: "NULL"`, ""},
			{"integer_range", "hs_record_pair", "i=>2147483648", "22003", `value "2147483648" is out of range for type integer`, ""},
			{"varchar_typmod", "hs_record_short", "v=>abc", "22001", "value too long for type character varying(2)", ""},
			{"numeric_typmod", "hs_record_numeric", "n=>1000", "22003", "numeric field overflow", "A field with precision 4, scale 1 must round to an absolute value less than 10^3."},
			{"descriptor_order", "hs_record_order", "a=>bad,z=>oops", "22P02", `invalid input syntax for type integer: "oops"`, ""},
		} {
			for _, operator := range []bool{false, true} {
				t.Run(tc.name+"/operator="+strconv.FormatBool(operator), func(t *testing.T) {
					want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					want.Detail = tc.detail
					literal := "SELECT " + hstoreRecordCall(operator, "NULL::"+tc.typ, hstoreLiteral(tc.source)+"::hstore")
					fixture.CompositeReferenceError(t, p, c, literal, "execute", want)
					bound := "SELECT " + hstoreRecordCall(operator, "NULL::"+tc.typ, "$1::hstore")
					fixture.CompositeReferenceError(t, p, c, bound, "execute", want, tc.source)
				})
			}
		}
	})
}

func TestReferenceHstoreRecordRowtypeDiagnostics(t *testing.T) {
	hstoreRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreRecordSetup(t, p, b, c)
		for _, tc := range []struct{ q, code, message string }{
			{"SELECT hstore(NULL::record)", "42809", "record type has not been registered"},
			{"SELECT populate_record(NULL::record,''::hstore)", "42809", "record type has not been registered"},
			{"SELECT (NULL::record #= ''::hstore)", "42809", "record type has not been registered"},
			{"SELECT populate_record(1,NULL::hstore)", "42804", "first argument must be a rowtype"},
			{"SELECT (1 #= NULL::hstore)", "42804", "first argument must be a rowtype"},
			{"SELECT populate_record(NULL::integer,NULL::hstore)", "42804", "first argument must be a rowtype"},
			{"SELECT (NULL::integer #= NULL::hstore)", "42804", "first argument must be a rowtype"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				fixture.CompositeReferenceError(t, p, c, tc.q, "execute", fixture.ControlsReferenceDiagnostic(tc.code, tc.message))
			})
		}
		for _, prefix := range []string{"SELECT ", "/*雪*/ SELECT "} {
			q := prefix + "hstore(1)"
			want := fixture.ControlsReferenceDiagnostic("42883", "function hstore(integer) does not exist")
			want.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "hstore")]) + 1)
			want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			fixture.CompositeReferenceError(t, p, c, q, "prepare", want)
		}
	})
}

func TestReferenceHstoreRecordOuterParameterDiagnostics(t *testing.T) {
	hstoreRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreRecordSetup(t, p, b, c)
		for _, operator := range []bool{false, true} {
			q := "SELECT " + hstoreRecordCall(operator, "$1::hs_record_pair", "$2::hstore")
			bad := hstoreParameterDiagnostic("22P02", `malformed record literal: "(1)"`, 1)
			bad.Detail = "Too few columns."
			fixture.CompositeReferenceError(t, p, c, q, "execute", bad, "(1)", "i=>42")
			bad = hstoreParameterDiagnostic("42601", `syntax error in hstore, near "b" at position 2`, 2)
			fixture.CompositeReferenceError(t, p, c, q, "execute", bad, "(7,keep)", "a=b")
		}
	})
}
