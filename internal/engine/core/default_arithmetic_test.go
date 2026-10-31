package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestDefaultSmallintArithmeticWidths(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE d(s SMALLINT DEFAULT (7::smallint/2::smallint),b BIGINT DEFAULT (7::smallint+2147483648::bigint),u SMALLINT DEFAULT (32767::smallint+1::smallint))")
	view := e.CatalogRead()
	ref := enginetest.RoleTableRef(t, e, "d")
	desc := view.MustRelation(ref.Address)
	if err := (core.RelationProvider).Validate(view, desc); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		attribute           int32
		left, right, result sqltype.ID
	}{{1, sqltype.Int2, sqltype.Int2, sqltype.Int2}, {2, sqltype.Int2, sqltype.Int8, sqltype.Int8}, {3, sqltype.Int2, sqltype.Int2, sqltype.Int2}} {
		n, ok := desc.ColumnMeta.Defaults[tc.attribute].Expression.(plan.ArithmeticExpr)
		if !ok || n.Left.ResultUsage().Type.ID() != tc.left || n.Right.ResultUsage().Type.ID() != tc.right || n.Provider.ResultType().ID() != tc.result {
			t.Fatal(tc, n)
		}
	}
	// Validation above must not consume the unused overflowing default.
	enginetest.Run(t, e, "INSERT INTO d(u) VALUES(7)")
	rows := enginetest.Run(t, e, "SELECT s,b,u FROM d")
	defer rows.Rows.Close()
	got := enginetest.Tuples(rows)
	if len(got) != 1 || got[0][0] != int64(3) || got[0][1] != int64(2147483655) || got[0][2] != int64(7) {
		t.Fatal(got)
	}
}
