package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/lib/pq"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type externalSQLHandler struct {
	mu          sync.Mutex
	connections []stuntgres.ExternalSQLConnection
	sessions    []stuntgres.ExternalSQLSession
	action      func(context.Context, stuntgres.ForeignRequest, stuntgres.ExternalSQLConnection) error
}

func externalSQLConfigure(d *stuntgres.Driver, provider *fixture.ExternalSQLProvider, handler *externalSQLHandler, version string) error {
	if err := d.RegisterExternalSQLProvider(stuntgres.ExternalSQLProviderDefinition{Name: "external_sql_fixture", Version: version, Provider: provider}); err != nil {
		return err
	}
	return d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Name: "external_sql_fixture_handler"}, ExternalProvider: "external_sql_fixture", Wrapper: handler})
}
func (*externalSQLHandler) GetForeignRelSize(_ context.Context, r stuntgres.ForeignRequest) (stuntgres.ForeignRelSize, error) {
	if r.ExternalSQL != nil {
		return stuntgres.ForeignRelSize{}, errors.New("planning received executable external capability")
	}
	return stuntgres.ForeignRelSize{Rows: 4, Width: float64(32 * len(r.Columns))}, nil
}
func (*externalSQLHandler) GetForeignPaths(_ context.Context, r stuntgres.ForeignRequest, _ stuntgres.ForeignRelSize) ([]stuntgres.ForeignPath, error) {
	if r.ExternalSQL != nil {
		return nil, errors.New("planning received executable external capability")
	}
	attributes := make([]int32, len(r.Columns))
	for i, c := range r.Columns {
		attributes[i] = c.Attribute
	}
	return []stuntgres.ForeignPath{{TotalCost: 1, Attributes: attributes}}, nil
}
func (*externalSQLHandler) GetForeignPlan(_ context.Context, r stuntgres.ForeignRequest, path stuntgres.ForeignPath) (stuntgres.ForeignPlan, error) {
	if r.ExternalSQL != nil {
		return stuntgres.ForeignPlan{}, errors.New("planning received executable external capability")
	}
	return stuntgres.ForeignPlan{Attributes: path.Attributes}, nil
}
func (*externalSQLHandler) ForeignTransactionCapabilities(context.Context, stuntgres.ForeignRequest) (stuntgres.ForeignTransactionCapabilities, error) {
	return stuntgres.ForeignTransactionCapabilities{StatementRollback: true, TransactionRollback: true, Savepoints: true}, nil
}
func (h *externalSQLHandler) BeginForeignTransaction(ctx context.Context, r stuntgres.ForeignRequest) (stuntgres.ForeignTransaction, error) {
	if r.ExternalSQL == nil {
		return nil, &stuntgres.ExternalSQLUnavailableError{Capability: "fixture execution session"}
	}
	h.mu.Lock()
	h.sessions = append(h.sessions, r.ExternalSQL)
	h.mu.Unlock()
	c, err := r.ExternalSQL.Open(ctx, stuntgres.ExternalSQLConnectRequest{})
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	h.connections = append(h.connections, c)
	h.mu.Unlock()
	tx := &externalSQLTransaction{connection: c}
	isolation := "REPEATABLE READ"
	if r.Characteristics != nil && r.Characteristics.Isolation == "serializable" {
		isolation = "SERIALIZABLE"
	}
	_, err = c.Execute(ctx, "BEGIN ISOLATION LEVEL "+isolation)
	return tx, err
}

