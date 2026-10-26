package parser

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestAggregateOrderedStrictSupportDeclaration(t *testing.T) {
	for _, suffix := range []string{
		"STRICT LANGUAGE SQL AS $$SELECT $1$$",
		"LANGUAGE SQL STRICT AS $$SELECT $1$$",
		"LANGUAGE SQL AS $$SELECT $1$$ STRICT",
		"STRICT AS $$SELECT $1$$ LANGUAGE SQL",
		"AS $$SELECT $1$$ STRICT LANGUAGE SQL",
		"AS $$SELECT $1$$ LANGUAGE SQL STRICT",
		"STRICT RETURN $1",
		"STRICT LANGUAGE SQL RETURN $1",
		"LANGUAGE SQL STRICT RETURN $1",
	} {
		statements, err := Parse("CREATE FUNCTION step(integer) RETURNS integer " + suffix)
		if err != nil {
			t.Fatal(suffix, err)
		}
		declaration := statements[0].(*CreateFunction)
		if !declaration.Strict || declaration.Language.Text != "sql" || declaration.Body != "SELECT $1" && declaration.Standard == nil {
			t.Fatal("strict support declaration lost its body or mode", suffix, declaration)
		}
	}
	statements, err := Parse("CREATE FUNCTION step(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$")
	if err != nil || statements[0].(*CreateFunction).Strict {
		t.Fatal("omitted strictness changed default", statements, err)
	}
	query := "/*é*/ CREATE FUNCTION step(integer) RETURNS integer STRICT LANGUAGE SQL STRICT AS $$SELECT $1$$"
	_, err = Parse(query)
	var diagnostic *sqlstate.Error
	position := strconv.Itoa(utf8.RuneCountInString(query[:strings.LastIndex(query, "STRICT")]) + 1)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != "conflicting or redundant options" || diagnostic.Position != position {
		t.Fatal("duplicate strictness diagnostic", diagnostic, err)
	}
	if statements, err = Parse(`CREATE FUNCTION step(integer) RETURNS integer "strict" LANGUAGE SQL AS $$SELECT $1$$`); err == nil || len(statements) != 0 {
		t.Fatal("quoted name admitted as strictness clause", statements, err)
	}
}

func TestAggregateOrderedVolatileSupportDeclaration(t *testing.T) {
	for _, tc := range []struct {
		suffix string
		strict bool
	}{
		{"VOLATILE LANGUAGE SQL AS $$SELECT $1$$", false},
		{"LANGUAGE SQL VOLATILE AS $$SELECT $1$$", false},
		{"LANGUAGE SQL AS $$SELECT $1$$ VOLATILE", false},
		{"VOLATILE AS $$SELECT $1$$ LANGUAGE SQL", false},
		{"AS $$SELECT $1$$ VOLATILE LANGUAGE SQL", false},
		{"AS $$SELECT $1$$ LANGUAGE SQL VOLATILE", false},
		{"VOLATILE BEGIN ATOMIC SELECT $1; END", false},
		{"STRICT VOLATILE LANGUAGE SQL AS $$SELECT $1$$", true},
		{"VOLATILE STRICT LANGUAGE SQL AS $$SELECT $1$$", true},
	} {
		statements, err := Parse("CREATE FUNCTION step(integer) RETURNS integer " + tc.suffix)
		if err != nil {
			t.Fatal(tc.suffix, err)
		}
		declaration := statements[0].(*CreateFunction)
		if declaration.Volatility != "volatile" || declaration.Strict != tc.strict || declaration.Language.Text != "sql" {
			t.Fatal("function attributes lost or conflated", declaration)
		}
	}
	query := "CREATE FUNCTION step(integer) RETURNS integer VOLATILE LANGUAGE SQL AS $$SELECT $1$$ VOLATILE"
	_, err := Parse(query)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != "conflicting or redundant options" {
		t.Fatal("duplicate volatility accepted", diagnostic, err)
	}
}

func TestFunctionReplacementAttributes(t *testing.T) {
	for _, suffix := range []string{
		"STRICT VOLATILE LANGUAGE SQL AS $$SELECT $1$$",
		"LANGUAGE SQL STRICT VOLATILE AS $$SELECT $1$$",
		"LANGUAGE SQL AS $$SELECT $1$$ STRICT VOLATILE",
		"STRICT AS $$SELECT $1$$ VOLATILE LANGUAGE SQL",
		"AS $$SELECT $1$$ STRICT LANGUAGE SQL VOLATILE",
		"AS $$SELECT $1$$ LANGUAGE SQL VOLATILE STRICT",
		"STRICT VOLATILE RETURN $1",
		"STRICT LANGUAGE SQL VOLATILE RETURN $1",
		"VOLATILE LANGUAGE SQL STRICT BEGIN ATOMIC SELECT $1; END",
	} {
		statements, err := Parse("CREATE OR REPLACE FUNCTION step(integer) RETURNS integer " + suffix)
		if err != nil {
			t.Fatal(suffix, err)
		}
		declaration := statements[0].(*CreateFunction)
		if !declaration.Replace || !declaration.Strict || declaration.Volatility != "volatile" || declaration.Language.Text != "sql" || declaration.Body != "SELECT $1" && declaration.Standard == nil {
			t.Fatal("replacement lost declaration attributes or body", suffix, declaration)
		}
	}
	statements, err := Parse("CREATE OR REPLACE FUNCTION event_fn() RETURNS event_trigger STRICT VOLATILE LANGUAGE plpgsql AS $$BEGIN RETURN; END$$")
	if err != nil {
		t.Fatal(err)
	}
	event := statements[0].(*CreateFunction)
	if !event.Replace || !event.Strict || event.Volatility != "volatile" || event.Language.Text != "plpgsql" || event.Body != "BEGIN RETURN; END" {
		t.Fatal("event replacement lost independent declaration attributes", event)
	}
	for _, attribute := range []string{"STRICT", "VOLATILE"} {
		query := "/*é*/ CREATE OR REPLACE FUNCTION step(integer) RETURNS integer " + attribute + " LANGUAGE SQL AS $$SELECT $1$$ " + attribute
		_, err := Parse(query)
		var diagnostic *sqlstate.Error
		position := strconv.Itoa(utf8.RuneCountInString(query[:strings.LastIndex(query, attribute)]) + 1)
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != "conflicting or redundant options" || diagnostic.Position != position {
			t.Fatal("replacement lost duplicate attribute diagnostic", attribute, diagnostic, err)
		}
	}
}
