package reference_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceLockSettingsValidation(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE TABLE lock_settings_sentinel(v integer CHECK(v=7))", 0))
		s.run(fixture.Exec("INSERT INTO lock_settings_sentinel VALUES(7)", 1))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ input, show, raw string }{{"1", "1ms", "1"}, {"1000ms", "1s", "1000"}, {"1.5s", "1500ms", "1500"}, {"1min", "1min", "60000"}, {"0x3e8", "1s", "1000"}, {"2147483647", "2147483647ms", "2147483647"}, {"1500us", "2ms", "2"}} {
				step := fixture.Exec("SET deadlock_timeout="+physicalLiteral(tc.input), 0)
				step.Prepared = prepared
				s.run(step)
				s.lockReads(prepared, tc.show)
				s.lockInventory(prepared, tc.raw, "session")
				step = fixture.Select("SELECT set_config('deadlock_timeout',$1,false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{tc.show}}, tc.input)
				step.Prepared = prepared
				s.run(step)
				s.lockInventory(prepared, tc.raw, "session")
			}
			s.run(fixture.Exec("SET deadlock_timeout='2s'", 0))
			s.run(physicalError("SET deadlock_timeout='1','2'", physicalDiagnostic("22023", "SET deadlock_timeout takes only one argument"), prepared, false))
			for _, tc := range []struct{ input, message, hint string }{
				{"0", `0 ms is outside the valid range for parameter "deadlock_timeout" (1 ms .. 2147483647 ms)`, ""},
				{"-1", `-1 ms is outside the valid range for parameter "deadlock_timeout" (1 ms .. 2147483647 ms)`, ""},
				{"500us", `0 ms is outside the valid range for parameter "deadlock_timeout" (1 ms .. 2147483647 ms)`, ""},
				{"broken", `invalid value for parameter "deadlock_timeout": "broken"`, ""},
				{"1MS", `invalid value for parameter "deadlock_timeout": "1MS"`, `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`},
				{"2147483648", `invalid value for parameter "deadlock_timeout": "2147483648"`, "Value exceeds integer range."},
			} {
				d := physicalDiagnostic("22023", tc.message)
				d.Hint = tc.hint
				s.run(physicalError("SET deadlock_timeout="+physicalLiteral(tc.input), d, prepared, false))
				s.lockInventory(prepared, "2000", "session")
				s.lockReads(prepared, "2s")
				s.lockSentinel()
				s.run(physicalError("SELECT set_config('deadlock_timeout',$1,false)", d, prepared, true, tc.input))
				s.lockInventory(prepared, "2000", "session")
				s.lockReads(prepared, "2s")
				s.lockSentinel()
			}
			for _, query := range []string{"SET deadlock_timeout=DEFAULT", "RESET deadlock_timeout", "SELECT set_config('deadlock_timeout',NULL,false)"} {
				s.run(fixture.Exec("SET deadlock_timeout='3s'", 0))
				if query[:6] == "SELECT" {
					step := fixture.Select(query, []string{"set_config"}, []string{"TEXT"}, [][]any{{"1s"}})
					step.Prepared = prepared
					s.run(step)
				} else { step := fixture.Exec(query, 0); step.Prepared = prepared; s.run(step) }
				s.lockInventory(prepared, "1000", "default")
			}
		}
	})
}

func TestReferenceLockSettingsContexts(t *testing.T) {
	for _, d := range lockSettings[1:] {
		t.Run(d.name, func(t *testing.T) {
			physicalCases(t, []string{"lock_context_login"}, func(s *physicalCase) {
				s.run(fixture.Exec("CREATE ROLE lock_context_login LOGIN PASSWORD 'role-core-fixture'", 0))
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if _, err := s.conn.ExecContext(ctx, "REVOKE SET ON PARAMETER "+d.name+" FROM lock_context_login"); err != nil {
						s.t.Error("parameter privilege cleanup", d.name, err)
					}
				}()
				s.run(fixture.Exec("CREATE TABLE lock_settings_sentinel(v integer)", 0))
				s.run(fixture.Exec("INSERT INTO lock_settings_sentinel VALUES(7)", 1))
				ordinary := s.login("lock_context_login")
				for phase, actor := range []*physicalCase{s, ordinary, ordinary} {
					if phase == 2 { s.run(fixture.Exec("GRANT SET ON PARAMETER "+d.name+" TO lock_context_login", 0)) }
					for _, prepared := range []bool{false, true} {
						for _, query := range []string{"SET "+d.name+"=65", "SET "+d.name+"="+d.raw, "SET "+d.name+"='broken'", "SET "+d.name+"=DEFAULT", "RESET "+d.name} {
							actor.run(physicalError(query, lockContext(d.name, d.context), prepared, false))
						}
						actor.run(physicalError("SET "+d.name+"='1','2'", physicalDiagnostic("22023", fmt.Sprintf("SET %s takes only one argument", d.name)), prepared, false))
						for _, local := range []string{"false", "true", "NULL"} {
							for _, value := range []string{"'65'", "'broken'", "NULL", physicalLiteral(d.raw)} {
								actor.run(physicalError("SELECT set_config("+physicalLiteral(d.name)+","+value+","+local+")", lockContext(d.name, d.context), prepared, true))
							}
						}
						for _, value := range []string{d.raw, "'broken'", "DEFAULT"} {
							actor.run(fixture.Exec("BEGIN", 0))
							actor.run(fixture.Exec("SAVEPOINT lock_context_save", 0))
							actor.run(physicalError("SET LOCAL "+d.name+"="+value, lockContext(d.name, d.context), prepared, false))
							actor.run(fixture.Exec("ROLLBACK TO lock_context_save", 0))
							actor.lockReads(prepared, "1s")
							actor.run(fixture.Exec("ROLLBACK", 0))
						}
						actor.run(fixture.Exec("RESET ALL", 0))
						actor.lockInventory(prepared, "1000", "default")
					}
				}
				s.lockSentinel()
			})
		})
	}
}