func (h *externalSQLHandler) BeginForeignScan(ctx context.Context, r stuntgres.ForeignRequest, _ stuntgres.ForeignPlan) (stuntgres.ForeignScan, error) {
	tx, ok := r.Transaction.(*externalSQLTransaction)
	if !ok {
		return nil, errors.New("real external transaction missing")
	}
	h.mu.Lock()
	action := h.action
	h.mu.Unlock()
	if action != nil {
		if err := action(ctx, r, tx.connection); err != nil {
			return nil, err
		}
	}
	columns := make([]string, len(r.Columns))
	for i, c := range r.Columns {
		columns[i] = pq.QuoteIdentifier(externalSQLOption(c.Options, "column_name", c.Name))
	}
	schema := externalSQLOption(r.TableOptions, "schema_name", r.Schema)
	table := externalSQLOption(r.TableOptions, "table_name", r.Table)
	projection := strings.Join(columns, ",")
	q := "SELECT " + projection + " FROM " + pq.QuoteIdentifier(schema) + "." + pq.QuoteIdentifier(table)
	results, err := tx.connection.Execute(ctx, q)
	if err != nil {
		return nil, err
	}
	if len(results) != 1 || !results[0].Tuple || len(results[0].Columns) != len(r.Columns) {
		return nil, errors.New("external descriptor does not match declared foreign columns")
	}
	return &externalSQLScan{result: results[0]}, nil
}
func externalSQLOption(options []stuntgres.ForeignOption, name, fallback string) string {
	for _, o := range options {
		if o.Name == name {
			return o.Value
		}
	}
	return fallback
}

type externalSQLScan struct {
	result stuntgres.ExternalSQLResult
	index  int
}

func (s *externalSQLScan) IterateForeignScan(context.Context) (stuntgres.ForeignRow, bool, error) {
	if s.index == s.result.RowCount {
		return nil, false, nil
	}
	width := len(s.result.Columns)
	row := make([]driver.Value, width)
	for i := range row {
		cell := s.result.Cells[s.index*width+i]
		if cell.Null {
			continue
		}
		column := s.result.Columns[i]
		if column.TypeOID == pgtype.ByteaOID {
			// ExternalSQLResult owns PostgreSQL output text; ForeignRow
			// exports decoded driver values. Decode bytea exactly once here,
			// not in the raw transport or the independently expected rows.
			value, err := (pgtype.ByteaCodec{}).DecodeDatabaseSQLValue(nil, column.TypeOID, column.Format, []byte(cell.Text))
			if err != nil {
				return nil, false, fmt.Errorf("remote bytea column %q: %w", column.Name, err)
			}
			row[i] = value
		} else {
			row[i] = cell.Text
		}
	}
	s.index++
	return row, true, nil
}
func (s *externalSQLScan) ReScanForeignScan(context.Context, stuntgres.ForeignScanParameters) error {
	s.index = 0
	return nil
}
func (s *externalSQLScan) EndForeignScan(context.Context) error {
	s.result = stuntgres.ExternalSQLResult{}
	return nil
}

type externalSQLTransaction struct {
	connection stuntgres.ExternalSQLConnection
}

func (t *externalSQLTransaction) command(ctx context.Context, q string) error {
	_, err := t.connection.Execute(ctx, q)
	return err
}
func (t *externalSQLTransaction) BeginStatement(ctx context.Context) error {
	return t.command(ctx, "SAVEPOINT external_sql_statement")
}
func (t *externalSQLTransaction) EndStatement(ctx context.Context, success bool) error {
	if !success {
		if err := t.command(ctx, "ROLLBACK TO SAVEPOINT external_sql_statement"); err != nil {
			return err
		}
	}
	return t.command(ctx, "RELEASE SAVEPOINT external_sql_statement")
}
func (t *externalSQLTransaction) finish(ctx context.Context, q string) error {
	err := t.command(ctx, q)
	cleanup := t.connection.Close(context.WithoutCancel(ctx))
	if err != nil {
		return err
	}
	return cleanup
}
func (t *externalSQLTransaction) Commit(ctx context.Context) error { return t.finish(ctx, "COMMIT") }
func (t *externalSQLTransaction) Rollback(ctx context.Context) error {
	return t.finish(context.WithoutCancel(ctx), "ROLLBACK")
}
func (t *externalSQLTransaction) Savepoint(ctx context.Context, name string) error {
	return t.command(ctx, "SAVEPOINT "+pq.QuoteIdentifier(name))
}
func (t *externalSQLTransaction) RollbackTo(ctx context.Context, name string) error {
	return t.command(ctx, "ROLLBACK TO SAVEPOINT "+pq.QuoteIdentifier(name))
}
func (t *externalSQLTransaction) Release(ctx context.Context, name string) error {
	return t.command(ctx, "RELEASE SAVEPOINT "+pq.QuoteIdentifier(name))
}
