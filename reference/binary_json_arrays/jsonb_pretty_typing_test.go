package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBPrettyTypingMetadata(t *testing.T) {
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, input := range []any{"[]", nil} {
			var want any
			if input != nil {
				want = "[\n]"
			}
			for _, alias := range []bool{false, true} {
				q, name := "SELECT jsonb_pretty($1)", "jsonb_pretty"
				if alias {
					q += " AS pretty"
					name = "pretty"
				}
				s := fixture.Select(q, []string{name}, []string{"TEXT"}, [][]any{{want}}, input)
				jsonbPrettyBoth(t, p, b, c, s)
				s.Query += " WHERE false"
				s.Rows = nil
				jsonbPrettyBoth(t, p, b, c, s)
			}
		}
		for _, q := range []string{
			`SELECT JsOnB_PrEtTy('{}') AS pretty`,
			`SELECT pg_catalog.jsonb_pretty('{}') AS pretty`,
			`SELECT "jsonb_pretty"('{}') AS pretty`,
			`SELECT jsonb_pretty('{}'::text::jsonb) AS pretty`,
		} {
			jsonbPrettyBoth(t, p, b, c, jsonbPrettyOne(q, "{\n}"))
		}
		jsonbPrettyBoth(t, p, b, c, jsonbPrettyOne(`SELECT jsonb_pretty(NULL::jsonb) AS pretty`, nil))
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE DOMAIN jbp_doc AS jsonb", 0))
		jsonbPrettyBoth(t, p, b, c, jsonbPrettyOne(`SELECT jsonb_pretty('{}'::jbp_doc) AS pretty`, "{\n}"))
	})
}

func jsonbPrettyHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	const q = "SELECT jsonb_pretty($1) AS pretty"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, tc := range []struct{ input, want any }{
		{jsonbPrettyNestedInput, jsonbPrettyNested}, {nil, nil},
		{jsonbPrettyEscapeInput, jsonbPrettyEscaped}, {[]byte(nil), nil},
		{[]byte("[]"), "[\n]"}, {jsonTypeOrderedObject, jsonbPrettyOrdered},
	} {
		jsonTypeRun(t, p, b, held, jsonTypeWithMetadata(jsonbPrettyOne(q, tc.want, tc.input), b.PG))
	}
	jsonTypeRun(t, p, b, held, jsonbPrettyInputError(q, true, "oops"))
	jsonTypeRun(t, p, b, held, jsonTypeWithMetadata(jsonbPrettyOne(q, "[\n]", "[]"), b.PG))
	owned := []byte(jsonbPrettyNestedInput)
	rows, err := stmt.QueryContext(p.Ctx, owned)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	jsonTypeMetadataCheck(t, rows, b.PG, []string{"pretty"}, []string{"TEXT"})
	metadata, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("missing retained row", rows.Err())
	}
	var text string
	var data []byte
	if err := rows.Scan(&text); err != nil {
		t.Fatal(err)
	}
	if err := rows.Scan(&data); err != nil {
		t.Fatal(err)
	}
	for i := range owned {
		owned[i] = 'x'
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("unexpected retained rows", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	jsonTypeRun(t, p, b, held, jsonTypeWithMetadata(jsonbPrettyOne(q, "{\n}", "{}"), b.PG))
	if text != jsonbPrettyNested || string(data) != jsonbPrettyNested {
		t.Fatalf("retained values=%q/%q", text, data)
	}
	if metadata[0].DatabaseTypeName() != "TEXT" || metadata[0].Name() != "pretty" {
		t.Fatal("retained metadata changed", metadata)
	}
}

func TestReferenceJSONBPrettyHeld(t *testing.T) { jsonTypeScenario(t, jsonbPrettyHeld) }
