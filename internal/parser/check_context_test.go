package parser

import "testing"

func TestCheckSelectedScalarContext(t *testing.T) {
	for _, query := range []string{
		"CREATE TABLE t(a INT CHECK(abs(a)=1))",
		"CREATE TABLE t(a INT CHECK(pg_catalog.abs(a)=1))",
		"CREATE TABLE t(a INT CHECK(COALESCE(abs(a),1)=1))",
		"CREATE TABLE t(a INT CHECK(CASE WHEN a IS NULL THEN TRUE ELSE abs(CAST(a AS BIGINT))=1 END))",
		"CREATE TABLE t(a INT CHECK(num_nonnulls(a)=1 AND current_schemas(false) IS NOT NULL))",
		"CREATE TABLE t(a INT CHECK(random()>=0 AND random()<1))",
		"CREATE TABLE t(a INT CHECK(abs /* ) CHECK ( */ (a)=1) DEFAULT 1 NOT NULL,CHECK(a>0) NO INHERIT)",
		"CREATE TABLE t(a TEXT CHECK(a='CHECK ( abs( )'),b INT CHECK(abs(b)=1))",
		`CREATE TABLE "check"("abs" INT CHECK("abs"=1),"random" INT CHECK(random>0))`,
		"CREATE TABLE t(a INT CHECK((abs(a))=1));CREATE TABLE u(b INT DEFAULT 1 NOT NULL)",
	} {
		query := query
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			statements, err := Parse(query)
			if err != nil || len(statements) == 0 {
				t.Fatal(query, statements, err)
			}
		})
	}
}

func TestCheckSelectedScalarContextDoesNotLeak(t *testing.T) {
	for _, suffix := range []string{
		"CREATE TABLE u(a INT DEFAULT abs(1))",
		"SELECT abs(a)=1 FROM t",
		"SELECT a FROM t WHERE abs(a)=1",
		"UPDATE t SET a=abs(a)+1",
		"INSERT INTO t VALUES(abs(1))",
	} {
		query := "CREATE TABLE t(a INT CHECK(abs(a)=1));" + suffix
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()
			statements, err := Parse(query)
			if err == nil || statements != nil {
				t.Fatal("CHECK context escaped its closing parenthesis", statements, err)
			}
		})
	}
	for _, query := range []string{
		`CREATE TABLE "check"(a INT DEFAULT abs(1))`,
		"CREATE TABLE t(a INT CHECK(abs(a)=1) DEFAULT abs(1))",
		"CREATE TABLE t(a INT CHECK(abs(a)=1) NO INHERIT CHECK(abs(a)=2) DEFAULT abs(1))",
	} {
		statements, err := Parse(query)
		if err == nil || statements != nil {
			t.Fatal("quoted name or trailing DEFAULT activated CHECK context", query, statements, err)
		}
	}
}

func TestStringCoreDefaultSelectedFunctionBoundary(t *testing.T) {
	for _, name := range []string{
		"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible",
		"abs", "num_nulls",
	} {
		for _, prefix := range []string{"", "pg_catalog."} {
			query := "CREATE TABLE default_boundary(id BOOL DEFAULT " + prefix + name + "(23))"
			keywordDiagnostic(t, query, "0A000", "functions are unsupported", "47")
		}
	}
	// String calls have their own admitted scalar productions. The legacy
	// selected-function rejection must not capture their DEFAULT route.
	for _, query := range []string{
		"CREATE TABLE t(v TEXT DEFAULT btrim(' x '))",
		"CREATE TABLE t(v TEXT DEFAULT normalize('é',NFC))",
		"CREATE TABLE t(v TEXT DEFAULT 'x'||42)",
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
	}
}

func TestCheckASTIdentityAndModifiers(t *testing.T) {
	statements, err := Parse("CREATE TABLE t(a INT CONSTRAINT c CHECK(a=1) NO INHERIT NOT NULL,CONSTRAINT d CHECK(a>0))")
	if err != nil {
		t.Fatal(err)
	}
	table := statements[0].(*CreateTable)
	column := table.Elements[0].Column
	if len(column.Constraints) != 2 || column.Constraints[0].Check == nil || column.Constraints[0].NotNull || column.Constraints[0].Check.Name.Text != "c" || !column.Constraints[0].Check.NoInherit || !column.Constraints[1].NotNull {
		t.Fatal(column)
	}
	if table.Elements[1].Check == nil || table.Elements[1].NotNull != nil || table.Elements[1].Check.Name.Text != "d" || table.Elements[1].Check.NoInherit {
		t.Fatal(table.Elements[1])
	}
}

func TestCheckQualifiedColumnAST(t *testing.T) {
	statements, err := Parse("CREATE TABLE t(CONSTRAINT c CHECK(public.t.a<t.b),a INT DEFAULT 1,b INT)")
	if err != nil {
		t.Fatal(err)
	}
	check := statements[0].(*CreateTable).Elements[0].Check
	comparison, ok := check.Expression.(OperatorExpr)
	if !ok || len(comparison.Args) != 2 {
		t.Fatal(check.Expression)
	}
	for i, want := range [][]string{{"public", "t", "a"}, {"t", "b"}} {
		column, ok := comparison.Args[i].(ColumnExpr)
		if !ok || len(column.Name.Parts) != len(want) {
			t.Fatal(comparison.Args[i])
		}
		for j, part := range want {
			if column.Name.Parts[j].Text != part || column.Name.Parts[j].Pos.Character == 0 {
				t.Fatal(column)
			}
		}
		if column.QualificationPos.Character == 0 || len(want) == 3 && column.ExtraQualificationPos.Character == 0 {
			t.Fatal(column)
		}
	}
}

func TestCheckQualificationConsumerBoundaries(t *testing.T) {
	statements, err := Parse("SELECT public.t.a")
	if err != nil || len(statements) != 1 || statements[0].(*Select).From != nil {
		t.Fatal("lost no-FROM qualification", statements, err)
	}
	for _, query := range []string{
		"CREATE TABLE t(a INT DEFAULT public.t.a)",
		"DELETE FROM t WHERE public.t.a=1",
	} {
		statements, err := Parse(query)
		if err == nil || statements != nil {
			t.Fatal("CHECK widened ordinary qualification boundary", query, statements, err)
		}
	}
	statements, err = Parse("UPDATE t SET a=public.t.a")
	if err != nil || len(statements) != 1 {
		t.Fatal("UPDATE namespace lost schema qualification", statements, err)
	}
	column, ok := statements[0].(*Update).Assignments[0].Value.(ColumnExpr)
	if !ok || len(column.Name.Parts) != 3 || column.Name.Parts[0].Text != "public" || column.Name.Parts[1].Text != "t" || column.Name.Parts[2].Text != "a" {
		t.Fatal("UPDATE qualification AST", column)
	}
}
