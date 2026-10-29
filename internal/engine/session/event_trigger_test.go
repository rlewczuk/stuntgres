package session

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/parser"
)

func TestEventTriggerClassification(t *testing.T) {
	seen := map[string]bool{}
	check := func(stmt parser.Statement, want string, eligible bool) {
		t.Helper()
		name := reflect.TypeOf(stmt).Elem().Name()
		seen[name] = true
		tag, got := EventCommand(stmt)
		if got != eligible || eligible && tag != want {
			t.Fatalf("%s: got %q,%v want %q,%v", name, tag, got, want, eligible)
		}
	}
	for _, group := range []struct {
		tag        string
		statements []parser.Statement
	}{
		{"CREATE TABLE", []parser.Statement{&parser.CreateTable{}}},
		{"DROP TABLE", []parser.Statement{&parser.DropTable{}}},
		{"ALTER TABLE", []parser.Statement{&parser.AlterTable{}, &parser.AlterTableDefault{}, &parser.AlterTableOf{}, &parser.AlterTableAttachPartition{}, &parser.AlterTableDetachPartition{}, &parser.AlterTableRenameColumn{}, &parser.AlterTableRenameTable{}, &parser.AlterTableSetSchema{}, &parser.AlterTableRenameConstraint{}, &parser.AlterTableAllInTablespace{}}},
		{"CREATE TABLE AS", []parser.Statement{&parser.CreateQueryTable{Form: parser.CreateTableAs}}},
		{"CREATE VIEW", []parser.Statement{&parser.CreateView{}}},
		{"ALTER VIEW", []parser.Statement{&parser.AlterView{}}},
		{"DROP VIEW", []parser.Statement{&parser.DropView{}}},
		{"REFRESH MATERIALIZED VIEW", []parser.Statement{&parser.RefreshMaterializedView{}}},
		{"CREATE SEQUENCE", []parser.Statement{&parser.CreateSequence{}}},
		{"ALTER SEQUENCE", []parser.Statement{&parser.AlterSequence{}}},
		{"DROP SEQUENCE", []parser.Statement{&parser.DropSequence{}}},
		{"CREATE TYPE", []parser.Statement{&parser.CreateRange{}, &parser.CreateEnum{}, &parser.CreateComposite{}}},
		{"ALTER TYPE", []parser.Statement{&parser.AlterEnum{}, &parser.AlterComposite{}, &parser.AlterType{}}},
		{"DROP TYPE", []parser.Statement{&parser.DropType{}}},
		{"CREATE DOMAIN", []parser.Statement{&parser.CreateDomain{}}},
		{"ALTER DOMAIN", []parser.Statement{&parser.AlterDomain{}}},
		{"DROP DOMAIN", []parser.Statement{&parser.DropDomain{}}},
		{"CREATE SCHEMA", []parser.Statement{&parser.CreateSchema{}}},
		{"ALTER SCHEMA", []parser.Statement{&parser.AlterSchema{}}},
		{"DROP SCHEMA", []parser.Statement{&parser.DropSchema{}}},
		{"CREATE COLLATION", []parser.Statement{&parser.CreateCollation{}}},
		{"ALTER COLLATION", []parser.Statement{&parser.AlterCollation{}}},
		{"DROP COLLATION", []parser.Statement{&parser.DropCollation{}}},
		{"CREATE CONVERSION", []parser.Statement{&parser.CreateConversion{}}},
		{"ALTER CONVERSION", []parser.Statement{&parser.AlterConversion{}}},
		{"DROP CONVERSION", []parser.Statement{&parser.DropConversion{}}},
		{"CREATE STATISTICS", []parser.Statement{&parser.CreateStatistics{}}},
		{"DROP STATISTICS", []parser.Statement{&parser.DropStatistics{}}},
		{"ALTER STATISTICS", []parser.Statement{&parser.AlterStatisticsTarget{}}},
		{"CREATE CAST", []parser.Statement{&parser.CreateCast{}}},
		{"DROP CAST", []parser.Statement{&parser.DropCast{}}},
		{"CREATE FUNCTION", []parser.Statement{&parser.CreateFunction{}}},
		{"CREATE AGGREGATE", []parser.Statement{&parser.CreateAggregate{}}},
		{"ALTER FUNCTION", []parser.Statement{&parser.AlterRoutine{Kind: "function"}}},
		{"DROP FUNCTION", []parser.Statement{&parser.DropRoutine{Kind: "function"}}},
		{"CREATE OPERATOR", []parser.Statement{&parser.CreateOperator{}}},
		{"ALTER OPERATOR", []parser.Statement{&parser.AlterOperator{}}},
		{"DROP OPERATOR", []parser.Statement{&parser.DropOperator{}}},
		{"CREATE OPERATOR FAMILY", []parser.Statement{&parser.CreateOperatorFamily{}}},
		{"ALTER OPERATOR FAMILY", []parser.Statement{&parser.AlterOperatorFamily{}}},
		{"DROP OPERATOR FAMILY", []parser.Statement{&parser.DropOperatorFamily{}}},
		{"CREATE OPERATOR CLASS", []parser.Statement{&parser.CreateOperatorClass{}}},
		{"ALTER OPERATOR CLASS", []parser.Statement{&parser.AlterOperatorClass{}}},
		{"DROP OPERATOR CLASS", []parser.Statement{&parser.DropOperatorClass{}}},
		{"CREATE ACCESS METHOD", []parser.Statement{&parser.CreateAccessMethod{}}},
		{"DROP ACCESS METHOD", []parser.Statement{&parser.DropAccessMethod{}}},
		{"CREATE TRIGGER", []parser.Statement{&parser.CreateTrigger{}}},
		{"ALTER TRIGGER", []parser.Statement{&parser.AlterTrigger{}}},
		{"DROP TRIGGER", []parser.Statement{&parser.DropTrigger{}}},
		{"CREATE RULE", []parser.Statement{&parser.CreateRule{}}},
		{"ALTER RULE", []parser.Statement{&parser.AlterRule{}}},
		{"DROP RULE", []parser.Statement{&parser.DropRule{}}},
		{"CREATE POLICY", []parser.Statement{&parser.CreatePolicy{}}},
		{"ALTER POLICY", []parser.Statement{&parser.AlterPolicy{}}},
		{"DROP POLICY", []parser.Statement{&parser.DropPolicy{}}},
		{"CREATE PUBLICATION", []parser.Statement{&parser.Publication{Action: "create"}}},
		{"ALTER PUBLICATION", []parser.Statement{&parser.Publication{Action: "alter"}}},
		{"DROP PUBLICATION", []parser.Statement{&parser.Publication{Action: "drop"}}},
		{"CREATE EXTENSION", []parser.Statement{&parser.CreateExtension{}}},
		{"ALTER EXTENSION", []parser.Statement{&parser.AlterExtensionUpdate{}, &parser.AlterExtension{Action: "schema"}, &parser.AlterExtension{Action: "add"}, &parser.AlterExtension{Action: "drop"}}},
		{"DROP EXTENSION", []parser.Statement{&parser.DropExtension{}}},
		{"CREATE TEXT SEARCH DICTIONARY", []parser.Statement{&parser.CreateDictionary{}}},
		{"ALTER TEXT SEARCH DICTIONARY", []parser.Statement{&parser.AlterDictionary{}}},
		{"DROP TEXT SEARCH DICTIONARY", []parser.Statement{&parser.DropDictionary{}}},
		{"CREATE TEXT SEARCH TEMPLATE", []parser.Statement{&parser.CreateTemplate{}}},
		{"ALTER TEXT SEARCH TEMPLATE", []parser.Statement{&parser.AlterTemplate{}}},
		{"DROP TEXT SEARCH TEMPLATE", []parser.Statement{&parser.DropTemplate{}}},
		{"CREATE TEXT SEARCH CONFIGURATION", []parser.Statement{&parser.CreateConfiguration{}}},
		{"ALTER TEXT SEARCH CONFIGURATION", []parser.Statement{&parser.AlterConfiguration{}}},
		{"DROP TEXT SEARCH CONFIGURATION", []parser.Statement{&parser.DropConfiguration{}}},
		{"CREATE TEXT SEARCH PARSER", []parser.Statement{&parser.CreateParser{}}},
		{"ALTER TEXT SEARCH PARSER", []parser.Statement{&parser.AlterParser{}}},
		{"DROP TEXT SEARCH PARSER", []parser.Statement{&parser.DropParser{}}},
		{"CREATE INDEX", []parser.Statement{&parser.CreateIndex{}}},
		{"ALTER INDEX", []parser.Statement{&parser.AlterIndex{}}},
		{"DROP INDEX", []parser.Statement{&parser.DropIndex{}}},
		{"REINDEX", []parser.Statement{&parser.Reindex{Kind: parser.Identifier{Text: "table"}}}},
		{"ALTER DEFAULT PRIVILEGES", []parser.Statement{&parser.AlterDefaultPrivileges{}}},
		{"DROP OWNED", []parser.Statement{&parser.DropOwned{}}},
		{"ALTER TABLE", []parser.Statement{&parser.AlterOwner{Kind: "table"}}},
		{"COMMENT", []parser.Statement{&parser.Comment{Kind: parser.CommentTable}}},
		{"GRANT", []parser.Statement{&parser.ObjectGrant{}}},
		{"CREATE FOREIGN TABLE", []parser.Statement{&parser.ForeignDDL{Action: "create", Kind: "table"}}},
	} {
		for _, stmt := range group.statements {
			check(stmt, group.tag, true)
		}
	}
	for _, stmt := range []parser.Statement{
		&parser.CreateEventTrigger{}, &parser.AlterEventTrigger{}, &parser.DropEventTrigger{},
		&parser.CreateDatabaseStmt{}, &parser.CreateTablespace{}, &parser.CreateRole{}, &parser.AlterRole{}, &parser.DropRole{}, &parser.RoleMembership{}, &parser.ReassignOwned{},
		&parser.RenameRole{}, &parser.AlterRoleSettings{}, &parser.AlterDatabaseSettings{},
		&parser.Call{}, &parser.Insert{}, &parser.Update{}, &parser.Delete{}, &parser.Merge{}, &parser.Select{}, &parser.Copy{},
		&parser.Explain{Statement: &parser.Insert{}},
		&parser.Do{}, &parser.Load{}, &parser.Vacuum{}, &parser.Analyze{}, &parser.Truncate{}, &parser.LockTable{},
		&parser.PrepareSQL{}, &parser.ExecuteSQL{}, &parser.DeallocateSQL{}, &parser.DeclareCursor{}, &parser.FetchCursor{}, &parser.CloseCursor{},
		&parser.Listen{}, &parser.Unlisten{}, &parser.Notify{}, &parser.Discard{},
		&parser.Set{}, &parser.Reset{}, &parser.Show{}, &parser.RoleSelection{}, &parser.SessionAuthorization{},
		&parser.AlterSystem{},
		&parser.TransactionControl{}, &parser.TransactionCharacteristics{}, &parser.SavepointControl{}, &parser.SetConstraints{},
	} {
		check(stmt, "", false)
	}
	// Subscriptions are shared objects, excluded from event triggers for every action.
	for _, action := range []string{"create", "connection", "options", "set", "add", "remove", "refresh", "enable", "disable", "skip", "owner", "rename", "drop"} {
		check(&parser.Subscription{Action: action}, "", false)
	}
	check(&parser.CreateQueryTable{Form: parser.SelectInto}, "SELECT INTO", true)
	check(&parser.CreateQueryTable{Form: parser.CreateMaterializedView}, "CREATE MATERIALIZED VIEW", true)
	check(&parser.AlterView{Materialized: true}, "ALTER MATERIALIZED VIEW", true)
	check(&parser.DropView{Materialized: true}, "DROP MATERIALIZED VIEW", true)
	check(&parser.CreateFunction{Procedure: true}, "", false)
	check(&parser.CreateIndex{Concurrent: parser.Position{Character: 1}}, "", false)
	check(&parser.RefreshMaterializedView{Concurrent: parser.Position{Character: 1}}, "REFRESH MATERIALIZED VIEW", true)
	check(&parser.CreateLanguage{}, "", false)
	check(&parser.AlterLanguage{}, "", false)
	check(&parser.DropLanguage{}, "", false)
	for _, kind := range []parser.CommentKind{parser.CommentColumn, parser.CommentSchema, parser.CommentSequence, parser.CommentForeignTable, parser.CommentExtension} {
		check(&parser.Comment{Kind: kind}, "COMMENT", true)
	}
	check(&parser.Comment{Kind: parser.CommentRole}, "", false)
	for _, kind := range []string{"database", "tablespace", "parameter"} {
		check(&parser.ObjectGrant{Kind: kind}, "", false)
		check(&parser.ObjectGrant{Kind: kind, Revoke: true}, "", false)
	}
	for _, kind := range []string{"", "sequence", "foreign server", "foreign-data wrapper", "function", "procedure", "routine", "schema", "type", "domain", "language", "large object"} {
		check(&parser.ObjectGrant{Kind: kind}, "GRANT", true)
		check(&parser.ObjectGrant{Kind: kind, Revoke: true}, "REVOKE", true)
	}
	check(&parser.AlterOwner{Kind: "database"}, "", false)
	check(&parser.AlterOwner{Kind: "type"}, "ALTER TYPE", true)
	check(&parser.AlterOwner{Kind: "domain"}, "ALTER DOMAIN", true)
	check(&parser.AlterOwner{Kind: "large object"}, "ALTER LARGE OBJECT", true)
	for _, kind := range []string{"wrapper", "server", "mapping", "table"} {
		for _, action := range []string{"create", "alter", "drop"} {
			object := map[string]string{"wrapper": "FOREIGN DATA WRAPPER", "server": "SERVER", "mapping": "USER MAPPING", "table": "FOREIGN TABLE"}[kind]
			check(&parser.ForeignDDL{Action: action, Kind: kind}, strings.ToUpper(action)+" "+object, true)
		}
	}
	check(&parser.ForeignDDL{Action: "import", Kind: "table"}, "IMPORT FOREIGN SCHEMA", true)
	// A new shipped statement must acquire a deliberate test category instead of
	// silently inheriting the safe default. This scans declarations, not SQL text.
	files, err := os.ReadDir("../../parser")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".go") || strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		unit, err := goparser.ParseFile(token.NewFileSet(), filepath.Join("../../parser", file.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range unit.Decls {
			method, ok := declaration.(*ast.FuncDecl)
			if !ok || method.Name.Name != "statement" || method.Recv == nil {
				continue
			}
			pointer, ok := method.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				t.Fatal("unexpected statement receiver", file.Name())
			}
			name := pointer.X.(*ast.Ident).Name
			if !seen[name] {
				t.Errorf("shipped AST %s has no deliberate event-eligibility witness", name)
			}
		}
	}
}
