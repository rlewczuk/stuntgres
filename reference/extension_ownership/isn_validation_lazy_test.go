package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Registration boundaries and promotion diagnostics are pinned PostgreSQL 18.6
// observations validating the independently specified REL_18_6 expectations.
// Each branch owns a NEW connector/pool and physical connection while A stays
// pinned. No branch can accidentally inherit a module-loaded idle connection.
func TestReferenceIsnValidationLazyRegistration(t *testing.T) {
	isnValidationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn) {
		runA := isnConversionRun(t, p, b, a)
		runA(fixture.Exec("CREATE EXTENSION isn", 0))
		var installerPID int64
		if b.PG {
			installerPID = fixture.OwnershipID(t, p, a, "SELECT pg_backend_pid()::bigint")
		}
		for _, branch := range []string{"untouched", "valid_placeholder", "invalid_placeholder", "local_rollback", "load_rollback", "prepare_only", "zero_row_control", "zero_row_function", "constant_input", "drop_recreate", "read_only"} {
			t.Run(branch, func(t *testing.T) {
				var notices []fixture.Diagnostic
				db, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				defer c.Close()
				defer isnValidationCleanup(t, c)
				noticeFixture := fixture.ControlsReferenceFixture{Notices: &notices}
				current := fixture.Backend{DB: db, Recorder: recorder, PG: b.PG}
				run := isnConversionRun(t, p, current, c)
				if b.PG && fixture.OwnershipID(t, p, c, "SELECT pg_backend_pid()::bigint") == installerPID {
					t.Fatal("registration branch reused the installation backend")
				}
				// Keep each registration boundary independently identified.
				step := func(label string, s fixture.Step) {
					t.Run(label, func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, recorder, s) })
				}
				setting := func(label string, want any) {
					step(label, fixture.Select("SELECT current_setting('isn.weak',true) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{want}}))
				}
				show := func(label, want string) {
					step(label, fixture.Select("SHOW isn.weak", []string{"isn.weak"}, []string{"TEXT"}, [][]any{{want}}))
				}
				setting("01_before_load", nil)
				switch branch {
				case "untouched":
					step("02_missing_show", isnConversionRuntimeError("SHOW isn.weak", isnConversionDiagnostic("42704", `unrecognized configuration parameter "isn.weak"`)))
					step("03_strict_null", isnValidationBool("SELECT public.isn_weak(NULL::boolean) AS v", nil))
					setting("04_after_null", nil)
					step("05_getter", isnValidationBool("SELECT public.isn_weak() AS v", false))
					show("06_loaded_show", "off")
					step("07_bad_boolean", isnConversionRuntimeError("SET isn.weak='bogus'", isnConversionDiagnostic("22023", `parameter "isn.weak" requires a Boolean value`)))
					show("08_after_failure", "off")
				case "valid_placeholder", "invalid_placeholder":
					value, promoted := "true", true
					if branch == "invalid_placeholder" {
						value, promoted = "bogus", false
					}
					step("02_set_placeholder", fixture.Exec("SET isn.weak="+isnConversionLiteral(value), 0))
					show("03_placeholder_show", value)
					step("04_promote", isnValidationBool("SELECT public.isn_weak() AS v", promoted))
					t.Run("04_promotion_notice", func(t *testing.T) {
						code, message := "", ""
						if branch == "invalid_placeholder" {
							code, message = "22023", `parameter "isn.weak" requires a Boolean value`
						}
						noticeFixture.NoticesWant(t, code, message)
					})
					canonical := "on"
					if !promoted {
						canonical = "off"
					}
					show("05_promoted_show", canonical)
					step("06_reset", fixture.Exec("RESET isn.weak", 0))
					show("07_reset_show", "off")
					step("08_getter_again", isnValidationBool("SELECT public.isn_weak() AS v", false))
				case "local_rollback", "load_rollback":
					step("02_begin", fixture.Exec("BEGIN", 0))
					local := branch == "local_rollback"
					if local {
						step("03_local_placeholder", fixture.Exec("SET LOCAL isn.weak='on'", 0))
					}
					step("04_load", isnValidationBool("SELECT public.isn_weak() AS v", local))
					step("05_rollback", fixture.Exec("ROLLBACK", 0))
					show("06_definition_survives", "off")
				case "prepare_only":
					stmt, err := c.PrepareContext(p.Ctx, "SELECT public.isn_weak() AS v")
					if err != nil {
						t.Fatal(err)
					}
					if err := stmt.Close(); err != nil {
						t.Fatal(err)
					}
					setting("02_after_prepare_close", nil)
				case "zero_row_control":
					step("02_control", fixture.Select("SELECT 1 AS v WHERE false", []string{"v"}, []string{"INT4"}, nil))
					setting("03_after_control", nil)
				case "zero_row_function":
					step("02_zero_row", fixture.Select("SELECT public.isn_weak() AS v WHERE false", []string{"v"}, []string{"BOOL"}, nil))
					setting("03_after_zero_row", "off")
				case "constant_input":
					step("02_typed_constant", fixture.Ean13Select(current, "SELECT '2222222222222'::public.ean13 AS v", []string{"v"}, []string{"EAN13"}, [][]any{{"222-222222222-2"}}, nil))
					setting("03_after_input", "off")
				case "drop_recreate":
					run(isnValidationBool("SELECT public.isn_weak(true) AS v", true))
					show("02_before_drop", "on")
					step("03_drop", fixture.Exec("DROP EXTENSION isn", 0))
					show("04_after_drop", "on")
					step("05_assign_without_extension", fixture.Exec("SET isn.weak='off'", 0))
					show("06_after_assignment", "off")
					step("07_recreate", fixture.Exec("CREATE EXTENSION isn", 0))
					step("08_same_owner_getter", isnValidationBool("SELECT public.isn_weak() AS v", false))
				case "read_only":
					step("02_begin_read_only", fixture.Exec("BEGIN READ ONLY", 0))
					step("03_setter", isnValidationBool("SELECT public.isn_weak(true) AS v", true))
					step("04_rollback", fixture.Exec("ROLLBACK", 0))
					step("05_restored", isnValidationBool("SELECT public.isn_weak() AS v", false))
				}
				noticeFixture.NoticesWant(t, "", "")
			})
		}
	})
}
