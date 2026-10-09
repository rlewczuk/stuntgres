package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryOuterJoinsSyntax(t *testing.T) {
	outerJoinsScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder) {
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			outerJoinsCases(t, p, conn, recorder,
				referenceSelect("SELECT * FROM a "+kind+" OUTER JOIN b USING(k)", []string{"k", "l", "r"}, []string{"INT8", "TEXT", "TEXT"}, outerJoinsMergedRows(kind)),
				outerJoinsSelect("SELECT x.*,y.* FROM a x "+kind+" OUTER JOIN b y ON x.k=y.k", outerJoinsOnRows(kind)))
		}
	})
}

func TestReferenceQueryOuterJoinsInsertAtomicity(t *testing.T) {
	outerJoinsScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder) {
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE TABLE required_destination(ak INTEGER,l TEXT,bk BIGINT NOT NULL,r TEXT)", 0))
		for _, prepared := range []bool{false, true} {
			query := "INSERT INTO required_destination SELECT * FROM a LEFT JOIN b ON a.k=b.k"
			if outerJoinsAssert(t, p, conn, recorder, query, prepared) {
				t.Fatal("NOT NULL insert accepted extended NULL")
			}
			outerJoinsCases(t, p, conn, recorder, referenceSelect("SELECT count(*) FROM required_destination", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
	})
}
