package parser

import "testing"

func TestPLpgSQLAssignmentParser(t *testing.T) {
	for _, source := range []string{
		"BEGIN a[1].items[2:3] := '{7,8}'::integer[]; RETURN a[1].items::text; END",
		"BEGIN $1[1].items[:2] = '{7,8}'::integer[]; RETURN $1[1].items::text; END",
		"<<scope>> DECLARE a integer[]; x ALIAS FOR a; BEGIN scope.a[1][4] = 7; x[:] := NULL; END",
	} {
		block, err := ParsePLpgSQL(source, PLReturnPolicy{})
		if err != nil || block == nil {
			t.Fatal(source, err)
		}
		if len(block.Statements) == 0 || block.Statements[0].Target == nil {
			t.Fatal("assignment target missing", source)
		}
		if _, err := ParsePLpgSQL(source+" trailing", PLReturnPolicy{}); err == nil {
			t.Fatal("accepted trailing procedural input", source)
		}
	}
	block, err := ParsePLpgSQL("BEGIN a[1].items[2:3] := 7; RETURN a[1].items; END", PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	outer, ok := block.Statements[0].Target.(SubscriptExpr)
	if !ok || len(outer.Subscripts) != 1 || !outer.Subscripts[0].Slice {
		t.Fatal("lost final slice", block.Statements[0].Target)
	}
	field, ok := outer.Base.(FieldExpr)
	if !ok || field.Field.Text != "items" {
		t.Fatal("lost ordered field", outer.Base)
	}
	inner, ok := field.Base.(SubscriptExpr)
	if !ok || len(inner.Subscripts) != 1 || inner.Subscripts[0].Slice {
		t.Fatal("lost leading element", field.Base)
	}
	root, ok := inner.Base.(ColumnExpr)
	if !ok || root.Name.Parts[0].Text != "a" || root.Pos.Character != 7 || inner.Subscripts[0].Pos.Character != 8 || field.Field.Pos.Character != 12 || outer.Subscripts[0].Pos.Character != 17 {
		t.Fatal("lost target source positions", outer)
	}
	read, ok := block.Statements[1].Expression.(FieldExpr)
	if !ok || read.Field.Text != "items" {
		t.Fatal("subscript field read not retained", block.Statements[1].Expression)
	}
	if _, ok := read.Base.(SubscriptExpr); !ok {
		t.Fatal("read lost its subscript base", read.Base)
	}
}

func TestPLpgSQLFirstTargetName(t *testing.T) {
	const source = "/* café 雪 */ DECLARE first integer; BEGIN first:=x; FETCH FIRST FROM c INTO first; first:=first+1; END"
	block, err := ParsePLpgSQL(source, PLReturnPolicy{})
	if err != nil || block == nil {
		t.Fatal("unreserved FIRST target rejected", err)
	}
	if len(block.Declarations) != 1 || block.Declarations[0].Name.Text != "first" || len(block.Statements) != 3 {
		t.Fatal("FIRST declaration or statements lost", block)
	}
	for _, index := range []int{0, 2} {
		statement := block.Statements[index]
		target, ok := statement.Target.(ColumnExpr)
		if !ok || len(target.Name.Parts) != 1 || target.Name.Parts[0].Text != "first" || target.SourcePosition() != statement.Pos {
			t.Fatal("FIRST target identity/position lost", statement)
		}
		if got := PLExpressionSource(source, statement.ExpressionStart, statement.ExpressionEnd); got != []string{"x", "", "first+1"}[index] {
			t.Fatal("FIRST assignment expression changed", got)
		}
	}
	cursor := block.Statements[1].Cursor
	if cursor == nil || cursor.Action != PLFetchCursor || len(cursor.Targets) != 1 {
		t.Fatal("FETCH FIRST cursor statement changed", block.Statements[1])
	}
	if got := PLExpressionSource(source, cursor.Motion.CountStart, cursor.Motion.CountEnd); got != "FIRST" {
		t.Fatal("FIRST motion consumed as a target", got)
	}
	target, ok := cursor.Targets[0].(ColumnExpr)
	if !ok || target.Name.Parts[0].Text != "first" {
		t.Fatal("FETCH target name lost", cursor.Targets)
	}
	if statements, err := Parse("SELECT first FROM names"); err != nil || len(statements) != 1 {
		t.Fatal("FIRST target admission leaked into SQL parsing", statements, err)
	}
}

func TestPLpgSQLSecondTargetName(t *testing.T) {
	const source = "DECLARE second integer; BEGIN second:=x; second:=second+1; END"
	block, err := ParsePLpgSQL(source, PLReturnPolicy{})
	if err != nil || block == nil || len(block.Declarations) != 1 || block.Declarations[0].Name.Text != "second" || len(block.Statements) != 2 {
		t.Fatal("unreserved SECOND target rejected", block, err)
	}
	for _, statement := range block.Statements {
		target, ok := statement.Target.(ColumnExpr)
		if !ok || len(target.Name.Parts) != 1 || target.Name.Parts[0].Text != "second" || target.SourcePosition() != statement.Pos {
			t.Fatal("SECOND target identity/position lost", statement)
		}
	}
	if statements, err := Parse("SELECT EXTRACT(SECOND FROM INTERVAL '1 second')"); err != nil || len(statements) != 1 {
		t.Fatal("SECOND target admission changed temporal field syntax", statements, err)
	}
}
