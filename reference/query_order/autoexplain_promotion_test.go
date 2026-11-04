package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func autoExplainPromotionWarning(t *testing.T, notices *[]fixture.Diagnostic, code, message, hint string) {
	t.Helper()
	want := []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: code, Message: message, Hint: hint}}
	if !reflect.DeepEqual(*notices, want) {
		t.Fatalf("promotion diagnostics=%#v want %#v", *notices, want)
	}
	*notices = nil
}

func autoExplainPromotionRunner(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) func(fixture.Step) {
	t.Helper()
	return func(s fixture.Step) {
		t.Helper()
		// Exercise actual driver preparation for assignments and activation, not
		// SQL PREPARE or unrelated transaction/role control statements.
		s.Prepared = prepared && (strings.HasPrefix(s.Query, "SET auto_explain.") || strings.HasPrefix(s.Query, "SET LOCAL auto_explain.") || strings.HasPrefix(s.Query, "LOAD "))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func autoExplainPromotionRevoke(t *testing.T, c *sql.Conn, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
	defer cancel()
	if t.Failed() {
		_, _ = c.ExecContext(ctx, "ROLLBACK")
	}
	for _, query := range []string{"RESET ROLE", "REVOKE SET ON PARAMETER " + name + " FROM ae_promotion_reader"} {
		if _, err := c.ExecContext(ctx, query); err != nil {
			t.Error("parameter privilege cleanup", err)
		}
	}
}

func autoExplainPromotionCases(t *testing.T) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) { autoExplainPromotionRouteCases(t, prepared) })
	}
}

