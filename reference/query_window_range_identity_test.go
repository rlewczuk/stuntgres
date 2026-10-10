package reference_test

import (
	"strconv"
	"testing"
)

func TestReferenceQueryWindowRangeNamedOffsetIdentity(t *testing.T) {
	for _, distinct := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run("distinct="+strconv.FormatBool(distinct)+"/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
				queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
					for _, s := range []referenceStep{
						referenceExec("CREATE SEQUENCE range_identity START WITH 1", 0),
						referenceExec("CREATE TABLE range_identity_t(id INT,k INT)", 0),
						referenceExec("INSERT INTO range_identity_t VALUES(1,1),(2,2),(3,3)", 3),
					} {
						queryWindowRun(t, p, b, s)
					}
					second := "w"
					definitions := "w AS(ORDER BY k RANGE nextval('range_identity') PRECEDING)"
					finalUpper, lastValue := int64(2), int64(1)
					if distinct {
						second = "v"
						definitions += ",v AS(ORDER BY k RANGE nextval('range_identity') PRECEDING)"
						finalUpper, lastValue = 3, 2
					}
					// The two named frames need not have a specified execution order. Assert
					// their independent final membership pair, not which name gets offset1.
					q := "SELECT id,LEAST(a,b) AS lo,GREATEST(a,b) AS hi FROM (SELECT id,count(*) OVER w AS a,count(*) OVER " + second + " AS b FROM range_identity_t WINDOW " + definitions + ") AS frames ORDER BY id"
					rows := [][]any{{int64(1), int64(1), int64(1)}, {int64(2), int64(2), int64(2)}, {int64(3), int64(2), finalUpper}}
					s := referenceSelect(q, []string{"id", "lo", "hi"}, []string{"INT4", "INT8", "INT8"}, rows)
					s.prepared = prepared
					queryWindowRun(t, p, b, s)
					queryWindowRoutes(t, p, b, referenceSelect("SELECT last_value FROM range_identity", []string{"last_value"}, []string{"INT8"}, [][]any{{lastValue}}))
				})
			})
		}
	}
}

func TestReferenceQueryWindowRangeNamedOffsetIdentityPersistence(t *testing.T) {
	queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
		for _, s := range []referenceStep{
			referenceExec("CREATE SEQUENCE range_identity START WITH 1", 0),
			referenceExec("CREATE TABLE range_identity_t(id INT,k INT)", 0),
			referenceExec("INSERT INTO range_identity_t VALUES(1,1),(2,2),(3,3)", 3),
			referenceExec("CREATE VIEW range_identity_view AS SELECT id,LEAST(count(*) OVER w,count(*) OVER v) AS lo,GREATEST(count(*) OVER w,count(*) OVER v) AS hi FROM range_identity_t WINDOW w AS(ORDER BY k RANGE (1+nextval('range_identity')%2) PRECEDING),v AS(ORDER BY k RANGE (1+nextval('range_identity')%2) PRECEDING)", 0),
		} {
			queryWindowRun(t, p, b, s)
		}
		// Cycling offsets give the pair{1,2} on every execution, independent of
		// which named frame runs first or how often export/replay/controls read it.
		expected := referenceSelect("SELECT * FROM range_identity_view ORDER BY id", []string{"id", "lo", "hi"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(2), int64(2), int64(2)}, {int64(3), int64(2), int64(3)}})
		queryWindowRoutes(t, p, b, expected)
		queryWindowRoutes(t, p, b, referenceSelect("SELECT last_value FROM range_identity", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(4)}}))
		steps := queryWindowReviewReplay(t, p, b, "range_identity_view", expected)
		if !b.pg {
			queryWindowRangeControls(t, p, steps)
		}
	})
}
