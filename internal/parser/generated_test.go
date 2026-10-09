package parser

import "testing"

func TestGeneratedStoredGrammar(t *testing.T) {
	statements, err := Parse(`/*é*/ CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	table := statements[0].(*CreateTable)
	definition := table.Elements[1].Column.Constraints[0].Generation
	if definition == nil || definition.Kind != "stored" || definition.Mode != IdentityAlways || definition.Expression == nil {
		t.Fatal(definition)
	}
	if table.Elements[1].Column.Constraints[0].Default != nil || table.Elements[1].Column.Constraints[0].Identity != nil {
		t.Fatal("generation conflated with default/identity")
	}
	statements, err = Parse(`UPDATE g SET b=DEFAULT`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := statements[0].(*Update).Assignments[0].Value.(DefaultExpr); !ok {
		t.Fatal("missing DEFAULT marker")
	}
	for _, q := range []string{`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED) trailing`, `CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED); garbage`} {
		if _, err := Parse(q); err == nil {
			t.Fatal("whole-input guard", q)
		}
	}
	// A column named like a disallowed function is still an ordinary identifier.
	if _, err := Parse(`CREATE TABLE g(sum INT,b INT GENERATED ALWAYS AS(sum+1) STORED)`); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedRestrictionsRemainPositionedSyntax(t *testing.T) {
	for _, expression := range []string{"sum(a)", "grouping(a)", "row_number() OVER ()", "sum(a) OVER (PARTITION BY a ORDER BY a)", "generate_series(1,a)", "(SELECT a)", "(SELECT a FROM g)"} {
		statements, err := Parse(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(` + expression + `) STORED)`)
		if err != nil {
			t.Fatal(expression, err)
		}
		definition := statements[0].(*CreateTable).Elements[1].Column.Constraints[0].Generation
		if _, ok := definition.Expression.(ForbiddenGenerationExpr); !ok {
			t.Fatal("restriction lost", expression, definition)
		}
	}
	if _, err := Parse(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(row_number() OVER (garbage nonsense)) STORED)`); err == nil {
		t.Fatal("window syntax failure masked")
	}
}
