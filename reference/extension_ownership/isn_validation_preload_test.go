package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIsnValidationPreloadSavepoints(t *testing.T) {
	isnValidationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, installer *sql.Conn) {
		isnConversionRun(t, p, b, installer)(fixture.Exec("CREATE EXTENSION isn", 0))
		installerPID := fixture.LockPID(t, p.Ctx, installer)
		for _, tc := range []struct {
			name, placeholder string
			masked, invalid, loaded, committed bool
		}{
			{name: "absent"},
			{name: "valid", placeholder: "true", loaded: true, committed: true},
			{name: "invalid", placeholder: "bogus", invalid: true},
			{name: "masked_valid", placeholder: "true", masked: true, committed: true},
			{name: "masked_invalid", placeholder: "bogus", masked: true, invalid: true},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared_%t", tc.name, prepared), func(t *testing.T) {
					var notices []fixture.Diagnostic
					db, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					defer c.Close()
					defer isnValidationCleanup(t, c)
					current := fixture.Backend{DB: db, Recorder: recorder, PG: b.PG}
					run := isnConversionRun(t, p, current, c)
					noticeFixture := fixture.ControlsReferenceFixture{Notices: &notices}
					pid := fixture.LockPID(t, p.Ctx, c)
					if pid == installerPID {
						t.Fatal("pre-load savepoint reused installation backend")
					}
					const getterSQL = "SELECT public.isn_weak() AS v"
					var getter fixture.Executor = c
					if prepared {
						held := fixture.IdentityHeld(t, p.Ctx, c, getterSQL)
						defer held.Statement.Close()
						getter = held
					}
					// Even an already prepared getter must not recognize the module.
					run(fixture.Select("SELECT current_setting('isn.weak',true) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
					if !tc.masked && tc.placeholder != "" {
						run(fixture.Exec("SET isn.weak="+isnConversionLiteral(tc.placeholder), 0))
					}
					run(fixture.Exec("BEGIN", 0))
					if tc.masked {
						run(fixture.Exec("SET isn.weak="+isnConversionLiteral(tc.placeholder), 0))
						run(fixture.Exec("SET LOCAL isn.weak='false'", 0))
					}
					run(fixture.Exec("SAVEPOINT iv_before", 0))
					noticeFixture.NoticesWant(t, "", "")
					fixture.RunStep(t, p.Ctx, getter, recorder, isnValidationBool(getterSQL, tc.loaded))
					// Pinned PostgreSQL 18.6 emits one warning for either an
					// invalid current placeholder or an invalid masked SET tail.
					if tc.invalid {
						noticeFixture.NoticesWant(t, "22023", `parameter "isn.weak" requires a Boolean value`)
					} else {
						noticeFixture.NoticesWant(t, "", "")
					}
					isnValidationSetting(run, tc.loaded)
					change := isnValidationBool("SELECT public.isn_weak($1::boolean) AS v", !tc.loaded, !tc.loaded)
					if prepared {
						change = fixture.Prepared(change)
					}
					run(change)
					isnValidationSetting(run, !tc.loaded)
					run(fixture.Exec("ROLLBACK TO iv_before", 0))
					// SHOW precedes the getter so recognition cannot be repaired by
					// the assertion that is intended to observe its persistence.
					isnValidationSetting(run, tc.loaded)
					run(fixture.Exec("SAVEPOINT iv_boolean", 0))
					run(isnConversionRuntimeError("SET isn.weak='bogus'", isnConversionDiagnostic("22023", `parameter "isn.weak" requires a Boolean value`)))
					run(fixture.Exec("ROLLBACK TO iv_boolean", 0))
					run(fixture.Exec("RELEASE iv_boolean", 0))
					isnValidationSetting(run, tc.loaded)
					fixture.RunStep(t, p.Ctx, getter, recorder, isnValidationBool(getterSQL, tc.loaded))
					noticeFixture.NoticesWant(t, "", "")
					run(fixture.Exec("SAVEPOINT iv_probe", 0))
					probe := isnValidationValueStep(current, 0, "$1::text::public.ean13", isnValidationBad[0], true, false)
					if !tc.loaded {
						probe = isnValidationQueryError(probe.Query, isnValidationChecksum(0, isnValidationBad[0]), isnValidationBad[0])
					}
					if prepared {
						probe = fixture.Prepared(probe)
					}
					run(probe)
					run(fixture.Exec("ROLLBACK TO iv_probe", 0))
					run(fixture.Exec("RELEASE iv_probe", 0))
					isnValidationSetting(run, tc.loaded)
					run(fixture.Exec("RELEASE iv_before", 0))
					run(fixture.Exec("COMMIT", 0))
					isnValidationSetting(run, tc.committed)
					fixture.RunStep(t, p.Ctx, getter, recorder, isnValidationBool(getterSQL, tc.committed))
					run(fixture.Exec("RESET isn.weak", 0))
					isnValidationSetting(run, false)
					fixture.RunStep(t, p.Ctx, getter, recorder, isnValidationBool(getterSQL, false))
					if fixture.LockPID(t, p.Ctx, c) != pid {
						t.Fatal("restoration changed physical owner")
					}
					noticeFixture.NoticesWant(t, "", "")
				})
			}
		}
	})
}

