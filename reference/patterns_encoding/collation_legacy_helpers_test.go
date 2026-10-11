package reference_test

import (
	"database/sql"
	"io"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Preserve the complete old error record while accepting both public protocols.
func collationLegacyDiagnostic(t *testing.T, err error, d stuntgres.Error) error {
	t.Helper()
	fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: d.Code, Message: d.Message, Detail: d.Detail, Hint: d.Hint, Position: d.Position, InternalPosition: d.InternalPosition, InternalQuery: d.InternalQuery, Context: d.Context, Schema: d.Schema, Table: d.Table, Column: d.Column, DataType: d.DataType, Constraint: d.Constraint})
	return nil
}

func collationLegacyError(t *testing.T, err error, code, message, position string) {
	t.Helper()
	collationLegacyDiagnostic(t, err, stuntgres.Error{Code: code, Message: message, Position: position})
}

func collationLegacyRows(t *testing.T, pg bool, rows *sql.Rows, names, types []string, values [][]any) {
	t.Helper()
	fixture.DeleteReferenceConsume(t, pg, rows, nil, fixture.Step{Columns: names, Types: types, Rows: values})
}

type collationLegacyReader struct {
	io.Reader
	entered chan struct{}
}

func (r *collationLegacyReader) Read(p []byte) (int, error) {
	if r.entered != nil {
		close(r.entered)
		r.entered = nil
	}
	return r.Reader.Read(p)
}
