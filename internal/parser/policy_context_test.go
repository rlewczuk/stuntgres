package parser

import "testing"

func TestPolicySelectedScalarContext(t *testing.T) {
	for _, query := range []string{
		"CREATE POLICY p ON t USING(random()>=0)",
		"CREATE POLICY p ON t WITH CHECK(random()>=0)",
		"ALTER POLICY p ON t USING(pg_catalog.random()>=0) WITH CHECK(random()<1)",
		"CREATE POLICY p ON t USING(COALESCE(random(),0)>=0 AND abs(id)>0)",
		"CREATE POLICY p ON t USING(EXISTS(SELECT 1 WHERE random()>=0))",
	} {
		t.Run(query, func(t *testing.T) {
			statements, err := Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
		})
	}
	statements, err := Parse("CREATE POLICY p ON t USING(random()>=0)")
	if err != nil {
		t.Fatal(err)
	}
	condition := statements[0].(*CreatePolicy).Using
	comparison, ok := condition.(OperatorExpr)
	if !ok || len(comparison.Name.Parts) != 1 || comparison.Name.Parts[0].Text != ">=" || len(comparison.Args) != 2 {
		t.Fatal("lost ordinary comparison AST", condition)
	}
	call, ok := comparison.Args[0].(FunctionExpr)
	if !ok || len(call.Name.Parts) != 1 || call.Name.Parts[0].Text != "random" || len(call.Args) != 0 {
		t.Fatal("lost ordinary function AST", comparison.Args[0])
	}
}

func TestPolicySelectedScalarContextDoesNotLeak(t *testing.T) {
	for _, head := range []string{
		"CREATE POLICY p ON t USING(random()>=0);",
		"CREATE POLICY p ON t WITH CHECK(random()>=0);",
		"ALTER POLICY p ON t USING(EXISTS(SELECT 1 WHERE random()>=0));",
	} {
		for _, suffix := range []string{
			"CREATE TABLE u(id INTEGER DEFAULT abs(1))",
		} {
			t.Run(head+suffix, func(t *testing.T) {
				statements, err := Parse(head + suffix)
				if err == nil || statements != nil {
					t.Fatal("policy context escaped its closing parenthesis", statements, err)
				}
			})
		}
	}
}
