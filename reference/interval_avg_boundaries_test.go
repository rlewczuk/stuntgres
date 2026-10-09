package reference_test

import (
	"database/sql"
	"testing"
)

func intervalAvgRangeError(query string, prepared bool) referenceStep {
	s := referenceImplicitError(query, referenceDiagnostic{Code: "22008", Message: "interval out of range"})
	s.selectRows = true
	s.prepared = prepared
	s.errorStage = "execute"
	return s
}

func TestReferenceIntervalAvgBoundaries(t *testing.T) {
	intervalAvgScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
		run(referenceExec("SET IntervalStyle = 'postgres'", 0))
		run(referenceExec("CREATE TABLE t(v INTERVAL)", 0))
		vectors := []struct {
			name, values, want string
			fails              bool
		}{
			{"float_integer_precision", "('2501999:47:34.740993')", "2501999:47:34.740992", false},
			{"time_min", "('-9223372036854775808 microseconds')", "-2562047788:00:54.775808", false},
			{"month_max", "('2147483647 months')", "178956970 years 7 mons", false},
			{"month_min", "('-2147483648 months')", "-178956970 years -8 mons", false},
			{"day_max", "('2147483647 days')", "2147483647 days", false},
			{"day_min", "('-2147483648 days')", "-2147483648 days", false},
			{"time_float_range", "('9223372036854775807 microseconds')", "", true},
			{"month_positive_overflow", "('2147483647 months'),('1 month')", "", true},
			{"month_negative_overflow", "('-2147483648 months'),('-1 month')", "", true},
			{"day_positive_overflow", "('2147483647 days'),('1 day')", "", true},
			{"day_negative_overflow", "('-2147483648 days'),('-1 day')", "", true},
			{"time_positive_overflow", "('9223372036854775807 microseconds'),('1 microsecond')", "", true},
			{"time_negative_overflow", "('-9223372036854775808 microseconds'),('-1 microsecond')", "", true},
			{"reserved_positive_sum", "('2147483647 months 2147483647 days'),('9223372036854775807 microseconds')", "", true},
			{"reserved_negative_sum", "('-2147483648 months -2147483648 days'),('-9223372036854775808 microseconds')", "", true},
			{"positive_infinity", "('infinity'),('1 day'),(NULL)", "infinity", false},
			{"negative_infinity", "('-infinity'),('1 day'),(NULL)", "-infinity", false},
			{"repeated_infinity", "('infinity'),('infinity')", "infinity", false},
			{"opposite_infinities", "('infinity'),('-infinity')", "", true},
			{"infinity_does_not_skip_overflow", "('infinity'),('2147483647 months'),('1 month')", "", true},
		}
		var previous int64
		for _, v := range vectors {
			t.Run(v.name, func(t *testing.T) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
				run(referenceExec("DELETE FROM t", previous))
				previous = 0
				count := int64(countIntervalAvgValues(v.values))
				run(referenceExec("INSERT INTO t VALUES "+v.values, count))
				previous = count
				for _, prepared := range []bool{false, true} {
					if v.fails {
						run(intervalAvgRangeError("SELECT avg(v) FROM t", prepared))
					} else {
						s := referenceSelect("SELECT avg(v) FROM t", []string{"avg"}, []string{"INTERVAL"}, [][]any{{v.want}})
						s.prepared = prepared
						run(s)
					}
				}
			})
		}
		// Eager finalization must veto the entire grouped result and INSERT source.
		run(referenceExec("CREATE TABLE grouped(g INTEGER,v INTERVAL)", 0))
		run(referenceExec("INSERT INTO grouped VALUES (1,'1 day'),(2,'2147483647 months'),(2,'1 month')", 3))
		run(referenceExec("CREATE TABLE target(g INTEGER,v INTERVAL)", 0))
		run(referenceExec("INSERT INTO target VALUES (9,'9 days')", 1))
		for _, prepared := range []bool{false, true} {
			run(intervalAvgRangeError("SELECT g,avg(v) FROM grouped GROUP BY g ORDER BY g", prepared))
			s := intervalAvgRangeError("INSERT INTO target SELECT g,avg(v) FROM grouped GROUP BY g", prepared)
			s.selectRows = false
			run(s)
			run(referenceSelect("SELECT g,v FROM target ORDER BY g", []string{"g", "v"}, []string{"INT4", "INTERVAL"}, [][]any{{int64(9), "9 days"}}))
		}
	})
}
