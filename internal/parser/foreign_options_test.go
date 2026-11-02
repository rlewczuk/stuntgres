package parser

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestFDWParserColumnOptionsTypeBoundary(t *testing.T) {
	for _, typ := range []struct {
		sql, name, schema string
		quoted            bool
		arrays            int
	}{
		{sql: "bytea", name: "bytea"},
		{sql: "pg_catalog.bytea", name: "bytea", schema: "pg_catalog"},
		{sql: "custom_type", name: "custom_type"},
		{sql: `s."Custom Type"`, name: "Custom Type", schema: "s", quoted: true},
		{sql: "bytea[]", name: "bytea", arrays: 1},
		{sql: "options", name: "options"},
		{sql: `"options"`, name: "options", quoted: true},
	} {
		for _, prefix := range []string{
			`CREATE FOREIGN TABLE "é Options"(`,
			`ALTER FOREIGN TABLE "é Options" ADD COLUMN `,
			`ALTER FOREIGN TABLE "é Options" ADD COLUMN IF NOT EXISTS `,
		} {
			suffix := ""
			if strings.HasPrefix(prefix, "CREATE") {
				suffix = ") SERVER remote"
			}
			q := prefix + `"Payload" ` + typ.sql + ` OPTIONS(column_name 'Remote Bytes') NOT NULL` + suffix
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatalf("%s: statements=%#v err=%v", q, statements, err)
			}
			declaration := statements[0].(*ForeignDDL)
			var column *Column
			var options []ForeignOption
			if declaration.Action == "create" {
				column = declaration.Table.Elements[0].Column
				options = declaration.ColumnOptions["Payload"]
			} else {
				if len(declaration.TableActions) != 1 {
					t.Fatal("lost ADD action", declaration)
				}
				action := declaration.TableActions[0]
				column, options = action.Ordinary.Definition, action.ColumnOptions
				if action.Ordinary.IfNotExists != strings.Contains(prefix, "IF NOT EXISTS") {
					t.Fatal("lost ADD admission spelling", action)
				}
			}
			if column == nil || column.Name != "Payload" || column.TypeName.Name != typ.name || column.TypeName.Quoted != typ.quoted || len(column.TypeName.ArrayBounds) != typ.arrays {
				t.Fatal("lost column/type identity", q, column)
			}
			if typ.schema == "" && column.TypeName.Schema != nil || typ.schema != "" && (column.TypeName.Schema == nil || column.TypeName.Schema.Text != typ.schema) {
				t.Fatal("lost type namespace", q, column.TypeName)
			}
			if len(column.Constraints) != 1 || !column.Constraints[0].NotNull || len(options) != 1 || options[0].Name.Text != "column_name" || options[0].Value != "Remote Bytes" {
				t.Fatal("lost options/NOT NULL", q, column, options)
			}
			position := func(token string) int { return utf8.RuneCountInString(q[:strings.Index(q, token)]) + 1 }
			if column.Pos.Character != position(`"Payload"`) || column.TypeName.Pos.Character != position(typ.sql) || options[0].Name.Pos.Character != position("column_name") {
				t.Fatal("lost original character positions", q, column, options)
			}
		}
	}
	keyword := lookupKeyword("options")
	if keyword == nil || keyword.category != Unreserved || !keyword.bareLabel || IdentifierNeedsQuotes("options") {
		t.Fatal("OPTIONS lost its ordinary identifier category", keyword)
	}
	for _, q := range []string{
		"SELECT options, options(options) AS options FROM options",
		"CREATE TABLE options(options options)",
		"CREATE DOMAIN options AS bytea",
		"CREATE FUNCTION options(options options) RETURNS options LANGUAGE SQL AS 'SELECT options'",
		"EXPLAIN(options true) SELECT 1",
	} {
		if statements, err := Parse(q); err != nil || len(statements) != 1 {
			t.Fatal("OPTIONS identifier/type/function/generic word changed", q, statements, err)
		}
	}
	for _, q := range []string{
		"CREATE TABLE t(v bytea OPTIONS(column_name 'v'))",
		"ALTER TABLE t ADD COLUMN v bytea OPTIONS(column_name 'v')",
		"ALTER TABLE IF EXISTS t ADD COLUMN v bytea OPTIONS(column_name 'v')",
		"ALTER TABLE IF EXISTS t ADD COLUMN IF NOT EXISTS v bytea OPTIONS(column_name 'v')",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal("ordinary OPTIONS must reach execution kind admission", q, statements, err)
		}
		var column *Column
		switch statement := statements[0].(type) {
		case *CreateTable:
			column = statement.Elements[0].Column
		case *AlterTable:
			column = statement.Actions[0].Definition
			guarded := strings.Contains(q, "IF EXISTS")
			if statement.IfExists != guarded || statement.Actions[0].IfNotExists != strings.Contains(q, "IF NOT EXISTS") {
				t.Fatal("ordinary OPTIONS lost existence gates", statement)
			}
		default:
			t.Fatalf("ordinary OPTIONS changed statement identity: %T", statement)
		}
		if column == nil || column.Name != "v" || column.TypeName.Name != "bytea" || len(column.ForeignOptions) != 1 || column.ForeignOptions[0].Name.Text != "column_name" || column.ForeignOptions[0].Value != "v" {
			t.Fatal("ordinary OPTIONS lost positioned column declaration", q, column)
		}
	}
	for _, q := range []string{
		"CREATE FOREIGN TABLE f(v bytea NOT NULL OPTIONS(column_name 'v')) SERVER s",
		"ALTER FOREIGN TABLE f ADD COLUMN v bytea NOT NULL OPTIONS(column_name 'v')",
		"CREATE TABLE t(v bytea nonsense)",
		"CREATE FOREIGN TABLE f(v bytea nonsense) SERVER s",
		"ALTER TABLE t ADD COLUMN v bytea nonsense",
		`CREATE FOREIGN TABLE f(v bytea "options"(column_name 'v')) SERVER s`,
	} {
		token := "OPTIONS"
		if strings.Contains(q, "nonsense") {
			token = "nonsense"
		}
		if strings.Contains(q, `"options"`) {
			token = `"options"`
		}
		statements, err := Parse(q)
		var diagnostic *sqlstate.Error
		wantPosition := strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, token)]) + 1)
		if statements != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Position != wantPosition {
			t.Fatal("invalid column/type tail boundary changed", q, statements, diagnostic, err)
		}
		if token != `"options"` && diagnostic.Message != `syntax error at or near "`+token+`"` {
			t.Fatal("invalid type tail diagnostic changed", q, diagnostic)
		}
	}
}
