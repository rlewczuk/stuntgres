package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func (s *physicalCase) compatibleSentinel(table string, prepared bool) {
	step := fixture.Select("SELECT v FROM "+table+" ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}})
	step.Prepared = prepared
	s.run(step)
}

func TestReferenceCompatibleSettingsSimulationSmoke(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE TABLE compatible_smoke(v integer)", 0))
		s.run(fixture.Exec("INSERT INTO compatible_smoke VALUES(1),(2),(3)", 3))
		for _, prepared := range []bool{false, true} {
			s.compatibleSentinel("compatible_smoke", prepared)
			for _, index := range []int{1, 2, 3, 4, 7, 8} {
				tc := compatibleSettings[index]
				value := tc.alternate
				if tc.name == "backslash_quote" {
					value = "off"
				}
				step := fixture.Exec("SET "+tc.name+"="+physicalLiteral(value), 0)
				step.Prepared = prepared
				s.run(step)
				s.compatibleReads(tc, value, prepared)
				s.compatibleMetadata(tc, value, "session", prepared)
				s.compatibleSentinel("compatible_smoke", prepared)
			}
			s.compatibleReads(compatibleSettings[0], "on", prepared)
			for _, value := range []string{"off", "broken"} {
				s.run(physicalError("SET allow_alter_system="+physicalLiteral(value), compatibleSighup(), prepared, false))
				s.compatibleReads(compatibleSettings[0], "on", prepared)
				s.compatibleSentinel("compatible_smoke", prepared)
			}
			step := fixture.Exec("RESET ALL", 0)
			step.Prepared = prepared
			s.run(step)
			for _, index := range []int{0, 1, 2, 3, 4, 7, 8} {
				tc := compatibleSettings[index]
				s.compatibleReads(tc, tc.initial, prepared)
				s.compatibleMetadata(tc, tc.initial, "default", prepared)
			}
			s.compatibleSentinel("compatible_smoke", prepared)
		}
	})
}
