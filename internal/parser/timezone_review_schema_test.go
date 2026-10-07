package parser

import (
	"strings"
	"testing"
)

func TestTimezoneReviewQualifiedLiteralSchema(t *testing.T) {
	for _, name := range []string{"timestamptz", "timetz"} {
		for _, quoted := range []bool{false, true} {
			spelling := name
			if quoted {
				spelling = `"` + name + `"`
			}
			for _, precision := range []string{"", "(3)"} {
				query := "SELECT pg_catalog." + spelling + precision + " '2004-07-01 12:00+00' FROM t"
				t.Run(spelling+precision, func(t *testing.T) {
					statements, err := Parse(query)
					if err != nil {
						t.Fatal(err)
					}
					literal := statements[0].(*Select).Targets[0].(TypedLiteralExpr)
					typ := literal.TypeName
					if typ.Schema == nil || typ.Schema.Text != "pg_catalog" || typ.Schema.Pos.Offset != 7 || typ.Schema.Pos.Character != 8 {
						t.Fatalf("explicit namespace lost: %#v", typ)
					}
					if typ.Name != name || typ.Quoted != quoted || typ.Pos.Offset != 7 || literal.Pos.Offset != strings.Index(query, "'") {
						t.Fatalf("literal identity/positions: %#v", literal)
					}
					if precision == "" {
						if len(typ.Modifiers) != 0 {
							t.Fatalf("unexpected modifiers: %#v", typ.Modifiers)
						}
					} else if len(typ.Modifiers) != 1 || typ.Modifiers[0].Integer == nil || *typ.Modifiers[0].Integer != 3 {
						t.Fatalf("precision lost: %#v", typ.Modifiers)
					}
				})
			}
		}
	}
}
