package parser

import "testing"

func TestOIDCastSyntax(t *testing.T) {
	for _, q := range []string{"SELECT CAST(v AS oid) FROM o", "SELECT v::oid FROM o", "SELECT oid(v) FROM o"} {
		stmts, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		cast, ok := stmts[0].(*Select).Targets[0].(CastExpr)
		if !ok || cast.TypeName.Name != "oid" {
			t.Fatal(q, stmts)
		}
	}
	statements, err := Parse("SELECT '1'::oid")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	selected := statements[0].(*Select)
	if selected.IsSingleton() || !selected.IsTypeSingleton() {
		t.Fatal("ordinary cast gained singleton authority")
	}
}
