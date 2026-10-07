package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestQueryOrderCatalogChar(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE ordering_char_table(id INT)")
	execSQL(t, db, "CREATE SEQUENCE ordering_char_sequence")
	execSQL(t, db, "CREATE VIEW ordering_char_view AS SELECT id FROM ordering_char_table")
	filter := " WHERE relname='ordering_char_table' OR relname='ordering_char_sequence' OR relname='ordering_char_view'"
	for _, tc := range []struct {
		q            string
		names, types []string
		want         [][]any
	}{
		{"SELECT relname,relkind FROM pg_catalog.pg_class" + filter + " ORDER BY relkind,relname", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{"ordering_char_sequence", "S"}, {"ordering_char_table", "r"}, {"ordering_char_view", "v"}}},
		{"SELECT relname,relkind FROM pg_catalog.pg_class" + filter + " ORDER BY relkind DESC,relname", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{"ordering_char_view", "v"}, {"ordering_char_table", "r"}, {"ordering_char_sequence", "S"}}},
		{"SELECT relname,CASE WHEN relname='ordering_char_view' THEN NULL ELSE relkind END AS kind FROM pg_catalog.pg_class" + filter + " ORDER BY kind NULLS FIRST,relname", []string{"relname", "kind"}, []string{"NAME", "CHAR"}, [][]any{{"ordering_char_view", nil}, {"ordering_char_sequence", "S"}, {"ordering_char_table", "r"}}},
		{"SELECT relkind FROM pg_catalog.pg_class WHERE relname='ordering_char_view' ORDER BY relkind", []string{"relkind"}, []string{"CHAR"}, [][]any{{"v"}}},
		{"SELECT relkind FROM pg_catalog.pg_class WHERE relname='ordering_char_absent' ORDER BY relkind", []string{"relkind"}, []string{"CHAR"}, [][]any{}},
	} {
		rows, err := db.Query(tc.q)
		queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, false)
		stmt, err := db.Prepare(tc.q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = stmt.Query()
		queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, false)
		stmt.Close()
	}
}
