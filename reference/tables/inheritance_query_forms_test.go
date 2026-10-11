package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func inheritanceQueryMapped(s *inheritanceCase) {
	s.exec("CREATE TABLE q(y BOOL,a INT)", 0)
	s.exec("CREATE TABLE p(a INT,x TEXT)", 0)
	s.exec("CREATE TABLE c(z BIGINT) INHERITS(q,p)", 0)
	s.exec("INSERT INTO p VALUES(1,'p')", 1)
	s.exec("INSERT INTO c VALUES(true,2,'c',9)", 1)
}

func TestReferenceInheritanceQueryForms(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		inheritanceQueryMapped(s)
		for _, from := range []string{"p", "p*", "public.p", `"public"."p"*`, "p AS r"} {
			s.values("SELECT * FROM "+from+" ORDER BY a", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "p"}, {int64(2), "c"}})
		}
		for _, from := range []string{"ONLY p", "ONLY(p)", "ONLY public.p AS r", `ONLY("public"."p") r`} {
			s.values("SELECT * FROM "+from, []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "p"}})
		}
		s.values("SELECT n,label FROM p* r(n,label) ORDER BY n", []string{"n", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "p"}, {int64(2), "c"}})
		step := fixture.Select("SELECT x,a FROM p WHERE a=$1", []string{"x", "a"}, []string{"TEXT", "INT4"}, [][]any{{"c", int64(2)}})
		step.Args = []any{2}
		s.run(step)
		for _, query := range []string{"SELECT * FROM p WHERE false", "SELECT * FROM p WHERE a=99"} {
			s.values(query, []string{"a", "x"}, []string{"INT4", "TEXT"}, nil)
		}
		s.exec("CREATE TABLE empty_parent(a INT,x TEXT)", 0)
		s.exec("CREATE TABLE empty_child() INHERITS(empty_parent)", 0)
		s.values("SELECT * FROM empty_parent", []string{"a", "x"}, []string{"INT4", "TEXT"}, nil)
	})
}

func TestReferenceInheritanceQueryDiagnostics(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		inheritanceQueryMapped(s)
		s.exec("CREATE TABLE keeper(a INT)", 0)
		s.exec("INSERT INTO keeper VALUES(91)", 1)
		for _, tc := range []struct{ query, code, message, token string }{
			{"SELECT z FROM p", "42703", `column "z" does not exist`, "z"},
			{"SELECT * FROM ONLY missing", "42P01", `relation "missing" does not exist`, "missing"},
			{"SELECT * FROM ONLY p*", "42601", `syntax error at or near "*"`, "*"},
			{"SELECT * FROM ONLY(p*)", "42601", `syntax error at or near "*"`, "*"},
			{"SELECT * FROM p* garbage garbage", "42601", `syntax error at or near "garbage"`, "garbage"},
			{"SELECT * FROM ONLY()", "42601", `syntax error at or near ")"`, ")"},
		} {
			position := strings.Index(tc.query, tc.token) + 1
			if tc.token == "*" || strings.HasSuffix(tc.query, "garbage garbage") {
				position = strings.LastIndex(tc.query, tc.token) + 1
			}
			step := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: strconv.Itoa(position)})
			step.SelectRows = true
			s.run(step)
		}
		// Whole-message parse rejection must precede a valid writing prefix.
		q := "INSERT INTO keeper VALUES(92); SELECT * FROM ONLY p*"
		s.fail(q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "*"`, Position: fmt.Sprint(strings.LastIndex(q, "*") + 1)})
		s.values("SELECT * FROM keeper", []string{"a"}, []string{"INT4"}, [][]any{{int64(91)}})
	})
}
