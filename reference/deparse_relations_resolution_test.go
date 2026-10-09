package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceDeparseRelationsResolutionDiagnostics(t *testing.T) {
	deparseRelationsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		deparseRelationsSeed(t, p, b, c)
		missingHint := "No function matches the given name and argument types. You might need to add explicit type casts."
		ambiguousHint := "Could not choose a best candidate function. You might need to add explicit type casts."
		type diagnosticCase struct {
			q       string
			want    referenceDiagnostic
			execute bool
			args    []any
		}
		cases := []diagnosticCase{}
		for _, tc := range []struct{ call, sig string }{
			{"pg_get_indexdef()", "pg_get_indexdef()"}, {"pg_get_indexdef(NULL,1)", "pg_get_indexdef(unknown, integer)"}, {"pg_get_indexdef(NULL,1,false,1)", "pg_get_indexdef(unknown, integer, boolean, integer)"},
			{"pg_get_viewdef()", "pg_get_viewdef()"}, {"pg_get_viewdef(NULL,true,1)", "pg_get_viewdef(unknown, boolean, integer)"},
			{"pg_get_viewdef(true)", "pg_get_viewdef(boolean)"}, {"pg_get_viewdef('v'::text,1)", "pg_get_viewdef(text, integer)"}, {"pg_get_viewdef(0::oid,1::bigint)", "pg_get_viewdef(oid, bigint)"},
			{"pg_get_indexdef('ix'::text)", "pg_get_indexdef(text)"}, {"pg_get_indexdef(1.5)", "pg_get_indexdef(numeric)"},
			{`"PG_GET_VIEWDEF"('v')`, `PG_GET_VIEWDEF(unknown)`}, {"public.pg_get_viewdef('v')", "public.pg_get_viewdef(unknown)"},
		} {
			q := "SELECT " + tc.call
			w := controlsReferenceDiagnostic("42883", "function "+tc.sig+" does not exist")
			w.Hint = missingHint
			w.Position = "8"
			cases = append(cases, diagnosticCase{q: q, want: w})
		}
		for _, tc := range []struct{ call, sig string }{
			{"pg_get_viewdef(NULL,NULL)", "pg_get_viewdef(unknown, unknown)"}, {"pg_get_viewdef('v',NULL)", "pg_get_viewdef(unknown, unknown)"},
			{"pg_get_viewdef('v','false')", "pg_get_viewdef(unknown, unknown)"}, {"pg_get_viewdef(0::oid,NULL)", "pg_get_viewdef(oid, unknown)"},
			{"pg_get_viewdef('v'::regclass,NULL)", "pg_get_viewdef(regclass, unknown)"}, {"pg_get_viewdef($1,$2)", "pg_get_viewdef(unknown, unknown)"},
		} {
			w := controlsReferenceDiagnostic("42725", "function "+tc.sig+" is not unique")
			w.Hint = ambiguousHint
			w.Position = "8"
			args := []any(nil)
			if tc.call == "pg_get_viewdef($1,$2)" {
				args = []any{nil, nil}
			}
			cases = append(cases, diagnosticCase{q: "SELECT " + tc.call, want: w, args: args})
		}
		for _, tc := range []struct {
			q, code, message string
			execute          bool
		}{
			{"SELECT pg_get_viewdef('absent'::text)", "42P01", `relation "absent" does not exist`, true},
			{"SELECT pg_get_viewdef('public.absent'::text)", "42P01", `relation "public.absent" does not exist`, true},
			{"SELECT pg_get_viewdef('absent_schema.v'::text)", "3F000", `schema "absent_schema" does not exist`, true},
			{"SELECT pg_get_viewdef(''::text)", "42602", "invalid name syntax", true},
			{"SELECT pg_get_viewdef('a..b'::text)", "42602", "invalid name syntax", true},
			{`SELECT pg_get_viewdef('"unfinished'::text)`, "42602", "invalid name syntax", true},
			{"SELECT pg_get_viewdef('v',1)", "22P02", `invalid input syntax for type oid: "v"`, false},
			{"SELECT pg_get_indexdef('bad')", "22P02", `invalid input syntax for type oid: "bad"`, false},
			{"SELECT pg_get_indexdef('4294967296')", "22003", `value "4294967296" is out of range for type oid`, false},
			{"SELECT pg_get_indexdef(NULL::oid,missing,false) FROM t", "42703", `column "missing" does not exist`, false},
			{"SELECT pg_get_viewdef(NULL::text,missing) FROM t", "42703", `column "missing" does not exist`, false},
			{"SELECT pg_get_indexdef(NULL::oid,'bad',false)", "22P02", `invalid input syntax for type integer: "bad"`, false},
			{"SELECT pg_get_indexdef(NULL::oid,1,false,missing) FROM t", "42703", `column "missing" does not exist`, false},
			{"SELECT absent_schema.pg_get_viewdef('v')", "3F000", `schema "absent_schema" does not exist`, false},
		} {
			w := controlsReferenceDiagnostic(tc.code, tc.message)
			if !tc.execute {
				pos := 8
				if strings.Contains(tc.q, "missing") {
					pos = strings.Index(tc.q, "missing") + 1
				}
				if tc.code == "22P02" || tc.code == "22003" {
					pos = strings.Index(tc.q, "'") + 1
				}
				w.Position = referencePosition(pos)
			}
			cases = append(cases, diagnosticCase{q: tc.q, want: w, execute: tc.execute})
		}
		q := "SELECT 'été', pg_get_indexdef() FROM t"
		w := controlsReferenceDiagnostic("42883", "function pg_get_indexdef() does not exist")
		w.Hint = missingHint
		w.Position = referencePosition(utf8.RuneCountInString(q[:strings.Index(q, "pg_get")]) + 1)
		cases = append(cases, diagnosticCase{q: q, want: w})
		for _, tc := range []struct{ value, code, message string }{
			{"bad", "22P02", `invalid input syntax for type oid: "bad"`},
			{"4294967296", "22003", `value "4294967296" is out of range for type oid`},
		} {
			w := controlsReferenceDiagnostic(tc.code, tc.message)
			w.Context = "unnamed portal parameter $1 = '...'"
			cases = append(cases, diagnosticCase{q: "SELECT pg_get_indexdef($1)", want: w, execute: true, args: []any{tc.value}})
		}
		for _, tc := range cases {
			for _, prepared := range []bool{false, true} {
				t.Run(tc.q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					var err error
					stage := "execute"
					if prepared {
						var s *sql.Stmt
						s, err = c.PrepareContext(p.ctx, tc.q)
						if err != nil {
							stage = "prepare"
						} else {
							defer s.Close()
							var r *sql.Rows
							r, err = s.QueryContext(p.ctx, tc.args...)
							if r != nil {
								r.Close()
								t.Fatal("error query published rows")
							}
						}
					} else {
						var r *sql.Rows
						r, err = c.QueryContext(p.ctx, tc.q, tc.args...)
						if r != nil {
							r.Close()
							t.Fatal("error query published rows")
						}
					}
					if err == nil {
						t.Fatal("expected error")
					}
					if prepared && (stage == "execute") != tc.execute {
						t.Errorf("diagnostic stage %s want execute=%v", stage, tc.execute)
					}
					referenceAssertDiagnostic(t, err, &tc.want)
				})
			}
		}
		for _, q := range []string{"SELECT pg_get_viewdef(0,NULL) AS definition", "SELECT pg_get_viewdef($1,1) AS definition", "SELECT pg_get_indexdef($1) AS definition"} {
			t.Run(q, func(t *testing.T) {
				s := deparseRelationsTextStep(t, b, q, nil)
				if strings.Contains(q, "$1") {
					s.args = []any{nil}
					s = referencePrepared(s)
				}
				referenceRunStep(t, p.ctx, c, b.recorder, s)
			})
		}
	})
}