func TestReferenceIsnValidationFirstLoadException(t *testing.T) {
	isnValidationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, installer *sql.Conn) {
		runInstaller := isnConversionRun(t, p, b, installer)
		runInstaller(fixture.Exec("CREATE EXTENSION isn", 0))
		runInstaller(fixture.Exec("CREATE FUNCTION public.iv_first_load_exception() RETURNS boolean LANGUAGE plpgsql AS $$BEGIN IF current_setting('isn.weak',true) IS NOT NULL THEN RAISE EXCEPTION 'module loaded before entry'; END IF; BEGIN EXECUTE 'SELECT public.isn_weak(true)'; RAISE EXCEPTION 'undo'; EXCEPTION WHEN raise_exception THEN PERFORM 1; END; RETURN public.isn_weak(); END$$", 0))
		installerPID := fixture.LockPID(t, p.Ctx, installer)
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
				var notices []fixture.Diagnostic
				db, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				defer c.Close()
				defer isnValidationCleanup(t, c)
				current := fixture.Backend{DB: db, Recorder: recorder, PG: b.PG}
				run := isnConversionRun(t, p, current, c)
				pid := fixture.LockPID(t, p.Ctx, c)
				if pid == installerPID {
					t.Fatal("first-load exception reused installation backend")
				}
				const query = "SELECT public.iv_first_load_exception() AS v"
				var executor fixture.Executor = c
				if prepared {
					held := fixture.IdentityHeld(t, p.Ctx, c, query)
					defer held.Statement.Close()
					executor = held
				}
				run(fixture.Select("SELECT current_setting('isn.weak',true) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
				fixture.RunStep(t, p.Ctx, executor, recorder, isnValidationBool(query, false))
				isnValidationSetting(run, false)
				run(isnValidationBool("SELECT public.isn_weak() AS v", false))
				bad := isnValidationQueryError("SELECT is_valid($1::text::public.ean13) AS v", isnValidationChecksum(0, isnValidationBad[0]), isnValidationBad[0])
				isnConversionRoutes(t, p, current, c, bad)
				run(isnValidationBool("SELECT is_valid($1::text::public.ean13) AS v", true, "2222222222222"))
				run(fixture.Exec("RESET isn.weak", 0))
				isnValidationSetting(run, false)
				run(isnValidationBool("SELECT public.isn_weak() AS v", false))
				if fixture.LockPID(t, p.Ctx, c) != pid {
					t.Fatal("first-load exception changed physical owner")
				}
				fixture.ControlsReferenceFixture{Notices: &notices}.NoticesWant(t, "", "")
			})
		}
	})
}
