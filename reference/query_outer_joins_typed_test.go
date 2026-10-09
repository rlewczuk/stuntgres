package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryOuterJoinsTypedExtension(t *testing.T) {
	outerJoinsScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder) {
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE typed_a(k INTEGER NOT NULL,flag BOOLEAN NOT NULL,label VARCHAR(3) NOT NULL)", 0),
			referenceExec("CREATE TABLE typed_b(k BIGINT NOT NULL,flag BOOLEAN NOT NULL,label CHAR(3) NOT NULL)", 0),
			referenceExec("INSERT INTO typed_a VALUES(1,TRUE,'abc')", 1), referenceExec("INSERT INTO typed_b VALUES(2,FALSE,'xyz')", 1),
		} {
			referenceRunStep(t, p.ctx, conn, recorder, s)
		}
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			var rows [][]any
			if kind != "RIGHT" {
				rows = append(rows, []any{int64(1), true, "abc", nil, nil, nil})
			}
			if kind != "LEFT" {
				rows = append(rows, []any{nil, nil, nil, int64(2), false, "xyz"})
			}
			query := "SELECT * FROM typed_a " + kind + " JOIN typed_b ON typed_a.k=typed_b.k"
			outerJoinsCases(t, p, conn, recorder, referenceSelect(query, []string{"k", "flag", "label", "k", "flag", "label"}, []string{"INT4", "BOOL", "VARCHAR", "INT8", "BOOL", "BPCHAR"}, rows))
			for _, prepared := range []bool{false, true} {
				outerJoinsAssert(t, p, conn, recorder, query, prepared)
				outerJoinsAssertMetadata(t, p, conn, recorder, query, prepared)
			}
		}
		product := [][]any{
			{int64(1), "a", int64(1), "x"}, {int64(1), "a", int64(1), "x"}, {int64(1), "a", int64(3), "y"}, {int64(1), "a", nil, "m"},
			{int64(1), "a", int64(1), "x"}, {int64(1), "a", int64(1), "x"}, {int64(1), "a", int64(3), "y"}, {int64(1), "a", nil, "m"},
			{int64(2), "b", int64(1), "x"}, {int64(2), "b", int64(1), "x"}, {int64(2), "b", int64(3), "y"}, {int64(2), "b", nil, "m"},
			{nil, "n", int64(1), "x"}, {nil, "n", int64(1), "x"}, {nil, "n", int64(3), "y"}, {nil, "n", nil, "m"},
		}
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			outerJoinsCases(t, p, conn, recorder, outerJoinsSelect("SELECT * FROM a "+kind+" JOIN b ON TRUE", product))
		}
	})
}
