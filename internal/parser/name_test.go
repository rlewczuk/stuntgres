package parser

import "testing"

// NAME modifiers are retained for the CREATE utility's descriptor-time
// prohibition, not rejected as a parser capability or parsed as int4 input.
func TestNameModifiersReachUtility(t *testing.T) {
	for _, declaration := range []string{"name(3)", `"name"(3)`, "name('oops')", "name(2147483648)"} {
		statements, err := Parse("/*é*/ CREATE TABLE bad(n " + declaration + ")")
		if err != nil {
			t.Fatal(declaration, err)
		}
		raw := statements[0].(*CreateTable).Elements[0].Column.TypeName
		if raw.Name != "name" || raw.Pos.Character != 26 || len(raw.Modifiers) != 1 {
			t.Fatal(declaration, raw)
		}
	}
}
