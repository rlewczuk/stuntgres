package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"testing"

	"stuntgres"
)

func scalarNativeExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatal(query, err)
	}
}

// Control checkpoints preserve original independent tuples. They are not an
// oracle: only literal expectations supplied by the scenario are accepted.
func scalarNativeTuples(t *testing.T, db *sql.DB, query string, want [][]any, args ...any) {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	got := [][]any{}
	for rows.Next() {
		row, dest := make([]any, len(columns)), make([]any, len(columns))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok := referenceCompareRows(got, want, !strings.Contains(strings.ToUpper(query), "ORDER BY")); !ok {
		t.Fatalf("control checkpoint %s: %#v want %#v", query, got, want)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func scalarNativeDiagnostic(t *testing.T, err error, want stuntgres.Error) {
	t.Helper()
	d := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: want.Code, Message: want.Message, Detail: want.Detail, Hint: want.Hint, Position: want.Position, InternalPosition: want.InternalPosition, InternalQuery: want.InternalQuery, Context: want.Context, Schema: want.Schema, Table: want.Table, Column: want.Column, DataType: want.DataType, Constraint: want.Constraint}
	referenceState(t, err, want.Code)
	referenceAssertDiagnostic(t, err, &d)
}

// Keep access to the public driver confined to sql.Conn.Raw's callback. No
// driver object or cursor escapes the lease; controls use the unwrapped pool.
func scalarNativeRaw(t *testing.T, p *referencePair, scenario func(driver.QueryerContext)) {
	t.Helper()
	connection, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := connection.Raw(func(raw any) error { scenario(raw.(driver.QueryerContext)); return nil }); err != nil {
		t.Fatal(err)
	}
}
