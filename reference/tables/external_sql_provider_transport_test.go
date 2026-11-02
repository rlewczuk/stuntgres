package reference_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgconn"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const externalSQLFixtureVersion = "pgconn-fixture-1"

var externalSQLApplication atomic.Uint64

// Network, libpq-compatible parameter parsing and authentication are test-host
// responsibilities. No transport code below is part of the embedded engine.
type externalSQLProvider struct {
	mu        sync.Mutex
	opened    []*externalSQLWire
	afterOpen func(context.Context, *externalSQLWire) error
	// Fault probes still open/close genuine PostgreSQL connections.
	nilSuccess   bool
	closeError   error
	mutateResult func([]stuntgres.ExternalSQLResult)
	requests     []stuntgres.ExternalSQLConnectRequest
}

type externalSQLWire struct {
	closeError   error
	mutateResult func([]stuntgres.ExternalSQLResult)
	conn         *pgconn.PgConn
	info         stuntgres.ExternalSQLConnectionInfo
	events       []stuntgres.ExternalSQLNotification // Command serialization owns input.
}

func (p *externalSQLProvider) Open(ctx context.Context, request stuntgres.ExternalSQLConnectRequest) (stuntgres.ExternalSQLConnection, error) {
	dsn := request.Conninfo
	if dsn == "" {
		dsn = fixture.DSN
	}
	config, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if request.Conninfo == "" {
		for _, options := range [][]stuntgres.ForeignOption{request.ServerOptions, request.MappingOptions} {
			for _, o := range options {
				switch o.Name {
				case "host":
					config.Host = o.Value
				case "port":
					port, e := strconv.ParseUint(o.Value, 10, 16)
					if e != nil {
						return nil, e
					}
					config.Port = uint16(port)
				case "dbname":
					config.Database = o.Value
				case "user":
					config.User = o.Value
				case "password":
					config.Password = o.Value
				case "application_name":
					config.RuntimeParams[o.Name] = o.Value
				default:
					return nil, &stuntgres.ExternalSQLUnavailableError{Capability: "fixture option " + o.Name}
				}
			}
		}
	}
	config.Fallbacks = nil
	config.RuntimeParams["application_name"] = fmt.Sprintf("external_sql_fixture_%d", externalSQLApplication.Add(1))
	wire := &externalSQLWire{}
	config.OnNotification = func(_ *pgconn.PgConn, event *pgconn.Notification) {
		wire.events = append(wire.events, stuntgres.ExternalSQLNotification{Channel: event.Channel, BackendPID: int32(event.PID), Payload: event.Payload})
	}
	wire.conn, err = pgconn.ConnectConfig(ctx, config)
	if err != nil {
		return nil, externalSQLRemoteError(err)
	}
	wire.info = stuntgres.ExternalSQLConnectionInfo{BackendPID: int32(wire.conn.PID()), ProviderVersion: externalSQLFixtureVersion, CredentialMechanism: "unknown"}
	results, err := wire.Execute(ctx, "SELECT current_user,current_database()")
	if err != nil {
		return wire, err
	}
	if len(results) != 1 || results[0].RowCount != 1 || len(results[0].Cells) != 2 {
		return wire, errors.New("unexpected remote identity result")
	}
	wire.info.User, wire.info.Database = results[0].Cells[0].Text, results[0].Cells[1].Text
	p.mu.Lock()
	p.opened = append(p.opened, wire)
	hook := p.afterOpen
	wire.closeError, wire.mutateResult = p.closeError, p.mutateResult
	nilSuccess := p.nilSuccess
	request.ServerOptions = append([]stuntgres.ForeignOption(nil), request.ServerOptions...)
	request.MappingOptions = append([]stuntgres.ForeignOption(nil), request.MappingOptions...)
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	if hook != nil {
		return wire, hook(ctx, wire)
	}
	if nilSuccess {
		_ = wire.Close(context.WithoutCancel(ctx))
		return nil, nil
	}
	return wire, nil
}

func (w *externalSQLWire) Execute(ctx context.Context, q string) ([]stuntgres.ExternalSQLResult, error) {
	reader := w.conn.Exec(ctx, q)
	var out []stuntgres.ExternalSQLResult
	for reader.NextResult() {
		rows := reader.ResultReader()
		fields := rows.FieldDescriptions()
		result := stuntgres.ExternalSQLResult{Tuple: fields != nil}
		// pgconn ReadAll copies descriptors only on the first row, losing
		// empty-tuple metadata. Copy the live RowDescription before advancing
		// or closing its reader; both descriptors and row buffers are borrowed.
		result.Columns = make([]stuntgres.ExternalSQLColumn, len(fields))
		for i, field := range fields {
			result.Columns[i] = stuntgres.ExternalSQLColumn{Name: field.Name, TypeOID: field.DataTypeOID, TypeModifier: field.TypeModifier, Format: field.Format}
		}
		for rows.NextRow() {
			for _, cell := range rows.Values() {
				result.Cells = append(result.Cells, stuntgres.ExternalSQLCell{Text: string(cell), Null: cell == nil})
			}
			result.RowCount++
		}
		tag, err := rows.Close()
		if err != nil {
			_ = reader.Close()
			return nil, externalSQLRemoteError(err)
		}
		result.CommandTag, result.RowsAffected = tag.String(), tag.RowsAffected()
		result.Tuple = result.Tuple || tag.Select() || strings.HasPrefix(tag.String(), "FETCH ")
		out = append(out, result)
	}
	if err := reader.Close(); err != nil {
		return nil, externalSQLRemoteError(err)
	}
	if w.mutateResult != nil {
		w.mutateResult(out)
	}
	return out, nil
}
func (w *externalSQLWire) Cancel(ctx context.Context) error          { return w.conn.CancelRequest(ctx) }
func (w *externalSQLWire) Info() stuntgres.ExternalSQLConnectionInfo { return w.info }
func (w *externalSQLWire) Close(ctx context.Context) error {
	if err := w.conn.Close(ctx); err != nil {
		return err
	}
	return w.closeError
}
func (w *externalSQLWire) DrainNotifications(ctx context.Context) ([]stuntgres.ExternalSQLNotification, error) {
	if _, err := w.Execute(ctx, "SELECT 1"); err != nil {
		return nil, err
	}
	out := w.events
	w.events = nil
	return out, nil
}

func externalSQLRemoteError(err error) error {
	var p *pgconn.PgError
	if !errors.As(err, &p) {
		return err
	}
	position := func(n int32) string {
		if n == 0 {
			return ""
		}
		return strconv.FormatInt(int64(n), 10)
	}
	return &stuntgres.Error{Severity: p.Severity, SeverityUnlocalized: p.SeverityUnlocalized, Code: p.Code, Message: p.Message, Detail: p.Detail, Hint: p.Hint, Position: position(p.Position), InternalPosition: position(p.InternalPosition), InternalQuery: p.InternalQuery, Context: p.Where, Schema: p.SchemaName, Table: p.TableName, Column: p.ColumnName, DataType: p.DataTypeName, Constraint: p.ConstraintName}
}

func (p *externalSQLProvider) snapshot() []*externalSQLWire {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*externalSQLWire(nil), p.opened...)
}
