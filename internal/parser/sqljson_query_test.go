package parser

import "testing"

func TestSQLJSONQueryParser(t *testing.T) {
	statements, err := Parse(`/*é🙂*/ SELECT JSON_QUERY(JSON_QUERY($1::jsonb,'$.a'),'$x' PASSING $2::date AS "X",$3::jsonb AS x RETURNING text WITH CONDITIONAL ARRAY WRAPPER KEEP QUOTES DEFAULT $4::text ON EMPTY DEFAULT $5::text ON ERROR)`)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := statements[0].(*Select).Targets[0].(SQLJSONQueryExpr)
	if !ok || raw.Kind != "json_query" || raw.Wrapper != "conditional" || raw.Quotes != "keep" || raw.Returning == nil || len(raw.Passing) != 2 || raw.Passing[0].Name.Text != "X" || len(raw.Children()) != 6 || raw.Pos.Character != 15 {
		t.Fatalf("query AST: %#v", raw)
	}
	if _, ok := raw.Document.(SQLJSONQueryExpr); !ok {
		t.Fatalf("nested query lost: %#v", raw.Document)
	}
	next := raw.WithChildren(raw.Children())
	next.Passing[0].Name.Text = "changed"
	if raw.Passing[0].Name.Text != "X" {
		t.Fatal("child reconstruction mutated source bindings")
	}
	for _, query := range []string{
		`SELECT JSON_VALUE('{}','$' WITH WRAPPER)`,
		`SELECT JSON_EXISTS('{}','$' RETURNING boolean)`,
		`SELECT JSON_VALUE('{}','$' OMIT QUOTES)`,
		`SELECT JSON_QUERY('{}','$') trailing garbage`,
	} {
		if _, err := Parse(query); err == nil {
			t.Fatalf("invalid query accepted: %s", query)
		}
	}
	for _, query := range []string{
		`SELECT JSON_QUERY('{}','$' EMPTY OBJECT ON ERROR)`,
		`SELECT JSON_VALUE('{}','$' DEFAULT 1 ON ERROR)`,
		`SELECT COALESCE(JSON_VALUE('{}','strict $a[0] == $b' PASSING a AS a,b AS b RETURNING boolean DEFAULT false ON ERROR),false)`,
		`SELECT JSON_VALUE('{}','$' RETURNING boolean DEFAULT NOT false ON ERROR)`,
		`SELECT JSON_VALUE('{}','$' RETURNING boolean DEFAULT true AND false ON ERROR)`,
		`SELECT JSON_VALUE('{}','$' DEFAULT NULL ON EMPTY DEFAULT NULL ON ERROR)`,
		`SELECT JSON_EXISTS('{}','$' FALSE ON ERROR),JSON_EXISTS('{}','$' TRUE ON ERROR),JSON_EXISTS('{}','$' UNKNOWN ON ERROR)`,
		`SELECT JSON_QUERY('{}','$' NULL ON EMPTY NULL ON ERROR)`,
		`SELECT JSON_VALUE('{}','$' RETURNING boolean DEFAULT JSON_EXISTS('{}','$' FALSE ON ERROR) ON ERROR)`,
		`SELECT JSON_QUERY('{}','$' DEFAULT JSON_ARRAY(NULL NULL ON NULL) ON ERROR)`,
		`SELECT JSON_ARRAY(JSON_VALUE('{}','$' DEFAULT NULL ON ERROR) NULL ON NULL)`,
		`CREATE TABLE sqjq_parser_control(a timestamp,b timestamptz,v boolean GENERATED ALWAYS AS (COALESCE(JSON_VALUE('{}','strict $a[*] == $b' PASSING a AS a,b AS b RETURNING boolean DEFAULT false ON ERROR),false)) STORED)`,
		`CREATE INDEX sqjq_parser_control_index ON sqjq_parser_control((COALESCE(JSON_VALUE('{}','strict $a[0] == $b' PASSING a AS a,b AS b RETURNING boolean DEFAULT false ON ERROR),false)))`,
		`SELECT JSON_QUERY('{}','$' PASSING true AS x RETURNING text)`,
		`SELECT JSON_QUERY(error,'$' PASSING passing AS x)`,
		`SELECT JSON_QUERY($1::jsonb,$2::jsonpath WITH WRAPPER ERROR ON ERROR)`,
		`SELECT JSON_QUERY($1::jsonb,$2::jsonpath WITHOUT ARRAY WRAPPER)`,
		`SELECT JSON_QUERY($1::jsonb,$2::jsonpath WITH CONDITIONAL ARRAY WRAPPER)`,
		`SELECT JSON_QUERY('{}','$' PASSING passing AS passing, passing AS x)`,
		`SELECT "json_query"('{}','$'),s.json_query('{}','$') FROM s`,
	} {
		if _, err := Parse(query); err != nil {
			t.Fatalf("scoped syntax %s: %v", query, err)
		}
	}
	for _, literal := range []string{"false", "true", "NULL"} {
		statements, err := Parse(`SELECT JSON_VALUE('{}','$' RETURNING boolean DEFAULT ` + literal + ` ON EMPTY DEFAULT ` + literal + ` ON ERROR)`)
		if err != nil {
			t.Fatal(err)
		}
		query := statements[0].(*Select).Targets[0].(SQLJSONQueryExpr)
		for _, behavior := range []SQLJSONBehavior{query.Empty, query.Error} {
			if _, ok := behavior.Default.(LiteralExpr); behavior.Kind != "default" || !ok {
				t.Fatalf("%s DEFAULT lost literal child: %#v", literal, behavior)
			}
		}
	}
}
