package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOuterJoinsSyntax(t *testing.T) {
	fixture.OuterJoinsScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder) {
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			fixture.OuterJoinsCases(t, p, conn, recorder,
				fixture.Select("SELECT * FROM a "+kind+" OUTER JOIN b USING(k)", []string{"k", "l", "r"}, []string{"INT8", "TEXT", "TEXT"}, fixture.OuterJoinsMergedRows(kind)),
				fixture.OuterJoinsSelect("SELECT x.*,y.* FROM a x "+kind+" OUTER JOIN b y ON x.k=y.k", fixture.OuterJoinsOnRows(kind)))
		}
	})
}

func TestReferenceQueryOuterJoinsInsertAtomicity(t *testing.T) {
	fixture.OuterJoinsScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder) {
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE required_destination(ak INTEGER,l TEXT,bk BIGINT NOT NULL,r TEXT)", 0))
		for _, prepared := range []bool{false, true} {
			query := "INSERT INTO required_destination SELECT * FROM a LEFT JOIN b ON a.k=b.k"
			if outerJoinsAssert(t, p, conn, recorder, query, prepared) {
				t.Fatal("NOT NULL insert accepted extended NULL")
			}
			fixture.OuterJoinsCases(t, p, conn, recorder, fixture.Select("SELECT count(*) FROM required_destination", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
	})
}
