package parser

import (
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"unicode/utf8"
)

func TestDropDatabaseAST(t *testing.T) {
	for _, query := range []string{`DROP DATABASE "München"`, `DROP DATABASE IF EXISTS "München"`, `DROP DATABASE IF EXISTS "München" (FORCE, FORCE)`, `DROP DATABASE IF EXISTS "München" WITH (FORCE, FORCE)`} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
		statement, ok := statements[0].(*DropDatabaseStmt)
		if !ok || statement.Name != "München" || statement.IfExists != strings.Contains(query, "IF EXISTS") || statement.Pos.Character != 1 {
			t.Fatal("positioned DROP AST", statements)
		}
		expected := 0
		if strings.Contains(query, "FORCE") {
			expected = 2
		}
		if len(statement.Options) != expected {
			t.Fatal("ordered option occurrences lost", statement.Options)
		}
		cursor := 0
		for _, option := range statement.Options {
			offset := strings.Index(query[cursor:], "FORCE") + cursor
			position := utf8.RuneCountInString(query[:offset]) + 1
			if option.Name != "force" || option.Position != position || option.Value != "" || option.Default || option.Numeric {
				t.Fatal("raw FORCE option identity", option, position)
			}
			cursor = offset + 5
		}
	}
}

func TestDropDatabaseParseFailures(t *testing.T) {
	for _, query := range []string{"DROP DATABASE", "DROP DATABASE d ()", "DROP DATABASE d WITH ()", "DROP DATABASE d WITH FORCE", "DROP DATABASE d (FORCE=true)", "DROP DATABASE d (FORCE true)", "DROP DATABASE d CASCADE", "DROP DATABASE d RESTRICT", "DROP DATABASE d,e", "DROP DATABASE d WITH (FORCE) tail", "DROP DATABASE d; SELECT )"} {
		statements, err := Parse(query)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || len(statements) != 0 {
			t.Fatal("whole-input DROP failure", query, statements, err)
		}
	}
}
