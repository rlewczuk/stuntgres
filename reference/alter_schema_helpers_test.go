package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func referenceSchemaSequenceState(t *testing.T, p *referencePair, c *sql.Conn, name string, value int64, called bool) {
	t.Helper()
	rows, err := c.QueryContext(p.ctx, "SELECT * FROM "+name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	labels, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(labels, []string{"last_value", "log_cnt", "is_called"}) {
		t.Fatal(labels, err)
	}
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 3 {
		t.Fatal(columns, err)
	}
	for i, want := range []string{"INT8", "INT8", "BOOL"} {
		if columns[i].DatabaseTypeName() != want {
			t.Fatal(i, columns[i].DatabaseTypeName(), want)
		}
	}
	if !rows.Next() {
		t.Fatal("missing sequence state", rows.Err())
	}
	var got, log int64
	var isCalled bool
	if err = rows.Scan(&got, &log, &isCalled); err != nil || got != value || isCalled != called || log < 0 {
		t.Fatal(got, log, isCalled, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("sequence state exhaustion", rows.Err())
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func referenceSchemaHeldRows(t *testing.T, held *sql.Rows) {
	t.Helper()
	labels, err := held.Columns()
	if err != nil || !reflect.DeepEqual(labels, []string{"id", "note"}) {
		t.Fatal(labels, err)
	}
	columns, err := held.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	referenceTableDDLColumnTypes(t, false, columns, []string{"id", "note"}, []string{"INT4", "TEXT"}, []bool{false, true})
	got := [][]any{}
	for held.Next() {
		var id, note any
		if err = held.Scan(&id, &note); err != nil {
			t.Fatal(err)
		}
		referenceAssertRawValue(t, false, "INT4", id)
		referenceAssertRawValue(t, false, "TEXT", note)
		got = append(got, []any{id, note})
	}
	if held.Err() != nil || !reflect.DeepEqual(got, [][]any{{int64(7), "keep"}, {int64(9), nil}}) {
		t.Fatal(got, held.Err())
	}
	if err = held.Close(); err != nil {
		t.Fatal(err)
	}
}
