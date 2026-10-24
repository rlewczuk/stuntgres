package parser

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestInfoSQLSizingIdentifierBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"ASCII63", strings.Repeat("a", 63), strings.Repeat("a", 63)},
		{"ASCII64", strings.Repeat("a", 64), strings.Repeat("a", 63)},
		{"UTF8_63", strings.Repeat("é", 31) + "a", strings.Repeat("é", 31) + "a"},
		{"UTF8_64", strings.Repeat("é", 32), strings.Repeat("é", 31)},
	} {
		for _, quoted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/quoted=%t", tc.name, quoted), func(t *testing.T) {
				name := tc.input
				if quoted {
					name = `"` + name + `"`
				}
				var notices []sqlstate.Error
				statements, err := ParseWithOptions("CREATE TABLE "+name+"("+name+" integer)", Options{StandardConformingStrings: true, Notice: func(n sqlstate.Error) { notices = append(notices, n) }})
				if err != nil || len(statements) != 1 {
					t.Fatal(statements, err)
				}
				table := statements[0].(*CreateTable)
				if table.Name.Parts[0].Text != tc.want || table.Elements[0].Column.Name != tc.want {
					t.Fatal("identifier byte clipping", table)
				}
				var want []sqlstate.Error
				if tc.input != tc.want {
					notice := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: fmt.Sprintf(`identifier "%s" will be truncated to "%s"`, tc.input, tc.want)}
					want = []sqlstate.Error{notice, notice}
				}
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("notices=%#v want %#v", notices, want)
				}
			})
		}
	}
}
