package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceQueryWindowRangeTypes(t *testing.T) {
	for _, tc := range []struct {
		name, typ, values, offset, frame string
		counts                           []int64
	}{
		{"int2", "SMALLINT", "1,2,4", "1", "", []int64{1, 2, 1}},
		{"int4", "INTEGER", "1,2,4", "1", "", []int64{1, 2, 1}},
		{"int8", "BIGINT", "1,2,4", "1", "", []int64{1, 2, 1}},
		{"wide", "SMALLINT", "1,2,4", "40000::BIGINT", "", []int64{1, 2, 3}},
		{"max", "BIGINT", "9223372036854775806,9223372036854775807", "", "BETWEEN CURRENT ROW AND 4 FOLLOWING", []int64{2, 1}},
		{"min", "BIGINT", "-9223372036854775808,-9223372036854775807", "4", "", []int64{1, 2}},
		{"float4", "REAL", "0,1,2", "0.99999999::DOUBLE PRECISION", "", []int64{1, 1, 1}},
		{"float8", "DOUBLE PRECISION", "0,1,2", "1", "", []int64{1, 2, 2}},
		{"numeric", "NUMERIC", "9007199254740992,9007199254740993,9007199254740995", "1", "", []int64{1, 2, 1}},
		{"typmod", "NUMERIC(2,0)", "98,99", "", "BETWEEN CURRENT ROW AND 10 FOLLOWING", []int64{2, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
				queryWindowRun(t, p, b, referenceExec("CREATE TABLE carrier(id INT,k "+tc.typ+")", 0))
				values := splitRangeValues(tc.values)
				for i, v := range values {
					queryWindowRun(t, p, b, referenceExec(fmt.Sprintf("INSERT INTO carrier VALUES(%d,%s)", i+1, v), 1))
				}
				frame := tc.frame
				if frame == "" {
					frame = tc.offset + " PRECEDING"
				}
				queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k RANGE "+frame+") AS n FROM carrier ORDER BY id", tc.counts...))
			})
		})
	}
	for _, base := range []string{"INTEGER", "NUMERIC", "TIMESTAMP"} {
		t.Run("domain_"+base, func(t *testing.T) {
			queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
				offset, values := "1", "(1,1),(2,2),(3,4)"
				if base == "TIMESTAMP" {
					offset = "INTERVAL '1 day'"
					values = "(1,'2025-01-01'),(2,'2025-01-02'),(3,'2025-01-04')"
				}
				queryWindowRun(t, p, b, referenceExec("CREATE DOMAIN range_domain AS "+base, 0))
				queryWindowRun(t, p, b, referenceExec("CREATE TABLE carrier(id INT,k range_domain)", 0))
				queryWindowRun(t, p, b, referenceExec("INSERT INTO carrier VALUES"+values, 3))
				queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k RANGE "+offset+" PRECEDING) AS n FROM carrier ORDER BY id", 1, 2, 1))
			})
		})
	}
}

func splitRangeValues(s string) []string {
	var result []string
	start := 0
	for i, c := range s {
		if c == ',' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	return append(result, s[start:])
}

func TestReferenceQueryWindowRangeTemporal(t *testing.T) {
	for _, tc := range []struct {
		name, typ, values, offset string
		counts                    []int64
	}{
		{"date", "DATE", "(1,'2025-02-28'),(2,'2025-03-30'),(3,'2025-03-31')", "1 month", []int64{1, 2, 3}},
		{"timestamp", "TIMESTAMP", "(1,'2025-02-28'),(2,'2025-03-30'),(3,'2025-03-31')", "1 month", []int64{1, 2, 3}},
		{"time", "TIME", "(1,'00:30'),(2,'23:30')", "1 hour", []int64{1, 1}},
		{"timetz", "TIMETZ", "(1,'00:30+00'),(2,'23:30+00')", "1 hour", []int64{1, 1}},
		{"time_negative_day", "TIME", "(1,'00:30'),(2,'23:30')", "-1 day", []int64{1, 1}},
		{"timetz_negative_day", "TIMETZ", "(1,'00:30+00'),(2,'23:30+00')", "-1 day", []int64{1, 1}},
		{"timetz_tie", "TIMETZ", "(1,'12:00+00'),(2,'13:00+01')", "0 seconds", []int64{1, 1}},
		{"interval", "INTERVAL", "(1,'0 days'),(2,'1 day'),(3,'2 days')", "1 day", []int64{1, 2, 2}},
		{"interval_components", "INTERVAL", "(1,'0 days'),(2,'1 day'),(3,'2 days')", "1 month -30 days", []int64{1, 1, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
				queryWindowRun(t, p, b, referenceExec("CREATE TABLE carrier(id INT,k "+tc.typ+")", 0))
				queryWindowRun(t, p, b, referenceExec("INSERT INTO carrier VALUES"+tc.values, int64(len(tc.counts))))
				queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k RANGE INTERVAL '"+tc.offset+"' PRECEDING) AS n FROM carrier ORDER BY id", tc.counts...))
			})
		})
	}
	for _, tc := range []struct {
		offset string
		counts []int64
	}{
		{"1 day", []int64{1, 2, 2}}, {"24 hours", []int64{1, 2, 3}}, {"23 hours", []int64{1, 2, 2}}, {"22 hours", []int64{1, 2, 1}},
	} {
		t.Run("dst_"+tc.offset, func(t *testing.T) {
			queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
				queryWindowRun(t, p, b, referenceExec("SET TIME ZONE 'America/New_York'", 0))
				queryWindowRun(t, p, b, referenceExec("CREATE TABLE carrier(id INT,k TIMESTAMPTZ)", 0))
				queryWindowRun(t, p, b, referenceExec("INSERT INTO carrier VALUES(1,'2025-03-08 11:30-05'),(2,'2025-03-08 12:00-05'),(3,'2025-03-09 12:00-04')", 3))
				queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k RANGE INTERVAL '"+tc.offset+"' PRECEDING) AS n FROM carrier ORDER BY id", tc.counts...))
			})
		})
	}
}

func TestReferenceQueryWindowRangeSpecialValues(t *testing.T) {
	for _, typ := range []string{"REAL", "DOUBLE PRECISION", "NUMERIC"} {
		t.Run(typ, func(t *testing.T) {
			queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
				queryWindowRun(t, p, b, referenceExec("CREATE TABLE carrier(id INT,k "+typ+")", 0))
				queryWindowRun(t, p, b, referenceExec("INSERT INTO carrier VALUES(1,'-Infinity'),(2,0),(3,'Infinity'),(4,'NaN'),(5,0)", 5))
				for _, tc := range []struct {
					offset string
					counts []int64
				}{
					{"0", []int64{1, 2, 1, 1, 2}}, {"'Infinity'", []int64{1, 3, 4, 1, 3}},
				} {
					queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k RANGE "+tc.offset+" PRECEDING) AS n FROM carrier ORDER BY id", tc.counts...))
				}
				for _, offset := range []string{"'NaN'", "'-Infinity'"} {
					queryWindowRangeRuntime(t, p, b, "SELECT count(*) OVER(ORDER BY k RANGE "+offset+" PRECEDING) FROM carrier", "22013", "invalid preceding or following size in window function")
				}
			})
		})
	}
}
