package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryOuterJoinsFiltering(t *testing.T) {
	outerJoinsScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder) {
		matched := outerJoinsOnRows("LEFT")[:4]
		outerJoinsCases(t, p, conn, recorder,
			outerJoinsSelect("SELECT * FROM a LEFT JOIN b ON a.k=b.k AND b.r='x'", outerJoinsOnRows("LEFT")),
			outerJoinsSelect("SELECT * FROM a LEFT JOIN b ON a.k=b.k WHERE b.r='x'", matched),
			outerJoinsSelect("SELECT * FROM a LEFT JOIN b ON a.k=b.k AND b.r='never'", outerJoinsUnmatchedRows("LEFT")),
			outerJoinsSelect("SELECT * FROM a LEFT JOIN b ON a.k=b.k WHERE b.r='never'", nil),
			outerJoinsSelect("SELECT * FROM a LEFT JOIN b ON a.k=b.k WHERE b.k IS NULL", [][]any{{int64(2), "b", nil, nil}, {nil, "n", nil, nil}}),
			outerJoinsSelect("SELECT * FROM a RIGHT JOIN b ON a.k=b.k AND a.l='never'", outerJoinsUnmatchedRows("RIGHT")),
			outerJoinsSelect("SELECT * FROM a RIGHT JOIN b ON a.k=b.k WHERE a.l='never'", nil),
			outerJoinsSelect("SELECT * FROM a FULL JOIN b ON a.k=b.k WHERE a.k IS NULL", [][]any{{nil, "n", nil, nil}, {nil, nil, int64(3), "y"}, {nil, nil, nil, "m"}}),
			outerJoinsSelect("SELECT * FROM a FULL JOIN b ON a.k=b.k WHERE b.k IS NULL", [][]any{{int64(2), "b", nil, nil}, {nil, "n", nil, nil}, {nil, nil, nil, "m"}}),
			outerJoinsSelect("SELECT * FROM a LEFT JOIN b ON a.k=b.k AND NULL::boolean", outerJoinsUnmatchedRows("LEFT")))
	})
}

func TestReferenceQueryOuterJoinsConsumers(t *testing.T) {
	outerJoinsScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder) {
		for _, tc := range []struct {
			kind    string
			count   int64
			grouped [][]any
		}{
			{"LEFT", 6, [][]any{{int64(1), int64(4)}, {int64(2), int64(1)}, {nil, int64(1)}}},
			{"RIGHT", 6, [][]any{{int64(1), int64(4)}, {int64(3), int64(1)}, {nil, int64(1)}}},
			{"FULL", 8, [][]any{{int64(1), int64(4)}, {int64(2), int64(1)}, {int64(3), int64(1)}, {nil, int64(2)}}},
		} {
			outerJoinsCases(t, p, conn, recorder,
				referenceSelect("SELECT count(*) FROM a "+tc.kind+" JOIN b ON a.k=b.k", []string{"count"}, []string{"INT8"}, [][]any{{tc.count}}),
				referenceSelect("SELECT k,count(*) FROM a "+tc.kind+" JOIN b USING(k) GROUP BY k", []string{"k", "count"}, []string{"INT8", "INT8"}, tc.grouped))
		}
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE TABLE destination(k BIGINT,l TEXT,r TEXT)", 0))
		for _, prepared := range []bool{false, true} {
			s := referenceExec("INSERT INTO destination SELECT * FROM a FULL JOIN b USING(k)", 8)
			s.prepared = prepared
			referenceRunStep(t, p.ctx, conn, recorder, s)
			outerJoinsCases(t, p, conn, recorder, referenceSelect("SELECT * FROM destination", []string{"k", "l", "r"}, []string{"INT8", "TEXT", "TEXT"}, outerJoinsMergedRows("FULL")))
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("DELETE FROM destination", 8))
		}
		outerJoinsCases(t, p, conn, recorder, referenceSelect("SELECT CASE WHEN b.k IS NULL THEN 'missing' ELSE a.l END AS label,CAST(a.k AS BIGINT) AS key FROM a LEFT JOIN b ON a.k=b.k ORDER BY a.k NULLS LAST LIMIT 5", []string{"label", "key"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {"a", int64(1)}, {"a", int64(1)}, {"a", int64(1)}, {"missing", int64(2)}}))
	})
}

func TestReferenceQueryOuterJoinsDemand(t *testing.T) {
	outerJoinsScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder) {
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			outerJoinsCases(t, p, conn, recorder,
				referenceSelect("SELECT 1/(a.k-a.k) AS sentinel FROM a "+kind+" JOIN b ON a.k=b.k LIMIT 0", []string{"sentinel"}, []string{"INT4"}, nil),
				referenceSelect("SELECT 1/(a.k-2) AS sentinel FROM a "+kind+" JOIN b ON a.k=b.k LIMIT 1", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(-1)}}))
		}
	})
}
