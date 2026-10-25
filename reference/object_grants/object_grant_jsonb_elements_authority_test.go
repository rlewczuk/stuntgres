package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantJSONBElementsAuthority(t *testing.T) {
	for _, name := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
		t.Run(name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_jbe_source(id INT,j JSONB)", 0))
				run(fixture.Exec(`INSERT INTO og_jbe_source VALUES(1,'[1,1,null,"x"]'),(2,'[]'),(3,NULL)`, 3))
				run(fixture.Exec("GRANT SELECT ON og_jbe_source TO og_object_reader", 0))
				typ, sibling, siblingType := "JSONB", "jsonb_array_elements_text", "TEXT"
				var nullValue any = "null"
				stringValue := `"x"`
				if name == "jsonb_array_elements_text" {
					typ, sibling, siblingType = "TEXT", "jsonb_array_elements", "JSONB"
					nullValue, stringValue = nil, "x"
				}
				input := `[1,1,null,"x"]`
				target := fixture.Select("SELECT "+name+"($1::JSONB) AS value", []string{"value"}, []string{typ}, [][]any{{"1"}, {"1"}, {nullValue}, {stringValue}}, input)
				target.Bag = true
				lateral := fixture.Select("SELECT s.id,e.value FROM og_jbe_source s CROSS JOIN LATERAL "+name+"(s.j) AS e", []string{"id", "value"}, []string{"INT4", typ}, [][]any{{int64(1), "1"}, {int64(1), "1"}, {int64(1), nullValue}, {int64(1), stringValue}})
				lateral.Bag = true
				cases := []objectGrantProviderCase{
					{fixture.Select("SELECT e.value,e.n FROM pg_catalog."+name+"(from_json=>$1::JSONB) WITH ORDINALITY AS e(value,n) ORDER BY e.n", []string{"value", "n"}, []string{typ, "INT8"}, [][]any{{"1", int64(1)}, {"1", int64(2)}, {nullValue, int64(3)}, {stringValue, int64(4)}}, input), true, true},
					{target, true, true},
					{lateral, true, true},
					{fixture.Select("SELECT e FROM "+name+"($1::JSONB) AS e", []string{"e"}, []string{typ}, [][]any{{"7"}}, "[7]"), true, true},
				}
				controls := []fixture.Step{
					fixture.Select("SELECT value FROM "+sibling+"($1::JSONB)", []string{"value"}, []string{siblingType}, [][]any{{"7"}}, "[7]"),
					fixture.Select("SELECT value FROM json_array_elements($1::JSON)", []string{"value"}, []string{"JSON"}, [][]any{{"7"}}, "[7]"),
				}
				objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog."+name+"(jsonb)", "function", name, cases, controls)
			})
		})
	}
}

func TestReferenceObjectGrantJSONBElementsAuthorityDemand(t *testing.T) {
	for _, name := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
		t.Run(name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_jbe_null(id INT,j JSONB)", 0))
				run(fixture.Exec("INSERT INTO og_jbe_null VALUES(1,NULL)", 1))
				run(fixture.Exec("GRANT SELECT ON og_jbe_null TO og_object_reader", 0))
				typ := "JSONB"
				if name == "jsonb_array_elements_text" {
					typ = "TEXT"
				}
				cases := []objectGrantProviderCase{
					{fixture.Select("SELECT value FROM "+name+"(NULL::JSONB)", []string{"value"}, []string{typ}, nil), true, true},
					{fixture.Select("SELECT value FROM "+name+"('[]'::JSONB)", []string{"value"}, []string{typ}, nil), true, true},
					{fixture.Select("SELECT value FROM "+name+"($1::JSONB)", []string{"value"}, []string{typ}, nil, nil), true, true},
					{fixture.Select("SELECT e.value FROM og_jbe_null s CROSS JOIN LATERAL "+name+"(s.j) AS e", []string{"value"}, []string{typ}, nil), true, true},
					{fixture.Select("SELECT e.value FROM og_jbe_null s CROSS JOIN LATERAL "+name+"(s.j) AS e WHERE s.id=99", []string{"value"}, []string{typ}, nil), true, true},
					{fixture.Select("SELECT "+name+"(NULL::JSONB) AS value", []string{"value"}, []string{typ}, nil), true, true},
					{fixture.Select("SELECT "+name+"('[1]'::JSONB) AS value WHERE FALSE", []string{"value"}, []string{typ}, nil), true, true},
					{fixture.Select("SELECT value FROM "+name+"('[1]'::JSONB) WHERE FALSE", []string{"value"}, []string{typ}, nil), false, true},
					{fixture.Select("SELECT e.value FROM og_jbe_null s CROSS JOIN LATERAL "+name+"(s.j) AS e WHERE FALSE", []string{"value"}, []string{typ}, nil), false, true},
				}
				objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog."+name+"(jsonb)", "function", name, cases, nil)
			})
		})
	}
}
