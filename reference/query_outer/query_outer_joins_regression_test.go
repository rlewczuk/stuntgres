package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOuterJoinsRegression(t *testing.T) {
	fixture.OuterJoinsScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder) {
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			for _, spelling := range []string{kind, kind + " OUTER"} {
				fixture.OuterJoinsCases(t, p, conn, recorder, fixture.OuterJoinsSelect("SELECT * FROM a "+spelling+" JOIN b ON a.k=b.k", fixture.OuterJoinsOnRows(kind)))
			}
		}
		fixture.OuterJoinsCases(t, p, conn, recorder,
			fixture.Select("SELECT a.l,b.r FROM a LEFT JOIN b ON a.k=$1 AND a.k=b.k", []string{"l", "r"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"a", "x"}, {"a", "x"}, {"a", "x"}, {"b", nil}, {"n", nil}}, int64(1)),
			fixture.Select("SELECT a.l,b.r FROM a LEFT JOIN b ON a.k=$1 AND a.k=b.k", []string{"l", "r"}, []string{"TEXT", "TEXT"}, [][]any{{"a", nil}, {"a", nil}, {"b", nil}, {"n", nil}}, nil))
		for _, s := range []fixture.Step{fixture.Exec("INSERT INTO a VALUES(2,'b')", 1), fixture.Exec("INSERT INTO b VALUES(3,'y')", 1)} {
			fixture.RunStep(t, p.Ctx, conn, recorder, s)
		}
		rows := append(fixture.OuterJoinsOnRows("FULL"), []any{int64(2), "b", nil, nil}, []any{nil, nil, int64(3), "y"})
		fixture.OuterJoinsCases(t, p, conn, recorder, fixture.OuterJoinsSelect("SELECT * FROM a FULL JOIN b ON a.k=b.k", rows))
	})
}

func TestReferenceQueryOuterJoinsMergedKeys(t *testing.T) {
	fixture.OuterJoinsScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder) {
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			merged := fixture.OuterJoinsMergedRows(kind)
			fixture.OuterJoinsCases(t, p, conn, recorder,
				fixture.Select("SELECT * FROM a "+kind+" JOIN b USING(k)", []string{"k", "l", "r"}, []string{"INT8", "TEXT", "TEXT"}, merged),
				fixture.Select("SELECT * FROM (a "+kind+" JOIN b USING(k)) AS j(kk,ll,rr)", []string{"kk", "ll", "rr"}, []string{"INT8", "TEXT", "TEXT"}, merged))
			keys := [][]any{{int64(1), int64(1), int64(1)}, {int64(1), int64(1), int64(1)}, {int64(1), int64(1), int64(1)}, {int64(1), int64(1), int64(1)}}
			if kind != "RIGHT" {
				keys = append(keys, []any{int64(2), nil, int64(2)}, []any{nil, nil, nil})
			}
			if kind != "LEFT" {
				keys = append(keys, []any{nil, int64(3), int64(3)}, []any{nil, nil, nil})
			}
			fixture.OuterJoinsCases(t, p, conn, recorder,
				fixture.Select("SELECT a.k,b.k,k FROM a "+kind+" JOIN b USING(k)", []string{"k", "k", "k"}, []string{"INT4", "INT8", "INT8"}, keys),
				fixture.Select("SELECT a.k,b.k,u.* FROM a "+kind+" JOIN b USING(k) AS u", []string{"k", "k", "k"}, []string{"INT4", "INT8", "INT8"}, keys),
				fixture.OuterJoinsSelect("SELECT a.*,b.* FROM a "+kind+" JOIN b USING(k)", fixture.OuterJoinsOnRows(kind)))
		}
	})
}

