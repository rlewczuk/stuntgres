package fixture

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgconn"

	"stuntgres"
)

const ExternalSQLFixtureVersion = "pgconn-fixture-1"

// Network, libpq-compatible parameter parsing and authentication are test-host
// responsibilities. No transport code below is part of the embedded engine.
type ExternalSQLProvider struct {
	mu        sync.Mutex
	opened    []*ExternalSQLWire
	faults    ExternalSQLFaults
	requests  []stuntgres.ExternalSQLConnectRequest
	openCalls atomic.Uint64
}

// OpenCalls observes entry even when real host parsing rejects before dialing.
func (p *ExternalSQLProvider) OpenCalls() uint64 { return p.openCalls.Load() }

type ExternalSQLWire struct {
	closeError    error
	mutateResult  func([]stuntgres.ExternalSQLResult)
	conn          *pgconn.PgConn
	info          stuntgres.ExternalSQLConnectionInfo
	events        []stuntgres.ExternalSQLNotification // Command serialization owns input.
	executions    atomic.Uint64
	cancellations atomic.Uint64
	closes        atomic.Uint64
}

func (p *ExternalSQLProvider) Open(ctx context.Context, request stuntgres.ExternalSQLConnectRequest) (stuntgres.ExternalSQLConnection, error) {
	p.openCalls.Add(1)
	if request.ConninfoPresent && (request.ServerOptions != nil || request.MappingOptions != nil) {
		return nil, errors.New("fixture: conninfo and options are mutually exclusive")
	}
	if !request.ConninfoPresent && request.Conninfo != "" {
		return nil, errors.New("fixture: conninfo requires presence")
	}
	dsn := request.Conninfo
	if !request.ConninfoPresent {
		// Structured FDW requests explicitly use this host fixture's defaults.
		// Present empty conninfo instead reaches pgconn's actual environment.
		dsn = DSN
	}
	config, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if !request.ConninfoPresent {
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
				case "application_name", "client_encoding", "options":
					config.RuntimeParams[o.Name] = o.Value
				case "fallback_application_name":
					if config.RuntimeParams["application_name"] == "" {
						config.RuntimeParams["application_name"] = o.Value
					}
				default:
					return nil, &stuntgres.ExternalSQLUnavailableError{Capability: "fixture option " + o.Name}
				}
			}
		}
	}
	config.Fallbacks = nil
	wire := &ExternalSQLWire{}
	config.OnNotification = func(_ *pgconn.PgConn, event *pgconn.Notification) {
		wire.events = append(wire.events, stuntgres.ExternalSQLNotification{Channel: event.Channel, BackendPID: int32(event.PID), Payload: event.Payload})
	}
	observation := &externalSQLAuthObservation{}
	config.AfterNetConnect = func(_ context.Context, _ *pgconn.Config, conn net.Conn) (net.Conn, error) {
		return &externalSQLAuthConn{Conn: conn, observation: observation}, nil
	}
	wire.conn, err = pgconn.ConnectConfig(ctx, config)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		var response *pgconn.PgError
		if errors.As(err, &response) {
			return nil, externalSQLRemoteError(err)
		}
		return nil, &stuntgres.ExternalSQLConnectError{Err: err}
	}
	wire.info = stuntgres.ExternalSQLConnectionInfo{BackendPID: int32(wire.conn.PID()), ProviderVersion: ExternalSQLFixtureVersion, CredentialMechanism: observation.mechanism()}
	results, err := wire.Execute(ctx, "SELECT current_user,current_database()")
	if err != nil {
		return wire, err
	}
	if len(results) != 1 || results[0].RowCount != 1 || len(results[0].Cells) != 2 {
		return wire, errors.New("unexpected remote identity result")
	}
	wire.info.User, wire.info.Database = results[0].Cells[0].Text, results[0].Cells[1].Text
	p.mu.Lock()
	if p.faults.OmitCredentialEvidence {
		wire.info.CredentialMechanism = "unknown"
	}
	p.opened = append(p.opened, wire)
	hook := p.faults.AfterOpen
	wire.closeError, wire.mutateResult = p.faults.CloseError, p.faults.MutateResult
	nilSuccess := p.faults.NilSuccess
	request.Conninfo, request.ServerOptions, request.MappingOptions = "", nil, nil
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

func (w *ExternalSQLWire) Execute(ctx context.Context, q string) ([]stuntgres.ExternalSQLResult, error) {
	w.executions.Add(1)
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
func (w *ExternalSQLWire) Cancel(ctx context.Context) error {
	w.cancellations.Add(1)
	return w.conn.CancelRequest(ctx)
}
func (w *ExternalSQLWire) Info() stuntgres.ExternalSQLConnectionInfo { return w.info }
func (w *ExternalSQLWire) Close(ctx context.Context) error {
	w.closes.Add(1)
	if err := w.conn.Close(ctx); err != nil {
		return err
	}
	return w.closeError
}
func (w *ExternalSQLWire) DrainNotifications(ctx context.Context) ([]stuntgres.ExternalSQLNotification, error) {
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

func (p *ExternalSQLProvider) Snapshot() []*ExternalSQLWire {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*ExternalSQLWire(nil), p.opened...)
}

// ExternalSQLFaults changes outcomes only after a genuine connection was opened.
// ConfigureFaults serializes changes with Open; callbacks must return errors,
// never call testing.Fatal or Goexit while an engine operation owns them.
type ExternalSQLFaults struct {
	AfterOpen    func(context.Context, *ExternalSQLWire) error
	NilSuccess   bool
	CloseError   error
	MutateResult func([]stuntgres.ExternalSQLResult)
	// Withhold attestation, never invent an authentication success/mechanism.
	OmitCredentialEvidence bool
}

func (p *ExternalSQLProvider) ConfigureFaults(update func(*ExternalSQLFaults)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	update(&p.faults)
}

// LastRequestIdentity exposes authority observations, never conninfo or secrets.
func (p *ExternalSQLProvider) LastRequestIdentity() stuntgres.ExternalSQLConnectRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		return stuntgres.ExternalSQLConnectRequest{}
	}
	r := p.requests[len(p.requests)-1]
	r.Conninfo, r.ServerOptions, r.MappingOptions = "", nil, nil
	return r
}

// ExternalSQLActivity counts calls on the actual returned transport. Together
// with retained Execute errors and independent PID exit it exposes unexpected
// replay/control I/O without replacing the physical connection witness.
type ExternalSQLActivity struct{ Executions, Cancellations, Closes uint64 }

func (w *ExternalSQLWire) Activity() ExternalSQLActivity {
	return ExternalSQLActivity{w.executions.Load(), w.cancellations.Load(), w.closes.Load()}
}