func autoExplainPromotionRouteCases(t *testing.T, prepared bool) {
	t.Helper()
	for _, tc := range []struct {
		name, setting, input, grant string
		allowed, invalid, other     bool
	}{
		{"ungranted", "log_analyze", "true", "none", false, false, false},
		{"permission_before_parse", "log_analyze", "bogus", "none", false, true, false},
		{"grant_before_set_exact_name", "log_analyze", "true", "before", true, false, true},
		{"grant_after_set", "log_analyze", "true", "after", true, false, false},
		{"revoke_before_load", "log_analyze", "true", "revoked", false, false, false},
		{"granted_invalid", "log_analyze", "bogus", "after", true, true, false},
		{"granted_invalid_enum_hint", "log_format", "bogus", "after", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"ae_promotion_reader"})
			autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
				run := autoExplainPromotionRunner(t, p, b, c, prepared)
				name := "auto_explain." + tc.setting
				run(fixture.Exec("CREATE ROLE ae_promotion_reader", 0))
				if tc.grant == "before" || tc.grant == "after" {
					defer autoExplainPromotionRevoke(t, c, name)
				}
				if tc.grant == "before" || tc.grant == "revoked" {
					run(fixture.Exec("GRANT SET ON PARAMETER "+name+" TO ae_promotion_reader", 0))
				}
				run(fixture.Exec("SET ROLE ae_promotion_reader", 0))
				run(fixture.Exec("SET "+name+" = '"+tc.input+"'", 0))
				if tc.other {
					run(fixture.Exec("SET auto_explain.log_verbose = true", 0))
				}
				run(fixture.Exec("RESET ROLE", 0))
				if tc.grant == "after" {
					run(fixture.Exec("GRANT SET ON PARAMETER "+name+" TO ae_promotion_reader", 0))
				}
				if tc.grant == "revoked" {
					run(fixture.Exec("REVOKE SET ON PARAMETER "+name+" FROM ae_promotion_reader", 0))
				}
				run(fixture.Exec("LOAD 'auto_explain'", 0))
				if !tc.allowed {
					autoExplainPromotionWarning(t, notices, "42501", `permission denied to set parameter "`+name+`"`, "")
				} else if tc.setting == "log_format" {
					autoExplainPromotionWarning(t, notices, "22023", `invalid value for parameter "auto_explain.log_format": "bogus"`, "Available values: text, xml, json, yaml.")
				} else if tc.invalid {
					autoExplainPromotionWarning(t, notices, "22023", `parameter "auto_explain.log_analyze" requires a Boolean value`, "")
				} else if tc.other {
					autoExplainPromotionWarning(t, notices, "42501", `permission denied to set parameter "auto_explain.log_verbose"`, "")
				} else {
					autoExplainQuiet(t, notices)
				}
				want := "off"
				if tc.setting == "log_format" {
					want = "text"
				} else if tc.allowed && !tc.invalid {
					want = "on"
				}
				run(fixture.Select("SHOW "+name, []string{name}, []string{"TEXT"}, [][]any{{want}}))
				if tc.other {
					run(fixture.Select("SHOW auto_explain.log_verbose", []string{"auto_explain.log_verbose"}, []string{"TEXT"}, [][]any{{"off"}}))
				}
				run(fixture.Exec("LOAD 'auto_explain'", 0))
				autoExplainQuiet(t, notices)
			})
		})
	}
	for _, tc := range []struct {
		name                          string
		sessionUser, localUser, grant bool
		atLoad, commit                string
	}{
		{"denied_set", true, false, false, "100ms", "100ms"},
		{"denied_masked_set", true, false, false, "300ms", "100ms"},
		{"denied_local", false, true, false, "200ms", "200ms"},
		{"granted_masked_set", true, false, true, "300ms", "200ms"},
	} {
		for _, ending := range []string{"COMMIT", "ROLLBACK"} {
			t.Run(tc.name+"_"+ending, func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"ae_promotion_reader"})
				autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
					run := autoExplainPromotionRunner(t, p, b, c, prepared)
					show := func(value string) {
						run(fixture.Select("SHOW auto_explain.log_min_duration", []string{"auto_explain.log_min_duration"}, []string{"TEXT"}, [][]any{{value}}))
					}
					run(fixture.Exec("CREATE ROLE ae_promotion_reader", 0))
					if tc.grant {
						run(fixture.Exec("GRANT SET ON PARAMETER auto_explain.log_min_duration TO ae_promotion_reader", 0))
						defer autoExplainPromotionRevoke(t, c, "auto_explain.log_min_duration")
					}
					run(fixture.Exec("SET auto_explain.log_min_duration=100", 0))
					run(fixture.Exec("BEGIN", 0))
					if tc.sessionUser {
						run(fixture.Exec("SET ROLE ae_promotion_reader", 0))
					}
					run(fixture.Exec("SET auto_explain.log_min_duration=200", 0))
					run(fixture.Exec("RESET ROLE", 0))
					if tc.name != "denied_set" {
						if tc.localUser {
							run(fixture.Exec("SET ROLE ae_promotion_reader", 0))
						}
						run(fixture.Exec("SET LOCAL auto_explain.log_min_duration=300", 0))
						run(fixture.Exec("RESET ROLE", 0))
					}
					run(fixture.Exec("LOAD 'auto_explain'", 0))
					if tc.grant {
						autoExplainQuiet(t, notices)
					} else {
						autoExplainPromotionWarning(t, notices, "42501", `permission denied to set parameter "auto_explain.log_min_duration"`, "")
					}
					show(tc.atLoad)
					run(fixture.Exec(ending, 0))
					want := tc.commit
					if ending == "ROLLBACK" {
						want = "100ms"
					}
					show(want)
					run(fixture.Exec("LOAD 'auto_explain'", 0))
					autoExplainQuiet(t, notices)
				})
			})
		}
	}
	t.Run("savepoint_and_original_superuser_context", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ae_original_super", "ae_promotion_reader"})
		autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := autoExplainPromotionRunner(t, p, b, c, prepared)
			run(fixture.Exec("CREATE ROLE ae_original_super SUPERUSER", 0))
			run(fixture.Exec("CREATE ROLE ae_promotion_reader", 0))
			run(fixture.Exec("SET ROLE ae_original_super", 0))
			run(fixture.Exec("SET auto_explain.log_analyze=true", 0))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("ALTER ROLE ae_original_super NOSUPERUSER", 0))
			run(fixture.Exec("SET auto_explain.log_min_duration=100", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT a", 0))
			run(fixture.Exec("SET ROLE ae_promotion_reader", 0))
			run(fixture.Exec("SET auto_explain.log_min_duration=200", 0))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("LOAD 'auto_explain'", 0))
			autoExplainPromotionWarning(t, notices, "42501", `permission denied to set parameter "auto_explain.log_min_duration"`, "")
			run(fixture.Select("SHOW auto_explain.log_analyze", []string{"auto_explain.log_analyze"}, []string{"TEXT"}, [][]any{{"on"}}))
			run(fixture.Exec("SET auto_explain.log_min_duration=300", 0))
			run(fixture.Exec("ROLLBACK TO a", 0))
			run(fixture.Select("SHOW auto_explain.log_min_duration", []string{"auto_explain.log_min_duration"}, []string{"TEXT"}, [][]any{{"100ms"}}))
			run(fixture.Exec("COMMIT", 0))
			run(fixture.Exec("LOAD 'auto_explain'", 0))
			autoExplainQuiet(t, notices)
		})
	})
	t.Run("distinct_equal_assignments", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ae_promotion_reader"})
		autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := autoExplainPromotionRunner(t, p, b, c, prepared)
			for _, query := range []string{"CREATE ROLE ae_promotion_reader", "BEGIN", "SET ROLE ae_promotion_reader", "SET LOCAL auto_explain.log_analyze='bogus'", "SAVEPOINT b", "SET LOCAL auto_explain.log_analyze='bogus'", "RESET ROLE", "LOAD 'auto_explain'"} {
				run(fixture.Exec(query, 0))
			}
			want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42501", Message: `permission denied to set parameter "auto_explain.log_analyze"`}
			if !reflect.DeepEqual(*notices, []fixture.Diagnostic{want, want}) {
				t.Fatal("distinct equal original assignments must warn twice, image copies only once", *notices)
			}
			*notices = nil
			run(fixture.Exec("ROLLBACK TO b", 0))
			run(fixture.Select("SHOW auto_explain.log_analyze", []string{"auto_explain.log_analyze"}, []string{"TEXT"}, [][]any{{"off"}}))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Exec("LOAD 'auto_explain'", 0))
			autoExplainQuiet(t, notices)
		})
	})
	t.Run("retained_set_local_warning_order", func(t *testing.T) {
		autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := autoExplainPromotionRunner(t, p, b, c, prepared)
			for _, query := range []string{"SET auto_explain.log_format='json'", "BEGIN", "SET auto_explain.log_format='bad_set'", "SET LOCAL auto_explain.log_format='bad_local'", "SAVEPOINT b", "SET LOCAL auto_explain.log_format='bad_nested'", "LOAD 'auto_explain'"} {
				run(fixture.Exec(query, 0))
			}
			// One variable's retained GUC stack has defined bottom-to-top order;
			// the masked SET precedes its LOCAL. No map/name order is assumed.
			var want []fixture.Diagnostic
			for _, value := range []string{"bad_set", "bad_local", "bad_nested"} {
				want = append(want, fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: `invalid value for parameter "auto_explain.log_format": "` + value + `"`, Hint: "Available values: text, xml, json, yaml."})
			}
			if !reflect.DeepEqual(*notices, want) {
				t.Fatalf("retained assignment warning order=%#v want %#v", *notices, want)
			}
			*notices = nil
			for _, query := range []string{"ROLLBACK TO b", "COMMIT", "LOAD 'auto_explain'"} {
				run(fixture.Exec(query, 0))
				run(fixture.Select("SHOW auto_explain.log_format", []string{"auto_explain.log_format"}, []string{"TEXT"}, [][]any{{"json"}}))
				autoExplainQuiet(t, notices)
			}
		})
	})
}
