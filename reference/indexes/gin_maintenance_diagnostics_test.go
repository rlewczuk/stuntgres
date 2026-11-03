package reference_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func ginMaintPosition(query, token string) string {
	return strconv.Itoa(utf8.RuneCountInString(query[:strings.Index(query, token)]) + 1)
}

func TestReferenceGINMaintenanceDiagnostics(t *testing.T) {
	const resolutionHint = "No function matches the given name and argument types. You might need to add explicit type casts."
	// The positions below are independent released-source expectations. Literal
	// regclass input errors point at the string token, resolution at the call;
	// runtime input/body errors have no statement position. Any oracle mismatch
	// must fail with the complete actual diagnostic, not become a learned golden.
	for _, tc := range []struct {
		name, call, code, message, positionToken string
		parse                                    bool
	}{
		{"no_arguments", "gin_clean_pending_list()", "42883", "function gin_clean_pending_list() does not exist", "gin_clean_pending_list", true},
		{"boolean", "gin_clean_pending_list(true)", "42883", "function gin_clean_pending_list(boolean) does not exist", "gin_clean_pending_list", true},
		{"two_arguments", "gin_clean_pending_list(NULL::regclass,NULL::regclass)", "42883", "function gin_clean_pending_list(regclass, regclass) does not exist", "gin_clean_pending_list", true},
		{"missing_function", "gin_clean_pending_lists(NULL::regclass)", "42883", "function gin_clean_pending_lists(regclass) does not exist", "gin_clean_pending_lists", true},
		{"unknown_literal", "gin_clean_pending_list('gin_maint_absent'::regclass)", "42P01", `relation "gin_maint_absent" does not exist`, "'gin_maint_absent'", true},
		{"unknown_untyped_literal", "gin_clean_pending_list('gin_maint_absent')", "42P01", `relation "gin_maint_absent" does not exist`, "'gin_maint_absent'", true},
		{"unknown_text", "gin_clean_pending_list('gin_maint_absent'::text)", "42P01", `relation "gin_maint_absent" does not exist`, "", false},
		{"zero_oid", "gin_clean_pending_list(0::regclass)", "XX000", "could not open relation with OID 0", "", false},
		{"zero_smallint", "gin_clean_pending_list(0::smallint)", "XX000", "could not open relation with OID 0", "", false},
		{"zero_integer", "gin_clean_pending_list(0::integer)", "XX000", "could not open relation with OID 0", "", false},
		{"zero_bigint", "gin_clean_pending_list(0::bigint)", "XX000", "could not open relation with OID 0", "", false},
		{"nonexistent_oid", "gin_clean_pending_list(17::oid::regclass)", "XX000", "could not open relation with OID 17", "", false},
	} {
		for _, prefix := range []string{"", "/*é😀*/ "} {
			t.Run("GinMaintDiagnosticPositions/"+tc.name+"/"+prefix, func(t *testing.T) {
				classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					conn, run := classConn(t, p, b)
					ginMaintSeed(run, false)
					q := prefix + "SELECT " + tc.call
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					if tc.code == "42883" {
						d.Hint = resolutionHint
					}
					if tc.positionToken != "" {
						d.Position = ginMaintPosition(q, tc.positionToken)
					}
					s := fixture.ErrorDiagnostic(q, d)
					s.ErrorStage = "execute"
					run(s)
					s.Prepared = true
					if tc.parse {
						s.ErrorStage = "prepare"
					}
					run(s)
					if !tc.parse {
						s.Prepared = false
						fixture.RunStep(t, p.Ctx, classHeld(t, p, conn, q), b.Recorder, s)
					}
					run(ginIDs(ginMaintIDsSQL, []int64{1}))
					run(ginMaintPositive(ginMaintPositiveSQL))
					run(ginMaintResult(ginMaintCleanSQL, int64(0)))
				})
			})
		}
	}

	for _, tc := range []struct{ target, ddl, message string }{
		{"gin_maint_oracle", "", `"gin_maint_oracle" is not an index`},
		{"gin_maint_view", "CREATE VIEW gin_maint_view AS SELECT id FROM gin_maint_oracle", `"gin_maint_view" is not an index`},
		{"gin_maint_sequence", "CREATE SEQUENCE gin_maint_sequence", `"gin_maint_sequence" is not an index`},
		{"gin_maint_btree", "CREATE INDEX gin_maint_btree ON gin_maint_oracle(id)", `"gin_maint_btree" is not a GIN index`},
	} {
		t.Run("GinMaintTargetDiagnostics/"+tc.target, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				ginMaintSeed(run, false)
				if tc.ddl != "" {
					run(fixture.Exec(tc.ddl, 0))
				}
				for _, prefix := range []string{"", "/*é😀*/ "} {
					q := prefix + "SELECT gin_clean_pending_list('" + tc.target + "'::regclass)"
					s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42809", tc.message))
					s.ErrorStage = "execute"
					classRoutes(t, p, b, conn, s)
					fixture.RunStep(t, p.Ctx, classHeld(t, p, conn, q), b.Recorder, s)
				}
				run(ginMaintPositive(ginMaintPositiveSQL))
				run(ginMaintResult(ginMaintCleanSQL, int64(0)))
			})
		})
	}

	t.Run("GinMaintOwnerDiagnostic", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"gin_maint_reader"})
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			ginMaintSeed(run, false)
			run(fixture.Exec("CREATE ROLE gin_maint_reader", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO gin_maint_reader", 0))
			run(fixture.Exec("GRANT SELECT,MAINTAIN ON gin_maint_oracle TO gin_maint_reader", 0))
			defer conn.ExecContext(p.Ctx, "RESET ROLE")
			run(fixture.Exec("SET ROLE gin_maint_reader", 0))
			for _, prefix := range []string{"", "/*é😀*/ "} {
				q := prefix + ginMaintCleanSQL
				s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "must be owner of index gin_maint_oracle_i"))
				s.ErrorStage = "execute"
				classRoutes(t, p, b, conn, s)
				fixture.RunStep(t, p.Ctx, classHeld(t, p, conn, q), b.Recorder, s)
			}
			run(fixture.Exec("RESET ROLE", 0))
			// Denial must leave the owner's pending work intact.
			run(ginMaintPositive(ginMaintPositiveSQL))
			run(ginMaintResult(ginMaintCleanSQL, int64(0)))
			run(fixture.Exec("DROP OWNED BY gin_maint_reader", 0))
			run(fixture.Exec("DROP ROLE gin_maint_reader", 0))
		})
	})

	t.Run("GinMaintOtherSessionTemporary", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			owner, ownerRun := classConn(t, p, b)
			other, _ := classConn(t, p, b)
			ownerRun(fixture.Exec("CREATE TEMP TABLE gin_maint_temp(id integer,a integer[])", 0))
			ownerRun(fixture.Exec("CREATE INDEX gin_maint_temp_i ON gin_maint_temp USING gin(a)", 0))
			ownerRun(fixture.Exec("INSERT INTO gin_maint_temp VALUES(1,'{2}')", 1))
			var oid int64
			if err := owner.QueryRowContext(p.Ctx, "SELECT 'gin_maint_temp_i'::regclass::oid::bigint").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			for _, prefix := range []string{"", "/*é😀*/ "} {
				q := prefix + "SELECT gin_clean_pending_list($1::regclass)"
				s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "cannot access temporary indexes of other sessions"), oid)
				s.ErrorStage = "execute"
				classRoutes(t, p, b, other, s)
				fixture.RunStep(t, p.Ctx, classHeld(t, p, other, q), b.Recorder, s)
			}
			ownerRun(ginMaintPositive("SELECT gin_clean_pending_list('gin_maint_temp_i'::regclass)>0 AS cleaned"))
			ownerRun(ginMaintResult("SELECT gin_clean_pending_list('gin_maint_temp_i'::regclass)", int64(0)))
		})
	})

	t.Run("GinMaintInvalidIndexDebug", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
			run(fixture.Exec("CREATE TABLE gin_maint_invalid(n integer)", 0))
			run(fixture.Exec("INSERT INTO gin_maint_invalid VALUES(0)", 1))
			s := fixture.ErrorDiagnostic("CREATE INDEX CONCURRENTLY gin_maint_invalid_i ON gin_maint_invalid USING gin((ARRAY[10/n]))", fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Select("SELECT indisvalid,indisready,indislive FROM pg_index WHERE indexrelid='gin_maint_invalid_i'::regclass", []string{"indisvalid", "indisready", "indislive"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, false, true}}))
			q := "SELECT gin_clean_pending_list('gin_maint_invalid_i'::regclass)"
			run(fixture.Exec("SET client_min_messages=warning", 0))
			notices = nil
			run(ginMaintResult(q, int64(0)))
			if len(notices) != 0 {
				t.Fatalf("DEBUG1 escaped warning-level filtering: %#v", notices)
			}
			run(fixture.Exec("SET client_min_messages=debug1", 0))
			want := fixture.Diagnostic{Severity: "DEBUG", SeverityUnlocalized: "DEBUG", Code: "55000", Message: `index "gin_maint_invalid_i" is not valid`}
			for _, route := range []string{"direct", "fresh", "held"} {
				notices = nil
				step := ginMaintResult(q, int64(0))
				switch route {
				case "direct":
					run(step)
				case "fresh":
					run(fixture.Prepared(step))
				case "held":
					fixture.RunStep(t, p.Ctx, classHeld(t, p, conn, q), recorder, step)
				}
				if !reflect.DeepEqual(notices, []fixture.Diagnostic{want}) {
					t.Fatalf("%s invalid-index DEBUG1 got %#v want %#v", route, notices, []fixture.Diagnostic{want})
				}
			}
			run(fixture.Exec("SET client_min_messages=notice", 0))
			run(ginIDs("SELECT n AS id FROM gin_maint_invalid ORDER BY n", []int64{0}))
		})
	})
}
