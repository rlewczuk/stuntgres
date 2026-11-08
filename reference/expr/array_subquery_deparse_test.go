package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// arraySubqueryStructure asserts the deparse contract structurally: an ARRAY(
// construct whose body is a SELECT and no comparison-quantifier rendering.
// Canonical text is never asserted; the two backends may format differently.
func arraySubqueryStructure(t *testing.T, kind, definition string) {
	t.Helper()
	if strings.TrimSpace(definition) == "" {
		t.Fatalf("%s definition is empty", kind)
	}
	index := strings.Index(definition, "ARRAY(")
	if index < 0 {
		t.Fatalf("%s definition has no ARRAY(: %q", kind, definition)
	}
	if !strings.Contains(definition[index+len("ARRAY("):], "SELECT") {
		t.Fatalf("%s ARRAY() body has no SELECT: %q", kind, definition)
	}
	upper := strings.ToUpper(definition)
	for _, quantifier := range []string{" ANY ", " ALL ", "ANY (", "ALL ("} {
		if strings.Contains(upper, quantifier) {
			t.Fatalf("%s definition renders comparison quantifier %q: %q", kind, quantifier, definition)
		}
	}
}

// arraySubqueryValuesSource is the view-specific structural body witness.
func arraySubqueryValuesSource(t *testing.T, kind, definition string) {
	t.Helper()
	compact := strings.Join(strings.Fields(definition), " ")
	if !strings.Contains(compact, "VALUES (1), (2), (3)") {
		t.Fatalf("%s definition body has no VALUES (1), (2), (3): %q", kind, definition)
	}
}

// TestReferenceExprArraySubqueryDeparse proves the emitted definitions are
// executable, not canonical: each view definition is replayed and must return
// {1,2,3}; the rule action is witnessed by its insert and a rule-name replay.
// The final Dump/Reset/Restore phase is explicit native-only stored state.
func TestReferenceExprArraySubqueryDeparse(t *testing.T) {
	arraySubqueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := arraySubqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE arrq_src(id int)", 0))
		run(fixture.Exec("CREATE TABLE arrq_log(v int[])", 0))
		run(fixture.Exec("CREATE VIEW arrq_view AS SELECT ARRAY(SELECT x FROM (VALUES (1),(2),(3)) v(x)) AS a", 0))
		run(fixture.ArrayReferenceSelect("SELECT a FROM arrq_view", []string{"a"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{1,2,3}"}}, b.PG))

		for _, pretty := range []bool{false, true} {
			query, view := "SELECT pg_get_viewdef('arrq_view'::regclass) AS definition", "arrq_replay_default"
			if pretty {
				query, view = "SELECT pg_get_viewdef('arrq_view'::regclass, true) AS definition", "arrq_replay_pretty"
			}
			var definition string
			if err := c.QueryRowContext(p.Ctx, query).Scan(&definition); err != nil {
				t.Fatal(err)
			}
			arraySubqueryStructure(t, "view", definition)
			arraySubqueryValuesSource(t, "view", definition)
			run(fixture.Exec("CREATE VIEW "+view+" AS "+definition, 0))
			run(fixture.ArrayReferenceSelect("SELECT a FROM "+view, []string{"a"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{1,2,3}"}}, b.PG))
		}

		run(fixture.Exec("CREATE RULE arrq_rule AS ON INSERT TO arrq_src DO ALSO INSERT INTO arrq_log(v) VALUES (ARRAY(SELECT 1 WHERE NEW.id > 0))", 0))
		run(fixture.Exec("INSERT INTO arrq_src VALUES (1)", 1))
		run(fixture.ArrayReferenceSelect("SELECT v FROM arrq_log", []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{1}"}}, b.PG))
		var ruleDefinition string
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_ruledef(oid) FROM pg_rewrite WHERE rulename='arrq_rule'").Scan(&ruleDefinition); err != nil {
			t.Fatal(err)
		}
		arraySubqueryStructure(t, "rule", ruleDefinition)
		if strings.Contains(ruleDefinition, "arrq_rule") {
			replay := strings.Replace(ruleDefinition, "arrq_rule", "arrq_replay_rule", 1)
			run(fixture.Exec("DROP RULE arrq_rule ON arrq_src", 0))
			run(fixture.Exec(replay, 0))
			run(fixture.Exec("INSERT INTO arrq_src VALUES (2)", 1))
			run(fixture.ArrayReferenceSelect("SELECT v FROM arrq_log", []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{1}"}, {"{1}"}}, b.PG))
		}

		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		fresh, err := p.Native.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := fresh.Close(); err != nil {
				t.Error(err)
			}
		}()
		rows, err := fresh.QueryContext(p.Ctx, "SELECT a FROM arrq_view")
		if err != nil {
			t.Fatal(err)
		}
		fixture.ScalarRows(t, false, rows, []string{"a"}, []string{"_INT4"}, [][]any{{"{1,2,3}"}}, false)
	})
}
