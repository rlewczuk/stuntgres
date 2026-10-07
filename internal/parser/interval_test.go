package parser

import (
	"errors"
	"strconv"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestIntervalDeclarationGrammar(t *testing.T) {
	for _, tc := range []struct {
		fields string
		mask   int32
	}{{"YEAR", 4}, {"MONTH", 2}, {"DAY", 8}, {"HOUR", 1024}, {"MINUTE", 2048}, {"SECOND", 4096}, {"YEAR TO MONTH", 6}, {"DAY TO HOUR", 1032}, {"DAY TO MINUTE", 3080}, {"DAY TO SECOND", 7176}, {"HOUR TO MINUTE", 3072}, {"HOUR TO SECOND", 7168}, {"MINUTE TO SECOND", 6144}} {
		q := "CREATE TABLE iv(v INTERVAL " + tc.fields + ")"
		statements, e := Parse(q)
		if e != nil {
			t.Fatal(q, e)
		}
		raw := statements[0].(*CreateTable).Elements[0].Column.TypeName
		if raw.Name != "interval" || len(raw.Modifiers) != 1 || *raw.Modifiers[0].Integer != tc.mask {
			t.Fatal(raw)
		}
		statements, e = Parse("SELECT INTERVAL '1' " + tc.fields + " FROM iv")
		if e != nil {
			t.Fatal(e)
		}
		raw = statements[0].(*Select).Targets[0].(TypedLiteralExpr).TypeName
		if *raw.Modifiers[0].Integer != tc.mask {
			t.Fatal(raw)
		}
	}
	for _, decl := range []string{"INTERVAL", "INTERVAL(2)", "INTERVAL SECOND(2)", "INTERVAL DAY TO SECOND(2)", "INTERVAL HOUR TO SECOND(2)", "INTERVAL MINUTE TO SECOND(2)", `"interval"(32767,2)`} {
		if _, e := Parse("CREATE TABLE iv(v " + decl + ")"); e != nil {
			t.Fatal(decl, e)
		}
	}
	for _, q := range []string{"SELECT INTERVAL(2) '1' FROM iv", "SELECT INTERVAL '1' MINUTE TO SECOND(2) FROM iv", "CREATE TABLE year(year INTERVAL,month INTERVAL,day INTERVAL,hour INTERVAL,minute INTERVAL,second INTERVAL)", "SELECT year,month,day,hour,minute,second FROM year"} {
		if _, e := Parse(q); e != nil {
			t.Fatal(q, e)
		}
	}
}
func TestIntervalSyntaxDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		q, token string
		position int
	}{{"CREATE TABLE ivbad(v INTERVAL MONTH TO DAY)", "TO", 37}, {"CREATE TABLE ivbad(v INTERVAL(-1))", "-", 31}, {"CREATE TABLE ivbad(v INTERVAL YEAR(2))", "(", 35}, {"CREATE TABLE ivbad(v INTERVAL(2) DAY)", "DAY", 34}, {"CREATE TABLE ivbad(v INTERVAL(1.5))", "1.5", 31}, {"CREATE TABLE ivbad(v INTERVAL(+1))", "+", 31}} {
		_, e := Parse(tc.q)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "syntax error at or near \"" + tc.token + "\"", Position: strconv.Itoa(tc.position)}
		if !errors.As(e, &got) || *got != want {
			t.Fatal(tc.q, e, want)
		}
	}
}

func TestIntervalLiteralWarningPosition(t *testing.T) {
	for _, q := range []string{"SELECT INTERVAL(7) '1 second' FROM iv", "SELECT INTERVAL '1 second' SECOND(7) FROM iv"} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		raw := statements[0].(*Select).Targets[0].(TypedLiteralExpr).TypeName
		if raw.Pos.Character != 8 {
			t.Fatal(raw.Pos)
		}
	}
	for _, q := range []string{"CREATE TABLE ivp(v INTERVAL(7))", "CREATE TABLE ivp(v INTERVAL SECOND(7))"} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		raw := statements[0].(*CreateTable).Elements[0].Column.TypeName
		if raw.Pos.Character != 20 {
			t.Fatal(raw.Pos)
		}
	}
}
