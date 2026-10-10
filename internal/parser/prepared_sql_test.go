package parser

import (
	"testing"
	"strings"
	"unicode/utf8"
)

func TestSQLPreparedRawDefinitionPositions(t *testing.T) {
	source := `/* é */ PREPARE "Mixed"(int, varchar(2)) AS SELECT $1 AS value`
	statements, err := Parse(source)
	if err != nil || len(statements) != 1 { t.Fatal(statements, err) }
	raw, ok := statements[0].(*PrepareSQL)
	if !ok || raw.Source != source || raw.Name.Text != "Mixed" || len(raw.Types) != 2 { t.Fatalf("raw definition: %#v", statements[0]) }
	body, ok := raw.Body.(*Select)
	if !ok { t.Fatalf("body %T", raw.Body) }
	parameter, ok := body.Targets[0].(ParameterExpr)
	if !ok || parameter.Number != 1 { t.Fatal(body.Targets) }
	offset := strings.Index(source, "$1")
	if parameter.Pos.Offset != offset || parameter.Pos.Character != utf8.RuneCountInString(source[:offset])+1 { t.Fatal("body positions rebased", parameter.Pos) }
	visited := 0
	if err := VisitQuerySources(raw, func(query *Select) error { if query != body { t.Fatal("traversal reparsed body") }; visited++; return nil }, nil); err != nil || visited != 1 { t.Fatal(visited, err) }
}

func TestSQLPreparedRawLifecycleForms(t *testing.T) {
	for _, source := range []string{"DEALLOCATE ALL", "DEALLOCATE PREPARE ALL", `DEALLOCATE "Mixed"`, `DEALLOCATE PREPARE "Mixed"`} {
		statements, err := Parse(source)
		if err != nil || len(statements) != 1 { t.Fatal(source, err) }
		raw, ok := statements[0].(*DeallocateSQL)
		if !ok || raw.All != strings.HasSuffix(source, "ALL") { t.Fatal(source, statements) }
	}
	statements, err := Parse(`EXECUTE "Mixed"(1+2, CASE WHEN TRUE THEN NULL ELSE 7 END)`)
	if err != nil || len(statements) != 1 { t.Fatal(err) }
	raw, ok := statements[0].(*ExecuteSQL)
	if !ok || raw.Name.Text != "Mixed" || len(raw.Arguments) != 2 { t.Fatal(statements) }
	statements, err = Parse(`PREPARE TRANSACTION 'gid'`)
	if err != nil || len(statements) != 1 { t.Fatal(err) }
	control, ok := statements[0].(*TransactionControl)
	if !ok || control.Kind != KindPrepareTransaction { t.Fatal("SQL name shadowed prepared transaction syntax") }
	for _, source := range []string{"PREPARE p() AS SELECT 1", "EXECUTE p()", "PREPARE p AS SET application_name TO p", "PREPARE p AS SELECT 1 INTO t"} {
		if _, err := Parse(source); err == nil { t.Fatal("invalid preparable syntax admitted", source) }
	}
}
