package reference_test

import (
	"context"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArithmeticContextualRestore(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE d(f BOOL)", 0))
	const script = "CREATE TABLE d(f BOOL); INSERT INTO d VALUES(1+2)"
	want := fixture.ImplicitError(script, fixture.Diagnostic{Code: "42804", Message: `column "f" is of type boolean but expression is of type integer`, Hint: "You will need to rewrite or cast the expression.", Position: strconv.Itoa(strings.Index(script, "1+2") + 1)})
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), want.Diagnostic)
	step := fixture.Select("SELECT f FROM d", []string{"f"}, []string{"BOOL"}, nil)
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
}

func TestReferenceArithmeticLexicalRestore(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(i INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(7)", 1))
	const script = "CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES(1); INSERT INTO replacement VALUES(7%-1)"
	want := fixture.ImplicitError(script, fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer %- integer", Position: strconv.Itoa(strings.Index(script, "%") + 1), Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), want.Diagnostic)
	for _, step := range []fixture.Step{
		fixture.Select("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}),
		fixture.Select("SELECT i% -1 FROM t", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(0)}}),
	} {
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
}

func TestReferenceArithmeticSignedLiteralControls(t *testing.T) {
	p := fixture.NewScenario(t)
	const script = "CREATE TABLE n(v NUMERIC); INSERT INTO n VALUES(+1.5),(+9223372036854775808)"
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	step := fixture.Select("SELECT v FROM n ORDER BY v", []string{"v"}, []string{"NUMERIC"}, [][]any{{"1.5"}, {"9223372036854775808"}})
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, step)
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Prepared(step))
}

func TestReferenceArithmeticCastControls(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE dest(i INT,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO dest VALUES(CAST(7/2 AS int4),(-7%2)::int8)", 1))
	p.Step(t, fixture.Exec("CREATE TABLE d(v BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO d VALUES(9)", 1))
	held, err := p.Memory.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, p.MemoryRecord), "SELECT v+1 FROM d")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	const replacement = "CREATE TABLE d(v BIGINT); INSERT INTO d VALUES(CAST(7/2 AS bigint)+1); CREATE TABLE dest(i INT,b BIGINT); INSERT INTO dest VALUES(CAST(7/2 AS int4),(-7%2)::int8)"
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(replacement)); err != nil {
		t.Fatal(err)
	}
	fixture.ArithmeticRoundtrip(t, p, "d", "dest")
	for _, step := range []fixture.Step{
		fixture.Select("SELECT v FROM d", []string{"v"}, []string{"INT8"}, [][]any{{int64(4)}}),
		fixture.Select("SELECT i,b FROM dest", []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(-1)}}),
	} {
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, step)
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Prepared(step))
	}
	err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE d(v BIGINT); INSERT INTO d VALUES(CAST(2147483647+1 AS bigint))"))
	fixture.CheckDiagnostic(t, err, fixture.ImplicitError("", fixture.Diagnostic{Code: "22003", Message: "integer out of range"}), "restore")
	for _, prefix := range []string{"", "/*é*/ "} {
		position := "50"
		if prefix != "" {
			position = "56"
		}
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(prefix+"CREATE TABLE d(i INT); INSERT INTO d VALUES(CAST($1+1 AS int4));"))
		fixture.CheckDiagnostic(t, err, fixture.ImplicitError("", fixture.Diagnostic{Code: "42P02", Message: "parameters are not allowed in restore", Position: position}), "restore")
	}
	remaining := fixture.Select("SELECT v FROM d", []string{"v"}, []string{"INT8"}, [][]any{{int64(4)}})
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, remaining)
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Prepared(remaining))
	meta, err := held.ColumnTypes()
	if err != nil || len(meta) != 1 || meta[0].Name() != "?column?" || meta[0].DatabaseTypeName() != "INT8" {
		t.Fatal(meta, err)
	}
	if !held.Next() {
		t.Fatal(held.Err())
	}
	var old any
	if err := held.Scan(&old); err != nil || old != int64(10) {
		t.Fatal(old, err)
	}
	if held.Next() || held.Err() != nil {
		t.Fatal("held end", held.Err())
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	referenceArithmeticRecordedClosure(t, p.MemoryRecord, "SELECT v+1 FROM d", 1, 1)
}