func TestReferenceQueryOuterJoinsNatural(t *testing.T) {
	fixture.OuterJoinsScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder) {
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			for _, spelling := range []string{kind, kind + " OUTER"} {
				fixture.OuterJoinsCases(t, p, conn, recorder, fixture.Select("SELECT * FROM a NATURAL "+spelling+" JOIN b", []string{"k", "l", "r"}, []string{"INT8", "TEXT", "TEXT"}, fixture.OuterJoinsMergedRows(kind)))
			}
		}
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE multi_a(q INT,k INT,l TEXT)", 0), fixture.Exec("CREATE TABLE multi_b(k INT,q INT,r TEXT)", 0),
			fixture.Exec("INSERT INTO multi_a VALUES(9,1,'a'),(8,2,'b')", 2), fixture.Exec("INSERT INTO multi_b VALUES(1,9,'x'),(3,7,'y')", 2),
			fixture.Exec("CREATE TABLE disjoint(z BOOLEAN)", 0),
		} {
			fixture.RunStep(t, p.Ctx, conn, recorder, s)
		}
		fixture.OuterJoinsCases(t, p, conn, recorder,
			fixture.Select("SELECT * FROM multi_a NATURAL FULL JOIN multi_b", []string{"q", "k", "l", "r"}, []string{"INT4", "INT4", "TEXT", "TEXT"}, [][]any{{int64(9), int64(1), "a", "x"}, {int64(8), int64(2), "b", nil}, {int64(7), int64(3), nil, "y"}}),
			fixture.Select("SELECT * FROM multi_a FULL JOIN multi_b USING(k,q)", []string{"k", "q", "l", "r"}, []string{"INT4", "INT4", "TEXT", "TEXT"}, [][]any{{int64(1), int64(9), "a", "x"}, {int64(2), int64(8), "b", nil}, {int64(3), int64(7), nil, "y"}}),
			fixture.Select("SELECT * FROM a NATURAL LEFT JOIN disjoint", []string{"k", "l", "z"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), "a", nil}, {int64(1), "a", nil}, {int64(2), "b", nil}, {nil, "n", nil}}),
			fixture.Select("SELECT * FROM disjoint NATURAL RIGHT JOIN b", []string{"z", "k", "r"}, []string{"BOOL", "INT8", "TEXT"}, [][]any{{nil, int64(1), "x"}, {nil, int64(1), "x"}, {nil, int64(3), "y"}, {nil, nil, "m"}}))
	})
}

func TestReferenceQueryOuterJoinsEmptyAndTruth(t *testing.T) {
	fixture.OuterJoinsScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder) {
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			for _, truth := range []string{"FALSE", "NULL::boolean"} {
				fixture.OuterJoinsCases(t, p, conn, recorder, fixture.OuterJoinsSelect("SELECT * FROM a "+kind+" JOIN b ON "+truth, fixture.OuterJoinsUnmatchedRows(kind)))
			}
			var left, right [][]any
			if kind != "RIGHT" {
				left = fixture.OuterJoinsUnmatchedRows("LEFT")
			}
			if kind != "LEFT" {
				right = fixture.OuterJoinsUnmatchedRows("RIGHT")
			}
			for _, condition := range []string{"TRUE", "a.k=b.k"} {
				fixture.OuterJoinsCases(t, p, conn, recorder,
					fixture.OuterJoinsSelect("SELECT * FROM a "+kind+" JOIN empty_b b ON "+condition, left),
					fixture.OuterJoinsSelect("SELECT * FROM empty_a a "+kind+" JOIN b ON "+condition, right),
					fixture.OuterJoinsSelect("SELECT * FROM empty_a a "+kind+" JOIN empty_b b ON "+condition, nil))
			}
		}
		for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE zero_a()", 0), fixture.Exec("CREATE TABLE zero_b()", 0), fixture.Exec("INSERT INTO zero_a DEFAULT VALUES", 1), fixture.Exec("INSERT INTO zero_a DEFAULT VALUES", 1), fixture.Exec("INSERT INTO zero_b DEFAULT VALUES", 1), fixture.Exec("INSERT INTO zero_b DEFAULT VALUES", 1), fixture.Exec("INSERT INTO zero_b DEFAULT VALUES", 1)} {
			fixture.RunStep(t, p.Ctx, conn, recorder, s)
		}
		for _, tc := range []struct {
			kind, truth string
			rows        [][]any
		}{
			{"LEFT", "TRUE", [][]any{{}, {}, {}, {}, {}, {}}}, {"RIGHT", "TRUE", [][]any{{}, {}, {}, {}, {}, {}}}, {"FULL", "TRUE", [][]any{{}, {}, {}, {}, {}, {}}},
			{"LEFT", "FALSE", [][]any{{}, {}}}, {"RIGHT", "FALSE", [][]any{{}, {}, {}}}, {"FULL", "FALSE", [][]any{{}, {}, {}, {}, {}}},
		} {
			fixture.OuterJoinsCases(t, p, conn, recorder, fixture.Select("SELECT FROM zero_a "+tc.kind+" JOIN zero_b ON "+tc.truth, []string{}, []string{}, tc.rows))
		}
	})
}

func TestReferenceQueryOuterJoinsNested(t *testing.T) {
	fixture.OuterJoinsScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder) {
		fixture.OuterJoinsCases(t, p, conn, recorder,
			fixture.Select("SELECT a.k,j.* FROM a LEFT JOIN (empty_a FULL JOIN empty_b USING(k)) j ON a.k=j.k", []string{"k", "k", "l", "r"}, []string{"INT4", "INT8", "TEXT", "TEXT"}, [][]any{{int64(1), nil, nil, nil}, {int64(1), nil, nil, nil}, {int64(2), nil, nil, nil}, {nil, nil, nil, nil}}),
			fixture.Select("SELECT k,j.l,j.r FROM (a FULL JOIN b USING(k)) j LEFT JOIN empty_a e USING(k)", []string{"k", "l", "r"}, []string{"INT8", "TEXT", "TEXT"}, fixture.OuterJoinsMergedRows("FULL")))
	})
}
