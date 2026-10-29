package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func ivScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder)) {
	fixture.RegtypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		body(t, p, b, c, r)
	})
}
func ivBoth(t *testing.T, p *fixture.Pair, c fixture.Executor, r *fixture.Recorder, s fixture.Step) {
	t.Helper()
	fixture.RegtypeBoth(t, p, c, r, s)
}
func ivBool(value, target string, want any) fixture.Step {
	return fixture.Select("SELECT pg_input_is_valid("+fixture.RegtypeLiteral(value)+","+fixture.RegtypeLiteral(target)+")", []string{"pg_input_is_valid"}, []string{"BOOL"}, [][]any{{want}})
}
func ivInfo(value, target string, row []any) fixture.Step {
	return fixture.Select("SELECT * FROM pg_input_error_info("+fixture.RegtypeLiteral(value)+","+fixture.RegtypeLiteral(target)+")", []string{"message", "detail", "hint", "sql_error_code"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{row})
}
func ivValueSteps() []fixture.Step {
	return []fixture.Step{
		ivBool("42", "integer", true), ivBool("oops", "integer", false), ivBool("42000000000", "integer", false),
		ivInfo("oops", "integer", []any{`invalid input syntax for type integer: "oops"`, nil, nil, "22P02"}),
		ivInfo("42000000000", "integer", []any{`value "42000000000" is out of range for type integer`, nil, nil, "22003"}),
		ivInfo("42", "integer", []any{nil, nil, nil, nil}),
		ivBool("true", "boolean", true), ivBool("nonsense", "boolean", false), ivBool("anything", "text", true), ivBool("42", "pg_catalog.int4", true),
		ivInfo("true", "boolean", []any{nil, nil, nil, nil}),
		ivInfo("nonsense", "boolean", []any{`invalid input syntax for type boolean: "nonsense"`, nil, nil, "22P02"}),
		ivInfo("anything", "text", []any{nil, nil, nil, nil}),
		ivInfo("42", "pg_catalog.int4", []any{nil, nil, nil, nil}),
		ivBool("{1,2,NULL}", "integer[]", true), ivBool("{1,oops}", "integer[]", false),
		ivInfo("{1,oops}", "integer[]", []any{`invalid input syntax for type integer: "oops"`, nil, nil, "22P02"}),
	}
}
func ivTypmodSteps() []fixture.Step {
	return []fixture.Step{
		ivBool("1234.567", "numeric(8,4)", true), ivBool("1234.567", "numeric(7,4)", false),
		ivInfo("1234.567", "numeric(8,4)", []any{nil, nil, nil, nil}),
		ivInfo("1234.567", "numeric(7,4)", []any{"numeric field overflow", "A field with precision 7, scale 4 must round to an absolute value less than 10^3.", nil, "22003"}),
		ivInfo("bogus", "aclitem", []any{`unrecognized key word: "bogus"`, nil, `ACL key word must be "group" or "user".`, "22P02"}),
		ivBool("{abc,def}", "varchar(3)[]", true),
		ivInfo("{abc,def}", "varchar(3)[]", []any{nil, nil, nil, nil}),
	}
}
func ivNullSteps() []fixture.Step {
	return []fixture.Step{
		fixture.Select("SELECT pg_input_error_info(NULL,'__iv_missing__')::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}),
		fixture.Select("SELECT pg_input_is_valid(NULL,'integer') AS a,pg_input_is_valid('42',NULL) AS b,pg_input_is_valid(NULL,'__iv_missing__') AS c", []string{"a", "b", "c"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{nil, nil, nil}}),
		fixture.Select("SELECT pg_input_error_info('42','integer')::text AS a,pg_input_error_info(NULL,'integer')::text AS b,pg_input_error_info('42',NULL)::text AS c,pg_input_error_info(NULL,NULL)::text AS d", []string{"a", "b", "c", "d"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"(,,,)", nil, nil, nil}}),
		fixture.Select("SELECT * FROM pg_input_error_info(NULL,'integer')", []string{"message", "detail", "hint", "sql_error_code"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{nil, nil, nil, nil}}),
		fixture.Select("SELECT * FROM pg_input_is_valid('42','integer')", []string{"pg_input_is_valid"}, []string{"BOOL"}, [][]any{{true}}),
		fixture.Select("SELECT * FROM pg_input_is_valid(NULL,'integer')", []string{"pg_input_is_valid"}, []string{"BOOL"}, [][]any{{nil}}),
		fixture.Select("SELECT pg_input_error_info('42','integer'),pg_input_error_info(NULL,'integer')", []string{"pg_input_error_info", "pg_input_error_info"}, []string{"RECORD", "RECORD"}, [][]any{{"(,,,)", nil}}),
		fixture.Select("SELECT (pg_input_error_info('oops','integer')).message AS message,(pg_input_error_info('oops','integer')).sql_error_code AS code", []string{"message", "code"}, []string{"TEXT", "TEXT"}, [][]any{{`invalid input syntax for type integer: "oops"`, "22P02"}}),
	}
}
func TestReferenceInputValidityValues(t *testing.T) {
	ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		for _, s := range ivValueSteps() {
			ivBoth(t, p, c, r, s)
		}
	})
}
func TestReferenceInputValidityNullRecords(t *testing.T) {
	ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		for _, s := range ivNullSteps() {
			ivBoth(t, p, c, r, s)
		}
	})
}
func ivHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	const q = "SELECT pg_input_is_valid($1,$2) AS ok"
	held, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, f := range []struct {
		value, target any
		want          any
	}{{"42", "integer", true}, {"oops", "integer", false}, {"1234.567", "numeric(8,4)", true}, {"1234.567", "numeric(7,4)", false}, {"42", "integer", true}, {nil, "__iv_missing__", nil}} {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, r, fixture.Select(q, []string{"ok"}, []string{"BOOL"}, [][]any{{f.want}}, f.value, f.target))
	}
	const info = "SELECT * FROM pg_input_error_info($1,$2)"
	statement, err := c.PrepareContext(p.Ctx, info)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	for _, s := range []fixture.Step{ivInfo("bogus", "aclitem", []any{`unrecognized key word: "bogus"`, nil, `ACL key word must be "group" or "user".`, "22P02"}), ivInfo("1234.567", "numeric(7,4)", []any{"numeric field overflow", "A field with precision 7, scale 4 must round to an absolute value less than 10^3.", nil, "22003"}), ivInfo("42", "integer", []any{nil, nil, nil, nil})} {
		args := []any{"42", "integer"}
		if s.Rows[0][3] == "22P02" {
			args = []any{"bogus", "aclitem"}
		}
		if s.Rows[0][3] == "22003" {
			args = []any{"1234.567", "numeric(7,4)"}
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: statement, SQL: info}, r, fixture.Select(info, s.Columns, s.Types, s.Rows, args...))
	}
}
func TestReferenceInputValidityTypmods(t *testing.T) {
	ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		for _, s := range ivTypmodSteps() {
			ivBoth(t, p, c, r, s)
		}
		ivHeld(t, p, c, r)
	})
}
func TestReferenceInputValiditySmoke(t *testing.T) {
	ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		for _, group := range [][]fixture.Step{ivValueSteps(), ivTypmodSteps(), ivNullSteps()} {
			for _, s := range group {
				ivBoth(t, p, c, r, s)
			}
		}
		ivHeld(t, p, c, r)
		ivHardBasics(t, p, c)
	})
}
