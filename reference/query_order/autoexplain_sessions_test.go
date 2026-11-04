package reference_test

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAutoExplainNested(t *testing.T) {
	autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE ae_nested(id integer)", 0))
		run(fixture.Exec("INSERT INTO ae_nested VALUES(1),(2)", 2))
		run(fixture.Exec("CREATE TABLE ae_nested_text(v text)", 0))
		run(fixture.Exec("INSERT INTO ae_nested_text VALUES('éx'),('a''b'),(NULL),('')", 4))
		run(fixture.Exec("CREATE TYPE ae_nested_record AS (v text)", 0))
		run(fixture.Exec("CREATE FUNCTION ae_nested_fn(key integer) RETURNS integer LANGUAGE plpgsql AS $$ DECLARE answer integer; BEGIN SELECT id INTO answer FROM ae_nested WHERE id=key; RETURN answer; END $$", 0))
		run(fixture.Exec("CREATE FUNCTION ae_nested_local(key text) RETURNS text LANGUAGE plpgsql AS $$ DECLARE current_key text := 'stale'; answer text; BEGIN current_key := key; SELECT v INTO answer FROM ae_nested_text WHERE v IS NOT DISTINCT FROM current_key; RETURN answer; END $$", 0))
		run(fixture.Exec("CREATE FUNCTION ae_nested_field(key text) RETURNS text LANGUAGE plpgsql AS $$ DECLARE current_record record; answer text; BEGIN current_record := ROW(key)::ae_nested_record; SELECT v INTO answer FROM ae_nested_text WHERE v IS NOT DISTINCT FROM current_record.v; RETURN answer; END $$", 0))
		autoExplainConfigure(t, p, b, c, notices)
		for _, test := range []struct {
			name, resultType, innerSource, signature string
			values                                   []any
			results                                  []any
			innerRows                                []float64
			literals                                 []string
		}{
			{
				name: "ae_nested_fn", resultType: "INT4",
				innerSource: "SELECT id             FROM ae_nested WHERE id=key",
				signature:   "ae_nested_fn(integer)",
				values:      []any{int64(2), int64(1), nil, int64(99), int64(2)},
				results:     []any{int64(2), int64(1), nil, nil, int64(2)},
				innerRows:   []float64{1, 1, 0, 0, 1},
				literals:    []string{"2", "1", "NULL::integer", "99", "2"},
			},
			{
				name: "ae_nested_local", resultType: "TEXT",
				innerSource: "SELECT v             FROM ae_nested_text WHERE v IS NOT DISTINCT FROM current_key",
				signature:   "ae_nested_local(text)",
				values:      []any{"éx", "a'b", nil, "", "éx"},
				results:     []any{"éx", "a'b", nil, "", "éx"},
				innerRows:   []float64{1, 1, 1, 1, 1},
				literals:    []string{"'éx'::text", "'a''b'::text", "NULL::text", "''::text", "'éx'::text"},
			},
			{
				name: "ae_nested_field", resultType: "TEXT",
				innerSource: "SELECT v             FROM ae_nested_text WHERE v IS NOT DISTINCT FROM current_record.v",
				signature:   "ae_nested_field(text)",
				values:      []any{"a'b", nil, "éx", "", "a'b"},
				results:     []any{"a'b", nil, "éx", "", "a'b"},
				innerRows:   []float64{1, 1, 1, 1, 1},
				literals:    []string{`('(a''b)'::ae_nested_record).v`, `('()'::ae_nested_record).v`, `('(éx)'::ae_nested_record).v`, `('("")'::ae_nested_record).v`, `('(a''b)'::ae_nested_record).v`},
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				query := "SELECT " + test.name + "($1) AS v"
				held, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				autoExplainQuiet(t, notices)
				for _, nested := range []string{"off", "on"} {
					run(fixture.Exec("SET auto_explain.log_nested_statements = "+nested, 0))
					for _, rate := range []string{"0", "1"} {
						run(fixture.Exec("SET auto_explain.sample_rate = "+rate, 0))
						for i, value := range test.values {
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: query}, b.Recorder, fixture.Select(query, []string{"v"}, []string{test.resultType}, [][]any{{test.results[i]}}, value))
							if rate == "0" {
								autoExplainQuiet(t, notices)
								continue
							}
							literal := ""
							if !b.PG {
								literal = test.literals[i]
							}
							autoExplainNestedPlans(t, notices, query, test.innerSource, test.signature, nested == "on", test.innerRows[i], literal)
						}
					}
				}
			})
		}
	})
}

// Preserve the exact executed-source padding and full routine/SPI diagnostic
// Context; presentation must not normalize away either independently checked field.
func autoExplainNestedPlans(t *testing.T, notices *[]fixture.Diagnostic, query, innerSource, signature string, nested bool, innerRows float64, literal string) {
	t.Helper()
	want := 1
	if nested {
		want = 2
	}
	if len(*notices) != want {
		t.Fatalf("nested=%t plans=%#v", nested, *notices)
	}
	outer, inner := 0, 0
	for len(*notices) != 0 {
		one := []fixture.Diagnostic{(*notices)[0]}
		*notices = (*notices)[1:]
		body := strings.TrimPrefix(one[0].Message, autoExplainDuration.FindString(one[0].Message))
		var document map[string]any
		if err := json.Unmarshal([]byte(body), &document); err != nil {
			t.Fatal(err)
		}
		source, ok := document["Query Text"].(string)
		if !ok {
			t.Fatal("nested source absent", document)
		}
		context, rows := "", float64(1)
		switch source {
		case query:
			outer++
		case innerSource:
			context = "SQL statement \"" + innerSource + "\"\nPL/pgSQL function " + signature + " line 1 at SQL statement"
			rows = innerRows
			inner++
		default:
			t.Fatal("wrong actual nested source", source)
		}
		autoExplainMessage(t, &one, "NOTICE", context)
		root, ok := document["Plan"].(map[string]any)
		if !ok || root["Actual Rows"] != rows || root["Actual Loops"] != float64(1) {
			t.Fatalf("nested actuals for %q: %#v; want %v/1", source, document, rows)
		}
		if source == innerSource && literal != "" {
			found := false
			explainJSONVisit(root, func(node map[string]any) {
				filter, _ := node["Filter"].(string)
				found = found || strings.Contains(filter, literal)
			})
			if !found {
				t.Fatalf("current local literal %q absent from nested plan: %#v", literal, document)
			}
		}
	}
	if outer != 1 || inner != want-1 {
		t.Fatal("nested source bag", outer, inner, want)
	}
}

func TestReferenceAutoExplainSessions(t *testing.T) {
	autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn, notices *[]fixture.Diagnostic) {
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		run := func(c *sql.Conn, s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		autoExplainConfigure(t, p, b, a, notices)
		const query = "SELECT 17::integer AS v"
		step := fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(17)}})
		run(peer, fixture.Exec("SET auto_explain.log_min_duration = 0", 0))
		run(peer, step)
		autoExplainQuiet(t, notices)
		run(a, step)
		autoExplainJSON(t, notices, query, nil, true, 1)
		// Returning a healthy lease preserves the physical loaded module.
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		reused, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer reused.Close()
		run(reused, step)
		autoExplainJSON(t, notices, query, nil, true, 1)
		// Force actual physical retirement, not another lease return.
		b.DB.SetMaxIdleConns(0)
		if err := reused.Close(); err != nil {
			t.Fatal(err)
		}
		fresh, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		run(fresh, fixture.Select("SELECT current_setting('auto_explain.log_min_duration',true) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
		run(fresh, step)
		autoExplainQuiet(t, notices)
	})
}
