package reference_test

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// PostgreSQL deliberately emits an empty array for no rewritten commands. This
// exception must never relax the ordinary Plan document reader.
func explainJSONEmptyRewriteRows(t *testing.T, b fixture.Backend, rows *sql.Rows) {
	t.Helper()
	if !b.PG {
		document, _ := explainJSONRows(t, rows)
		root := explainJSONFind(t, document, "Node Type", "Rewrite")
		if root["Action"] != "nothing" {
			t.Fatal(root)
		}
		explainJSONAbsent(t, document, "Actual Rows", "Actual Loops", "Actual Total Time", "Actual Startup Time")
		return
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"QUERY PLAN"}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "JSON" {
		t.Fatal(types, err)
	}
	if !rows.Next() {
		t.Fatal("missing rewrite document", rows.Err())
	}
	var text string
	if err := rows.Scan(&text); err != nil {
		t.Fatal(err)
	}
	var empty []any
	if err := json.Unmarshal([]byte(text), &empty); err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("no-command rewrite must be exactly []: %s, %v", text, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("multiple/failed rewrite rows", rows.Err())
	}
}
