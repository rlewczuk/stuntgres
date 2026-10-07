//go:build reference

package reference_test

import "testing"

func TestReferenceQueryOrderCatalogChar(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE ordering_char_table(id INT)", 0))
	p.step(t, referenceExec("CREATE SEQUENCE ordering_char_sequence", 0))
	p.step(t, referenceExec("CREATE VIEW ordering_char_view AS SELECT id FROM ordering_char_table", 0))
	filter := " WHERE relname='ordering_char_table' OR relname='ordering_char_sequence' OR relname='ordering_char_view'"
	for _, step := range []referenceStep{
		referenceSelect("SELECT relname,relkind FROM pg_catalog.pg_class"+filter+" ORDER BY relkind,relname", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{"ordering_char_sequence", "S"}, {"ordering_char_table", "r"}, {"ordering_char_view", "v"}}),
		referenceSelect("SELECT relname,relkind FROM pg_catalog.pg_class"+filter+" ORDER BY relkind DESC,relname", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{"ordering_char_view", "v"}, {"ordering_char_table", "r"}, {"ordering_char_sequence", "S"}}),
		referenceSelect("SELECT relname,CASE WHEN relname='ordering_char_view' THEN NULL ELSE relkind END AS kind FROM pg_catalog.pg_class"+filter+" ORDER BY kind NULLS FIRST,relname", []string{"relname", "kind"}, []string{"NAME", "CHAR"}, [][]any{{"ordering_char_view", nil}, {"ordering_char_sequence", "S"}, {"ordering_char_table", "r"}}),
		referenceSelect("SELECT relkind FROM pg_catalog.pg_class WHERE relname='ordering_char_view' ORDER BY relkind", []string{"relkind"}, []string{"CHAR"}, [][]any{{"v"}}),
		referenceSelect("SELECT relkind FROM pg_catalog.pg_class WHERE relname='ordering_char_absent' ORDER BY relkind", []string{"relkind"}, []string{"CHAR"}, nil),
	} {
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}
