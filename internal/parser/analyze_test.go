package parser

import (
	"strings"
	"testing"
)

func TestAnalyzeGrammar(t *testing.T) {
	for _, q := range []string{
		"ANALYZE", "ANALYSE;", "ANALYZE VERBOSE", "ANALYSE VERBOSE t",
		"ANALYZE (VERBOSE,SKIP_LOCKED) t",
		"ANALYZE (VERBOSE TRUE,VERBOSE FALSE,SKIP_LOCKED ON,SKIP_LOCKED OFF) t",
		"ANALYZE (VERBOSE 1,SKIP_LOCKED 0,BUFFER_USAGE_LIMIT '128kB') ONLY s.t *(a,b),u(c)",
		"ANALYZE (BUFFER_USAGE_LIMIT -1) t",
		"ANALYZE (WHAT true) t",
		"ANALYZE ONLY t", "ANALYZE t*", "ANALYZE ONLY t*",
		"ANALYZE \"s\".\"T\"(\"A\",b),u",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatalf("%s: %#v %v", q, statements, err)
		}
		if _, ok := statements[0].(*Analyze); !ok {
			t.Fatalf("%s: %T", q, statements[0])
		}
	}
}

func TestAnalyzePositionsAndOrder(t *testing.T) {
	q := "/*é*/ ANALYSE (VERBOSE false,VERBOSE true,SKIP_LOCKED ON,BUFFER_USAGE_LIMIT '128kB') ONLY \"s\".\"T\" *(\"A\",b),u(c)"
	statements, err := Parse(q)
	if err != nil {
		t.Fatal(err)
	}
	a := statements[0].(*Analyze)
	if a.Pos.Character != 7 || len(a.Options) != 4 || len(a.Targets) != 2 {
		t.Fatalf("positions/ordered syntax: %#v", a)
	}
	for i, name := range []string{"verbose", "verbose", "skip_locked", "buffer_usage_limit"} {
		o := a.Options[i]
		if o.Name.Text != name || !o.HasValue || o.Name.Pos.Character <= a.Pos.Character || o.Value.Pos.Character <= o.Name.Pos.Character {
			t.Fatalf("option %d: %#v", i, o)
		}
	}
	if a.Options[0].Value.Boolean || !a.Options[1].Value.Boolean || a.Options[3].Value.Text != "128kB" {
		t.Fatalf("option values lost: %#v", a.Options)
	}
	first, second := a.Targets[0], a.Targets[1]
	if !first.Only || !first.Star || len(first.Name.Parts) != 2 || first.Name.Parts[0].Text != "s" || first.Name.Parts[1].Text != "T" || len(first.Columns) != 2 || first.Columns[0].Text != "A" || first.Columns[1].Text != "b" {
		t.Fatalf("first target: %#v", first)
	}
	if second.Only || second.Star || second.Name.Parts[0].Text != "u" || len(second.Columns) != 1 || second.Columns[0].Text != "c" || second.Name.Parts[0].Pos.Character <= first.Columns[1].Pos.Character {
		t.Fatalf("second target: %#v", second)
	}
	for _, tc := range []struct {
		q        string
		position int
	}{{"ANALYZE VERBOSE t", 9}, {"ANALYSE (VERBOSE) t", 10}} {
		statements, err := Parse(tc.q)
		if err != nil {
			t.Fatal(tc.q, err)
		}
		o := statements[0].(*Analyze).Options[0]
		if o.HasValue || o.Value.Kind != BooleanValue || !o.Value.Boolean || o.Name.Pos.Character != tc.position {
			t.Fatalf("implicit Boolean: %s: %#v", tc.q, o)
		}
	}
}

func TestAnalyzeWholeInput(t *testing.T) {
	for _, q := range []string{
		"ANALYZE ()", "ANALYZE (VERBOSE,)", "ANALYZE (VERBOSE true false)",
		"ANALYZE t()", "ANALYZE t(a,)", "ANALYZE t(a+1)", "ANALYZE t(a.b)",
		"ANALYZE t,", "ANALYZE ONLY", "ANALYZE t suffix", "ANALYZE t; ANALYZE u suffix",
		"ANALYZE VERBOSE VERBOSE t", "ANALYZE VERBOSE (SKIP_LOCKED) t", "ANALYZE FULL t",
		"PREPARE p AS ANALYZE t", "CREATE ANALYZE",
	} {
		if statements, err := Parse(q); err == nil || statements != nil {
			t.Fatalf("accepted malformed whole input %q: %#v %v", q, statements, err)
		}
	}
	for _, q := range []string{"ANALYZE t; ANALYSE u", "SELECT 1 AS analyze", "SELECT 1 AS analyse", "CREATE TABLE \"analyze\"(a int)"} {
		statements, err := Parse(q)
		if err != nil || len(statements) == 0 {
			t.Fatalf("valid neighboring syntax %s: %#v %v", q, statements, err)
		}
		if strings.Contains(q, ";") && len(statements) != 2 {
			t.Fatalf("lost statement: %#v", statements)
		}
	}
}
