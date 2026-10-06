package parser

import "testing"

func TestSmallintDeclarationSpellings(t *testing.T) {
	for _, tc := range []struct {
		spelling, name string
		quoted         bool
	}{{"SMALLINT", "smallint", false}, {"INT2", "smallint", false}, {`"int2"`, "int2", true}, {`"smallint"`, "smallint", true}, {`"INT2"`, "INT2", true}} {
		statements, err := Parse("CREATE TABLE t(s " + tc.spelling + ")")
		if err != nil {
			t.Fatal(err)
		}
		raw := statements[0].(*CreateTable).Elements[0].Column.TypeName
		if raw.Name != tc.name || raw.Quoted != tc.quoted || raw.Pos.Character != 18 {
			t.Fatal(tc, raw)
		}
	}
}
