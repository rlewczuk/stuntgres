package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestDeparseConstraintsReviewViewLiteralCaptureCutover(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(id INT)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(1)")
	enginetest.Run(t, e, "CREATE VIEW v AS SELECT TIME(3) '04:05:06.1235' AS clock,+2147483648 AS number FROM t")
	view := stored.ViewOf(e.CatalogRead().MustRelation(enginetest.RoleTableRef(t, e, "v").Address))
	literal, ok := view.Literals()[analyze.ViewLiteralPath{Query: view.Query().Root.(*plan.ProjectNode).Frame, Clause: analyze.ViewTargetLiteral, Target: 0, Node: 0}]
	if !ok || literal.Usage.Type.ID() != sqltype.Time || literal.Usage.Typmod != 3 {
		t.Fatal(view.Literals())
	}
	raw, err := literal.Value.Time()
	if err != nil || raw != 14706123500 {
		t.Fatal(raw, err)
	}
	literal, ok = view.Literals()[analyze.ViewLiteralPath{Query: view.Query().Root.(*plan.ProjectNode).Frame, Clause: analyze.ViewTargetLiteral, Target: 1, Node: 1}]
	if !ok || literal.Usage.Type.ID() != sqltype.Int8 {
		t.Fatal(view.Literals())
	}
	number, err := literal.Value.Integer()
	if err != nil || number != 2147483648 {
		t.Fatal(number, err)
	}
	rows := enginetest.Tuples(enginetest.Run(t, e, "SELECT clock=TIME '04:05:06.124' AS rounded,number FROM v"))
	if len(rows) != 1 || len(rows[0]) != 2 || rows[0][0] != true || rows[0][1] != int64(2147483648) {
		t.Fatal(rows)
	}
}
