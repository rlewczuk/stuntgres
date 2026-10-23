package parser

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestCreateDatabaseAST(t *testing.T) {
	query := `CREATE DATABASE "München" WITH OWNER = 'Mixed' TEMPLATE DEFAULT ENCODING 6 OWNER DEFAULT LC_COLLATE 'C' bogus 'default' CONNECTION LIMIT -1`
	statements, err := Parse(query)
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 1 {
		t.Fatal(statements)
	}
	stmt, ok := statements[0].(*CreateDatabaseStmt)
	if !ok || stmt.Name != "München" {
		t.Fatalf("database AST: %#v", statements[0])
	}
	position := func(text string) int {
		return utf8.RuneCountInString(query[:strings.Index(query, text)]) + 1
	}
	want := []DatabaseOption{
		{Name: "owner", Value: "Mixed", Position: position("OWNER =")},
		{Name: "template", Default: true, Position: position("TEMPLATE")},
		{Name: "encoding", Value: "6", Numeric: true, Position: position("ENCODING")},
		{Name: "owner", Default: true, Position: position("OWNER DEFAULT")},
		{Name: "lc_collate", Value: "C", Position: position("LC_COLLATE")},
		{Name: "bogus", Value: "default", Position: position("bogus")},
		{Name: "connection_limit", Value: "-1", Numeric: true, Position: position("CONNECTION")},
	}
	if !reflect.DeepEqual(stmt.Options, want) {
		t.Fatalf("ordered options: got %#v want %#v", stmt.Options, want)
	}
	for _, query := range []string{
		"CREATE DATABASE Mixed",
		"CREATE DATABASE Mixed WITH",
		"CREATE DATABASE Mixed ENCODING = 'UTF8' TEMPLATE template1 OWNER DEFAULT",
		`CREATE DATABASE Mixed OWNER "current_user"`,
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatalf("%s: %v, %v", query, statements, err)
		}
		if stmt, ok := statements[0].(*CreateDatabaseStmt); !ok || stmt.Name != "mixed" {
			t.Fatalf("%s: %#v", query, statements[0])
		}
	}
}

func TestCreateDatabaseParseFailures(t *testing.T) {
	for _, query := range []string{
		"CREATE DATABASE",
		"CREATE DATABASE schema.name",
		"CREATE DATABASE d OWNER",
		"CREATE DATABASE d ENCODING =",
		"CREATE DATABASE d ENCODING 'UTF8' garbage",
		"CREATE DATABASE d ENCODING $1",
		"CREATE DATABASE d OWNER CURRENT_USER",
		"CREATE DATABASE d OWNER SESSION_USER",
		"CREATE DATABASE d OWNER DEFAULT, TEMPLATE DEFAULT",
		"CREATE DATABASE d; CREATE DATABASE e OWNER",
	} {
		statements, err := Parse(query)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || len(statements) != 0 {
			t.Fatalf("whole-input failure %q: %#v, %v", query, statements, err)
		}
	}
}
