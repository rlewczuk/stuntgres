package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceIndexPropertiesDiagnostics(t *testing.T) {
	indexPropertiesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		indexPropertiesSeed(t, p, b, c)
		type diagnosticCase struct {
			q       string
			want    referenceDiagnostic
			runtime bool
			args    []any
		}
		cases := []diagnosticCase{}
		for _, tc := range []struct{ call, signature string }{
			{"pg_indexam_has_property()", "pg_indexam_has_property()"},
			{"pg_indexam_has_property(403)", "pg_indexam_has_property(integer)"},
			{"pg_indexam_has_property(403,'can_order',1)", "pg_indexam_has_property(integer, unknown, integer)"},
			{"pg_index_has_property()", "pg_index_has_property()"},
			{"pg_index_has_property('ip_default')", "pg_index_has_property(unknown)"},
			{"pg_index_has_property('ip_default','index_scan',1)", "pg_index_has_property(unknown, unknown, integer)"},
			{"pg_index_column_has_property()", "pg_index_column_has_property()"},
			{"pg_index_column_has_property('ip_default',1)", "pg_index_column_has_property(unknown, integer)"},
			{"pg_index_column_has_property('ip_default',1,'asc',1)", "pg_index_column_has_property(unknown, integer, unknown, integer)"},
			{"pg_indexam_has_property(true,'can_order')", "pg_indexam_has_property(boolean, unknown)"},
			{"pg_index_has_property(true,'index_scan')", "pg_index_has_property(boolean, unknown)"},
			{"pg_index_column_has_property(true,1,'asc')", "pg_index_column_has_property(boolean, integer, unknown)"},
			{"pg_index_has_property(1.5,'index_scan')", "pg_index_has_property(numeric, unknown)"},
			{"pg_indexam_has_property(403,true)", "pg_indexam_has_property(integer, boolean)"},
			{"pg_index_has_property(NULL::regclass,true)", "pg_index_has_property(regclass, boolean)"},
			{"pg_index_column_has_property('ip_default',1,true)", "pg_index_column_has_property(unknown, integer, boolean)"},
			{"pg_index_column_has_property('ip_default',1::bigint,'asc')", "pg_index_column_has_property(unknown, bigint, unknown)"},
			{"pg_index_has_propert('ip_default','index_scan')", "pg_index_has_propert(unknown, unknown)"},
			{`"PG_INDEX_HAS_PROPERTY"('ip_default','index_scan')`, "PG_INDEX_HAS_PROPERTY(unknown, unknown)"},
			{"public.pg_index_has_property('ip_default','index_scan')", "public.pg_index_has_property(unknown, unknown)"},
		} {
			w := controlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
			w.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			w.Position = "8"
			cases = append(cases, diagnosticCase{q: "SELECT " + tc.call + " FROM ip_data", want: w})
		}
		for _, tc := range []struct {
			q, code, message, token string
			runtime                 bool
		}{
			{"SELECT absent_ip_schema.pg_index_has_property('ip_default','index_scan')", "3F000", `schema "absent_ip_schema" does not exist`, "absent_ip_schema", false},
			{"SELECT pg_index_column_has_property(NULL::regclass,'bad','asc')", "22P02", `invalid input syntax for type integer: "bad"`, "'bad'", false},
			{"SELECT pg_index_column_has_property(NULL::regclass,missing,'asc') FROM ip_data", "42703", `column "missing" does not exist`, "missing", false},
			{"SELECT pg_index_has_property('ip_absent','index_scan')", "42P01", `relation "ip_absent" does not exist`, "'ip_absent'", false},
			{"SELECT pg_index_has_property('ip_absent'::text,'index_scan')", "42P01", `relation "ip_absent" does not exist`, "", true},
			{"SELECT pg_indexam_has_property('bad','can_order')", "22P02", `invalid input syntax for type oid: "bad"`, "'bad'", false},
			{"SELECT pg_indexam_has_property('4294967296','can_order')", "22003", `value "4294967296" is out of range for type oid`, "'4294967296'", false},
			{"SELECT pg_index_has_property(-1::bigint,'index_scan')", "22003", "OID out of range", "", true},
			{"SELECT pg_index_has_property(4294967296::bigint,'index_scan')", "22003", "OID out of range", "", true},
		} {
			w := controlsReferenceDiagnostic(tc.code, tc.message)
			if tc.token != "" {
				w.Position = referencePosition(utf8.RuneCountInString(tc.q[:strings.Index(tc.q, tc.token)]) + 1)
			}
			cases = append(cases, diagnosticCase{q: tc.q, want: w, runtime: tc.runtime})
		}
		q := "SELECT 'été', pg_index_has_property()"
		w := controlsReferenceDiagnostic("42883", "function pg_index_has_property() does not exist")
		w.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		w.Position = referencePosition(utf8.RuneCountInString(q[:strings.Index(q, "pg_index")]) + 1)
		cases = append(cases, diagnosticCase{q: q, want: w})
		for _, tc := range []struct {
			q             string
			args          []any
			code, message string
		}{
			{"SELECT pg_indexam_has_property($1,$2)", []any{"bad", "can_order"}, "22P02", `invalid input syntax for type oid: "bad"`},
			{"SELECT pg_index_has_property($1,$2)", []any{"ip_absent", "index_scan"}, "42P01", `relation "ip_absent" does not exist`},
			{"SELECT pg_index_column_has_property($1,$2,$3)", []any{"ip_default", "bad", "asc"}, "22P02", `invalid input syntax for type integer: "bad"`},
		} {
			w := controlsReferenceDiagnostic(tc.code, tc.message)
			parameter := "$1"
			if strings.Contains(tc.q, "column") {
				parameter = "$2"
			}
			w.Context = "unnamed portal parameter " + parameter + " = '...'"
			cases = append(cases, diagnosticCase{q: tc.q, want: w, runtime: true, args: tc.args})
		}
		for _, tc := range cases {
			if !strings.Contains(tc.q, " FROM ") {
				tc.q += " FROM ip_data"
			}
			for _, prepared := range []bool{false, true} {
				name := "/direct"
				if prepared {
					name = "/prepared"
				}
				t.Run(tc.q+name, func(t *testing.T) {
					s := referenceErrorDiagnostic(tc.q, tc.want, tc.args...)
					s.selectRows = true
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared && !tc.runtime {
						s.errorStage = "prepare"
					}
					referenceRunStep(t, p.ctx, c, b.recorder, s)
				})
			}
		}
	})
}
