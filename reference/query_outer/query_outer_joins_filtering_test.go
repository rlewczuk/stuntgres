package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOuterJoinsFiltering(t *testing.T) {
	fixture.OuterJoinsScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder) {
		matched := fixture.OuterJoinsOnRows("LEFT")[:4]
		fixture.OuterJoinsCases(t, p, conn, recorder,
			fixture.OuterJoinsSelect("SELECT * FROM a LEFT JOIN b ON a.k=b.k AND b.r='x'", fixture.OuterJoinsOnRows("LEFT")),
			fixture.OuterJoinsSelect("SELECT * FROM a LEFT JOIN b ON a.k=b.k WHERE b.r='x'", matched),
			fixture.OuterJoinsSelect("SELECT * FROM a LEFT JOIN b ON a.k=b.k AND b.r='never'", fixture.OuterJoinsUnmatchedRows("LEFT")),
			fixture.OuterJoinsSelect("SELECT * FROM a LEFT JOIN b ON a.k=b.k WHERE b.r='never'", nil),
			fixture.OuterJoinsSelect("SELECT * FROM a LEFT JOIN b ON a.k=b.k WHERE b.k IS NULL", [][]any{{int64(2), "b", nil, nil}, {nil, "n", nil, nil}}),
			fixture.OuterJoinsSelect("SELECT * FROM a RIGHT JOIN b ON a.k=b.k AND a.l='never'", fixture.OuterJoinsUnmatchedRows("RIGHT")),
			fixture.OuterJoinsSelect("SELECT * FROM a RIGHT JOIN b ON a.k=b.k WHERE a.l='never'", nil),
			fixture.OuterJoinsSelect("SELECT * FROM a FULL JOIN b ON a.k=b.k WHERE a.k IS NULL", [][]any{{nil, "n", nil, nil}, {nil, nil, int64(3), "y"}, {nil, nil, nil, "m"}}),
			fixture.OuterJoinsSelect("SELECT * FROM a FULL JOIN b ON a.k=b.k WHERE b.k IS NULL", [][]any{{int64(2), "b", nil, nil}, {nil, "n", nil, nil}, {nil, nil, nil, "m"}}),
			fixture.OuterJoinsSelect("SELECT * FROM a LEFT JOIN b ON a.k=b.k AND NULL::boolean", fixture.OuterJoinsUnmatchedRows("LEFT")))
	})
}

func TestReferenceQueryOuterJoinsConsumers(t *testing.T) {
	fixture.OuterJoinsScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder) {
		for _, tc := range []struct {
			kind    string
			count   int64
			grouped [][]any
		}{
			{"LEFT", 6, [][]any{{int64(1), int64(4)}, {int64(2), int64(1)}, {nil, int64(1)}}},
			{"RIGHT", 6, [][]any{{int64(1), int64(4)}, {int64(3), int64(1)}, {nil, int64(1)}}},
			{"FULL", 8, [][]any{{int64(1), int64(4)}, {int64(2), int64(1)}, {int64(3), int64(1)}, {nil, int64(2)}}},
		} {
			fixture.OuterJoinsCases(t, p, conn, recorder,
				fixture.Select("SELECT count(*) FROM a "+tc.kind+" JOIN b ON a.k=b.k", []string{"count"}, []string{"INT8"}, [][]any{{tc.count}}),
				fixture.Select("SELECT k,count(*) FROM a "+tc.kind+" JOIN b USING(k) GROUP BY k", []string{"k", "count"}, []string{"INT8", "INT8"}, tc.grouped))
		}
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE destination(k BIGINT,l TEXT,r TEXT)", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO destination SELECT * FROM a FULL JOIN b USING(k)", 8)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, conn, recorder, s)
			fixture.OuterJoinsCases(t, p, conn, recorder, fixture.Select("SELECT * FROM destination", []string{"k", "l", "r"}, []string{"INT8", "TEXT", "TEXT"}, fixture.OuterJoinsMergedRows("FULL")))
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("DELETE FROM destination", 8))
		}
		fixture.OuterJoinsCases(t, p, conn, recorder, fixture.Select("SELECT CASE WHEN b.k IS NULL THEN 'missing' ELSE a.l END AS label,CAST(a.k AS BIGINT) AS key FROM a LEFT JOIN b ON a.k=b.k ORDER BY a.k NULLS LAST LIMIT 5", []string{"label", "key"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {"a", int64(1)}, {"a", int64(1)}, {"a", int64(1)}, {"missing", int64(2)}}))
	})
}

func TestReferenceQueryOuterJoinsDemand(t *testing.T) {
	fixture.OuterJoinsScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder) {
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			fixture.OuterJoinsCases(t, p, conn, recorder,
				fixture.Select("SELECT 1/(a.k-a.k) AS sentinel FROM a "+kind+" JOIN b ON a.k=b.k LIMIT 0", []string{"sentinel"}, []string{"INT4"}, nil),
				fixture.Select("SELECT 1/(a.k-2) AS sentinel FROM a "+kind+" JOIN b ON a.k=b.k LIMIT 1", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(-1)}}))
		}
	})
}
