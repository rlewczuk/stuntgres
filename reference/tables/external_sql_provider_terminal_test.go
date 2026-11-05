package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func externalSQLTerminalErrorOwnership(t *testing.T, p *fixture.Pair, provider *fixture.ExternalSQLProvider, handler *externalSQLHandler) {
	t.Helper()
	cleanupFailure := errors.New("actual remote terminal close failed after closing transport")
	var extra stuntgres.ExternalSQLConnection
	handler.action = func(ctx context.Context, r stuntgres.ForeignRequest, _ stuntgres.ExternalSQLConnection) error {
		provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) {
			f.CloseError = cleanupFailure
		})
		var err error
		extra, err = r.ExternalSQL.Open(ctx, stuntgres.ExternalSQLConnectRequest{})
		provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) {
			f.CloseError = nil
		})
		return err
	}
	defer func() { handler.action = nil }()
	physical, err := p.NativeConnector.Connect(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer physical.Close()
	rows, err := physical.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT id FROM external_sql_data WHERE id=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	metadata, ok := rows.(driver.RowsColumnTypeDatabaseTypeName)
	if !ok || metadata.ColumnTypeDatabaseTypeName(0) != "INT4" {
		t.Fatal("terminal owner typed descriptor", rows)
	}
	values := make([]driver.Value, 1)
	if err := rows.Next(values); err != nil || values[0] != int64(1) {
		t.Fatal("terminal owner real source value", values, err)
	}
	if err := rows.Next(values); err != io.EOF {
		t.Fatal("terminal owner row tail", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	pid := extra.Info().BackendPID
	if err := physical.Close(); !errors.Is(err, cleanupFailure) {
		t.Fatal("physical Close dropped joined terminal error", err)
	}
	externalSQLBackendGone(t, p.Ctx, pid)
	if err := physical.Close(); !errors.Is(err, cleanupFailure) {
		t.Fatal("terminal Close error was not sticky", err)
	}
}
