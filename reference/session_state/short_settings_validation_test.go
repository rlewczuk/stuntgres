package reference_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceShortSettingsInventory(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		if len(shortSettings) != 25 { s.t.Fatal("short inventory cardinality") }
		for _, prepared := range []bool{false, true} {
			for _, d := range shortSettings {
				show, raw, source, _ := shortInitial(d, s.pg)
				s.shortRead(d.name, show, raw, source, prepared)
				s.shortMetadata(d, prepared)
			}
			s.shortVisible(true, prepared)
		}
	})
}

func TestReferenceShortSettingsValidation(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		s.shortCreateSentinel()
		for _, prepared := range []bool{false, true} {
			for _, d := range shortSettings {
				if !shortMutable(d) || d.kind != "bool" { continue }
				// Keep peers at captured defaults while validating each flag.
				s.developerExec("RESET ALL", prepared)
				for _, tc := range []struct{ input, want string }{{"off", "off"}, {"TRUE", "on"}, {"f", "off"}, {"yes", "on"}, {"NO", "off"}, {"1", "on"}, {"0", "off"}, {"on", "on"}} {
					s.developerExec("SET "+d.name+"="+physicalLiteral(tc.input), prepared)
					s.shortRead(d.name, tc.want, tc.want, "session", prepared)
				}
				for _, invalid := range []string{"o", "bogus"} {
					diagnostic := physicalDiagnostic("22023", fmt.Sprintf("parameter %q requires a Boolean value", d.name))
					s.run(physicalError("SET "+d.name+"="+physicalLiteral(invalid), diagnostic, prepared, false))
					s.run(physicalError("SELECT set_config($1,$2,false)", diagnostic, prepared, true, d.name, invalid))
					s.shortRead(d.name, "on", "on", "session", prepared)
					s.shortSentinel()
				}
				s.run(physicalError("SET "+d.name+" TO on,off", physicalDiagnostic("22023", "SET "+d.name+" takes only one argument"), prepared, false))
				for _, value := range []any{"true", nil} {
					want := "on"; source := "session"
					if value == nil { want, source = d.boot, "default" }
					step := fixture.Select("SELECT set_config($1,$2,$3)", []string{"set_config"}, []string{"TEXT"}, [][]any{{want}}, d.name, value, false)
					step.Prepared = prepared; s.run(step)
					s.shortRead(d.name, want, want, source, prepared)
				}
				for _, query := range []string{"SET "+d.name+"=DEFAULT", "RESET "+d.name} {
					s.developerExec("SET "+d.name+"=off", prepared)
					s.developerExec(query, prepared)
					s.shortRead(d.name, d.show, d.boot, "default", prepared)
				}
				s.shortSentinel()
			}
			s.developerExec("RESET ALL", prepared)
			s.developerExec("SET DateStyle TO ISO, MDY", prepared)
			s.shortRead("DateStyle", "ISO, MDY", "ISO, MDY", "session", prepared)
			for _, tc := range []struct{ name, input, show, raw string }{
				{"work_mem", "1MB", "1MB", "1024"}, {"log_min_messages", "debug", "debug2", "debug2"},
				{"DateStyle", "ISO, MDY", "ISO, MDY", "ISO, MDY"}, {"DateStyle", "euro", "ISO, DMY", "ISO, DMY"},
				{"DateStyle", "SQL", "SQL, DMY", "SQL, DMY"}, {"DateStyle", "US", "SQL, MDY", "SQL, MDY"}, {"DateStyle", "euro", "SQL, DMY", "SQL, DMY"},
			} {
				s.developerExec("SET "+tc.name+"="+physicalLiteral(tc.input), prepared)
				s.shortRead(tc.name, tc.show, tc.raw, "session", prepared)
				s.shortSentinel()
			}
			for _, tc := range []struct{ name, input, message, hint, detail, show, raw string }{
				{"work_mem", "1kB", `1 kB is outside the valid range for parameter "work_mem" (64 kB .. 2147483647 kB)`, "", "", "1MB", "1024"},
				{"work_mem", "1 mb", `invalid value for parameter "work_mem": "1 mb"`, `Valid units for this parameter are "B", "kB", "MB", "GB", and "TB".`, "", "1MB", "1024"},
				{"log_min_messages", "bogus", `invalid value for parameter "log_min_messages": "bogus"`, "Available values: debug5, debug4, debug3, debug2, debug1, info, notice, warning, error, log, fatal, panic.", "", "debug2", "debug2"},
				{"DateStyle", "broken", `invalid value for parameter "DateStyle": "broken"`, "", `Unrecognized key word: "broken".`, "SQL, DMY", "SQL, DMY"},
			} {
				d := physicalDiagnostic("22023", tc.message); d.Hint, d.Detail = tc.hint, tc.detail
				s.run(physicalError("SET "+tc.name+"="+physicalLiteral(tc.input), d, prepared, false))
				s.run(physicalError("SELECT set_config($1,$2,false)", d, prepared, true, tc.name, tc.input))
				s.shortRead(tc.name, tc.show, tc.raw, "session", prepared)
				s.shortSentinel()
			}
			s.developerExec("RESET ALL", prepared)
		}
	})
}

func TestReferenceShortSettingsContext(t *testing.T) {
	physicalCases(t, []string{"short_context_login"}, func(s *physicalCase) {
		s.shortCreateSentinel()
		s.run(fixture.Exec("CREATE ROLE short_context_login LOGIN PASSWORD 'role-core-fixture'", 0))
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, d := range shortSettings {
				if shortMutable(d) { continue }
				if _, err := s.conn.ExecContext(ctx, "REVOKE SET ON PARAMETER "+d.name+" FROM short_context_login"); err != nil { s.t.Error("parameter privilege cleanup", d.name, err) }
			}
		}()
		ordinary := s.login("short_context_login")
		for _, d := range shortSettings {
			if shortMutable(d) { continue }
			for phase, observer := range []*physicalCase{s, ordinary, ordinary} {
				if phase == 2 { s.run(fixture.Exec("GRANT SET ON PARAMETER "+d.name+" TO short_context_login", 0)) }
				for _, prepared := range []bool{false, true} {
					show, _, _, _ := shortInitial(d, s.pg)
					diagnostic := developerContext(developerSetting{name: d.name, context: d.context})
					for _, query := range []string{"SET "+d.name+"="+physicalLiteral(show), "SET "+d.name+"='bogus'", "SET "+d.name+"=DEFAULT", "RESET "+d.name} {
						observer.run(physicalError(query, diagnostic, prepared, false))
					}
					for _, value := range []any{"bogus", show, nil} { observer.run(physicalError("SELECT set_config($1,$2,false)", diagnostic, prepared, true, d.name, value)) }
					if d.name != "listen_addresses" && d.name != "unix_socket_directories" { observer.run(physicalError("SET "+d.name+" TO 1,2", physicalDiagnostic("22023", "SET "+d.name+" takes only one argument"), prepared, false)) }
					if d.name == "listen_addresses" || d.name == "unix_socket_directories" { observer.run(physicalError("SET "+d.name+" TO 'a','b'", diagnostic, prepared, false)) }
					observer.developerExec("RESET ALL", prepared)
					s.shortMetadata(d, prepared)
					s.shortSentinel()
				}
			}
		}
	})
}
