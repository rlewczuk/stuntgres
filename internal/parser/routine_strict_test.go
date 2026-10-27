package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func hasRoutineAttribute(d *CreateFunction, kind string) bool {
	for _, attribute := range d.Attributes {
		if attribute.Kind == kind {
			return true
		}
	}
	return false
}

func TestAggregateOrderedStrictSupportDeclaration(t *testing.T) {
	for _, suffix := range []string{
		"STRICT LANGUAGE SQL AS $$SELECT $1$$", "LANGUAGE SQL STRICT AS $$SELECT $1$$",
		"LANGUAGE SQL AS $$SELECT $1$$ STRICT", "STRICT AS $$SELECT $1$$ LANGUAGE SQL",
		"AS $$SELECT $1$$ STRICT LANGUAGE SQL", "AS $$SELECT $1$$ LANGUAGE SQL STRICT",
		"STRICT RETURN $1", "STRICT LANGUAGE SQL RETURN $1", "LANGUAGE SQL STRICT RETURN $1",
	} {
		statements, err := Parse("CREATE FUNCTION step(integer) RETURNS integer " + suffix)
		if err != nil {
			t.Fatal(suffix, err)
		}
		d := statements[0].(*CreateFunction)
		if !hasRoutineAttribute(d, "strict") || d.Language.Text != "sql" || d.Body != "SELECT $1" && d.Standard == nil {
			t.Fatal("strict support body/mode", suffix, d)
		}
	}
	statements, err := Parse("CREATE FUNCTION step(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$")
	if err != nil || len(statements[0].(*CreateFunction).Attributes) != 0 {
		t.Fatal("omitted attributes", statements, err)
	}
	if statements, err = Parse(`CREATE FUNCTION step(integer) RETURNS integer "strict" LANGUAGE SQL AS $$SELECT $1$$`); err == nil || len(statements) != 0 {
		t.Fatal("quoted attribute", statements, err)
	}
}

func TestAggregateOrderedVolatileSupportDeclaration(t *testing.T) {
	for _, suffix := range []string{
		"VOLATILE LANGUAGE SQL AS $$SELECT $1$$", "LANGUAGE SQL VOLATILE AS $$SELECT $1$$",
		"LANGUAGE SQL AS $$SELECT $1$$ VOLATILE", "VOLATILE AS $$SELECT $1$$ LANGUAGE SQL",
		"AS $$SELECT $1$$ VOLATILE LANGUAGE SQL", "AS $$SELECT $1$$ LANGUAGE SQL VOLATILE",
		"VOLATILE BEGIN ATOMIC SELECT $1; END", "STRICT VOLATILE LANGUAGE SQL AS $$SELECT $1$$", "VOLATILE STRICT LANGUAGE SQL AS $$SELECT $1$$",
	} {
		statements, err := Parse("CREATE FUNCTION step(integer) RETURNS integer " + suffix)
		if err != nil {
			t.Fatal(suffix, err)
		}
		d := statements[0].(*CreateFunction)
		if !hasRoutineAttribute(d, "volatile") || hasRoutineAttribute(d, "strict") != strings.Contains(suffix, "STRICT") || d.Language.Text != "sql" {
			t.Fatal("independent attributes", d)
		}
	}
}

func TestFunctionReplacementAttributes(t *testing.T) {
	for _, suffix := range []string{
		"STRICT VOLATILE LANGUAGE SQL AS $$SELECT $1$$", "LANGUAGE SQL STRICT VOLATILE AS $$SELECT $1$$",
		"LANGUAGE SQL AS $$SELECT $1$$ STRICT VOLATILE", "STRICT AS $$SELECT $1$$ VOLATILE LANGUAGE SQL",
		"AS $$SELECT $1$$ STRICT LANGUAGE SQL VOLATILE", "AS $$SELECT $1$$ LANGUAGE SQL VOLATILE STRICT",
		"STRICT VOLATILE RETURN $1", "STRICT LANGUAGE SQL VOLATILE RETURN $1", "VOLATILE LANGUAGE SQL STRICT BEGIN ATOMIC SELECT $1; END",
	} {
		statements, err := Parse("CREATE OR REPLACE FUNCTION step(integer) RETURNS integer " + suffix)
		if err != nil {
			t.Fatal(suffix, err)
		}
		d := statements[0].(*CreateFunction)
		if !d.Replace || !hasRoutineAttribute(d, "strict") || !hasRoutineAttribute(d, "volatile") || d.Language.Text != "sql" || d.Body != "SELECT $1" && d.Standard == nil {
			t.Fatal("replacement/body", suffix, d)
		}
	}
	statements, err := Parse("CREATE OR REPLACE FUNCTION event_fn() RETURNS event_trigger STRICT VOLATILE LANGUAGE plpgsql AS $$BEGIN RETURN; END$$")
	if err != nil {
		t.Fatal(err)
	}
	d := statements[0].(*CreateFunction)
	if !d.Replace || !hasRoutineAttribute(d, "strict") || !hasRoutineAttribute(d, "volatile") || d.Language.Text != "plpgsql" || d.Body != "BEGIN RETURN; END" {
		t.Fatal("event replacement", d)
	}
}

func TestRoutineAttributeSyntax(t *testing.T) {
	for _, kind := range []string{"STRICT", "VOLATILE"} {
		query := "/*é*/ CREATE OR REPLACE FUNCTION step(integer) RETURNS integer " + kind + " LANGUAGE SQL AS $$SELECT $1$$ " + kind
		statements, err := Parse(query)
		if err != nil {
			t.Fatal("semantic duplicate rejected during parse", err)
		}
		attributes := statements[0].(*CreateFunction).Attributes
		at := strings.LastIndex(query, kind)
		if len(attributes) != 2 || attributes[1].Kind != strings.ToLower(kind) || attributes[1].Pos != (Position{Offset: at, Character: utf8.RuneCountInString(query[:at]) + 1}) {
			t.Fatal("duplicate ordered positions", attributes)
		}
	}
	query := "ALTER FUNCTION f(integer) IMMUTABLE CALLED ON NULL INPUT SECURITY DEFINER PARALLEL RESTRICTED COST -2 ROWS +3 SET work_mem FROM CURRENT RESET ALL SET search_path TO public, pg_catalog"
	statements, err := Parse(query)
	if err != nil {
		t.Fatal(err)
	}
	a := statements[0].(*AlterRoutine)
	want := []string{"immutable", "strict", "security", "parallel", "cost", "rows", "set", "resetall", "set"}
	if a.Action != "attributes" || len(a.Attributes) != len(want) {
		t.Fatal(a)
	}
	for i, kind := range want {
		if a.Attributes[i].Kind != kind || i > 0 && a.Attributes[i].Pos.Character <= a.Attributes[i-1].Pos.Character {
			t.Fatal("order", a.Attributes)
		}
	}
	if !a.Attributes[6].Setting.FromCurrent || a.Attributes[4].Value != "-2" || a.Attributes[8].Setting.Values[1] != "pg_catalog" {
		t.Fatal("values", a.Attributes)
	}
	for _, suffix := range []string{" COST", " SET x FROM", " PARALLEL", " STRICT garbage"} {
		if statements, err := Parse("CREATE FUNCTION f() RETURNS integer LANGUAGE SQL AS 'SELECT 1'" + suffix); err == nil || len(statements) != 0 {
			t.Fatal("whole input accepted", suffix, statements, err)
		}
	}
}
