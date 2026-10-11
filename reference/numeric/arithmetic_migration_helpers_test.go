package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func referenceMigratedRows(rows *sql.Rows, err error, columns, types []string, want [][]any) error {
	if err != nil {
		return err
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return err
	}
	meta, err := rows.ColumnTypes()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(names, columns) || len(meta) != len(types) {
		return fmt.Errorf("columns %v metadata %v want %v/%v", names, meta, columns, types)
	}
	for i, typ := range types {
		if meta[i].DatabaseTypeName() != typ {
			return fmt.Errorf("type %d: %s want %s", i, meta[i].DatabaseTypeName(), typ)
		}
	}
	got := make([][]any, 0)
	for rows.Next() {
		values := make([]any, len(types))
		targets := make([]any, len(types))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return err
		}
		for i := range values {
			values[i] = fixture.Normalize(values[i])
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("rows %#v want %#v", got, want)
	}
	return rows.Close()
}
func referenceMigratedDiagnostic(t *testing.T, err error, want stuntgres.Error) error {
	t.Helper()
	fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: want.Code, Message: want.Message, Detail: want.Detail, Hint: want.Hint, Position: want.Position})
	return nil
}

// PostgreSQL may expose a stream before a runtime error. Native must not expose
// a cursor on its eager failure; both routes check the complete terminal error.
func referenceMigratedFailure(t *testing.T, pg bool, rows *sql.Rows, err error) error {
	t.Helper()
	if rows != nil {
		defer rows.Close()
		if !pg {
			t.Fatal("arithmetic error exposed cursor")
		}
		for rows.Next() {
		}
		err = rows.Err()
	}
	return err
}
