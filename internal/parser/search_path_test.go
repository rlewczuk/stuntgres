package parser

import "testing"

func TestSearchPathQualifiedSyntax(t *testing.T) {
	for _, q := range []string{
		"CREATE TABLE operator(x INT)",
		"SELECT operator FROM operator",
		"CREATE TABLE s.t(id pg_catalog.int4,n pg_catalog.numeric(5,2),note pg_catalog.text)",
		"SELECT CAST(id AS pg_catalog.int4),pg_catalog.int4(id),pg_catalog.count(*) FROM s.t",
		"SELECT pg_catalog.num_nulls(NULL),pg_catalog.current_schemas($1)",
		"SELECT pg_catalog.nextval('s.seq')",
		"SELECT pg_catalog.current_setting('search_path')",
		"SELECT pg_catalog.set_config('search_path','s',false)",
		"SELECT pg_catalog.current_schema()",
		"SELECT id OPERATOR(pg_catalog.-) 2 + 3 FROM s.t",
		"SELECT OPERATOR(pg_catalog.-) id FROM s.t",
		"SELECT id OPERATOR(pg_catalog.=) 7 FROM s.t",
	} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, q := range []string{"SELECT 1::pg_catalog.int4", "SELECT 1 OPERATOR(pg_catalog.+) 2", "CREATE TABLE t(a NAME[])", "CREATE TABLE t(a pg_catalog.int4[])"} {
		if statements, err := Parse(q); err == nil || len(statements) != 0 {
			t.Fatal("admitted excluded syntax", q, statements, err)
		}
	}
	statements, err := Parse("SELECT id OPERATOR(pg_catalog.-) 2 + 3 FROM s.t")
	if err != nil {
		t.Fatal(err)
	}
	op := statements[0].(*Select).Targets[0].(OperatorExpr)
	if len(op.Name.Parts) != 2 || op.Name.Parts[0].Text != "pg_catalog" || op.Name.Parts[1].Text != "-" || op.Pos.Character != 11 {
		t.Fatal(op)
	}
	rhs, ok := op.Args[1].(OperatorExpr)
	if !ok || rhs.Name.Parts[0].Text != "+" {
		t.Fatal("qualified operator precedence", op)
	}
}

func TestSearchPathQualifiedTemporalModifiers(t *testing.T) {
	for _, tc := range []struct {
		terminal, name    string
		quoted            bool
		precisionPosition int
	}{
		{"time", "time", false, 41},
		{"timestamp", "timestamp", false, 46},
		{`"time"`, "time", true, 43},
		{`"timestamp"`, "timestamp", true, 48},
	} {
		statements, err := Parse("CREATE TABLE temporal(v pg_catalog." + tc.terminal + "(3))")
		if err != nil {
			t.Fatal(tc.terminal, err)
		}
		typ := statements[0].(*CreateTable).Elements[0].Column.TypeName
		if typ.Schema == nil || typ.Schema.Text != "pg_catalog" || typ.Schema.Pos.Character != 25 || typ.Pos.Character != 25 || typ.Name != tc.name || typ.Quoted != tc.quoted || len(typ.Modifiers) != 1 || typ.Modifiers[0].Integer == nil || *typ.Modifiers[0].Integer != 3 || typ.Modifiers[0].Pos.Character != tc.precisionPosition {
			t.Fatalf("%s: %#v", tc.terminal, typ)
		}
	}
}
