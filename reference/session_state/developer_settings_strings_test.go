package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceDeveloperSettingsStrings(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		for _, prepared := range []bool{false, true} {
			for _, tc := range developerSettings {
				if tc.name != "backtrace_functions" && tc.name != "wal_consistency_checking" && tc.name != "debug_io_direct" {
					continue
				}
				if tc.name == "debug_io_direct" {
					for _, value := range []string{"data, wal", "bogus", "wal,"} {
						s.run(physicalError("SET "+tc.name+"="+physicalLiteral(value), developerContext(tc), prepared, false))
					}
					continue
				}
				accepted := []string{"", "foo,, 9bad,", " \t\n", "foo,bar", "_foo,123"}
				if tc.name == "wal_consistency_checking" {
					accepted = []string{"", "heap, btree", "HEAP,BTREE", `"HeAp", BTREE`, "heap,heap,all", "all, heap", "heap,heap2,btree,hash,gin,gist,sequence,spgist,brin,generic"}
				}
				retained := ""
				for _, value := range accepted {
					s.developerExec("SET "+tc.name+"="+physicalLiteral(value), prepared)
					s.developerRead(tc, value, prepared)
					s.developerMetadata(tc, value, "session", prepared)
					s.developerConfig(tc, value, value, prepared)
					retained = value
				}
				rejected := []struct{ input, detail string }{
					{"foo-bar", "Invalid character."}, {"é", "Invalid character."},
					{"foo\rbar", "Invalid character."}, {"foo\fbar", "Invalid character."},
					{`"foo"`, "Invalid character."},
				}
				if tc.name == "wal_consistency_checking" {
					rejected = []struct{ input, detail string }{
						{"bogus", `Unrecognized key word: "bogus".`},
						{`"BOGUS"`, `Unrecognized key word: "BOGUS".`},
						{"HEAP,", "List syntax is invalid."},
						{"heap btree", "List syntax is invalid."},
						{"xlog", `Unrecognized key word: "xlog".`},
					}
				}
				for _, value := range rejected {
					d := physicalDiagnostic("22023", fmt.Sprintf(`invalid value for parameter "%s": "%s"`, tc.name, value.input))
					d.Detail = value.detail
					s.run(physicalError("SET "+tc.name+"="+physicalLiteral(value.input), d, prepared, false))
					s.developerRead(tc, retained, prepared)
					s.run(physicalError("SELECT set_config($1,$2,$3)", d, prepared, true, tc.name, value.input, false))
					s.developerMetadata(tc, retained, "session", prepared)
				}
				if tc.name == "backtrace_functions" {
					s.run(physicalError("SET backtrace_functions=foo,bar", physicalDiagnostic("22023", "SET backtrace_functions takes only one argument"), prepared, false))
				} else {
					s.developerExec("SET wal_consistency_checking=heap,btree", prepared)
					s.developerRead(tc, "heap, btree", prepared)
				}
				s.developerExec("RESET "+tc.name, prepared)
			}
		}
	})
}
