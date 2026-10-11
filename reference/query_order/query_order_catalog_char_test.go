package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOrderCatalogChar(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE ordering_char_table(id INT)", 0))
	p.Step(t, fixture.Exec("CREATE SEQUENCE ordering_char_sequence", 0))
	p.Step(t, fixture.Exec("CREATE VIEW ordering_char_view AS SELECT id FROM ordering_char_table", 0))
	filter := " WHERE relname='ordering_char_table' OR relname='ordering_char_sequence' OR relname='ordering_char_view'"
	for _, step := range []fixture.Step{
		fixture.Select("SELECT relname,relkind FROM pg_catalog.pg_class"+filter+" ORDER BY relkind,relname", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{"ordering_char_sequence", "S"}, {"ordering_char_table", "r"}, {"ordering_char_view", "v"}}),
		fixture.Select("SELECT relname,relkind FROM pg_catalog.pg_class"+filter+" ORDER BY relkind DESC,relname", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{"ordering_char_view", "v"}, {"ordering_char_table", "r"}, {"ordering_char_sequence", "S"}}),
		fixture.Select("SELECT relname,CASE WHEN relname='ordering_char_view' THEN NULL ELSE relkind END AS kind FROM pg_catalog.pg_class"+filter+" ORDER BY kind NULLS FIRST,relname", []string{"relname", "kind"}, []string{"NAME", "CHAR"}, [][]any{{"ordering_char_view", nil}, {"ordering_char_sequence", "S"}, {"ordering_char_table", "r"}}),
		fixture.Select("SELECT relkind FROM pg_catalog.pg_class WHERE relname='ordering_char_view' ORDER BY relkind", []string{"relkind"}, []string{"CHAR"}, [][]any{{"v"}}),
		fixture.Select("SELECT relkind FROM pg_catalog.pg_class WHERE relname='ordering_char_absent' ORDER BY relkind", []string{"relkind"}, []string{"CHAR"}, nil),
	} {
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
}
