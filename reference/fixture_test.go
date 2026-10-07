//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"github.com/lib/pq"
	"github.com/ngrok/sqlmw"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"io"
	"os"
	"reflect"
	"strconv"
	"stuntgres"
	"sync"
	"testing"
	"time"
)

const referenceImage = "postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854"

var referenceContainer *postgres.PostgresContainer
var referenceAdmin *sql.DB
var referenceDSN, referenceContainerID string
var referenceMemoryDriver = stuntgres.NewDriver()

func TestMain(m *testing.M) {
	code := runReferenceMain(m)
	os.Exit(code)
}

func runReferenceMain(m *testing.M) (code int) {
	code = 1
	defer func() {
		if p := recover(); p != nil {
			fmt.Fprintf(os.Stderr, "reference startup/runtime panic: %v\n", p)
			code = 1
		}
		if referenceAdmin != nil {
			if err := referenceAdmin.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "reference close: %v\n", err)
				code = 1
			}
		}
		if referenceContainer != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			if err := referenceContainer.Terminate(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "reference terminate: %v\n", err)
				code = 1
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var err error
	referenceContainer, err = postgres.Run(ctx, referenceImage,
		postgres.WithDatabase("stuntgres_reference"), postgres.WithUsername("stuntgres"),
		postgres.WithPassword("stuntgres"), postgres.BasicWaitStrategies(),
		testcontainers.WithEnv(map[string]string{"POSTGRES_INITDB_ARGS": "--locale=C --encoding=UTF8"}))
	if err != nil {
		fmt.Fprintf(os.Stderr, "reference startup: %v\n", err)
		return
	}
	referenceContainerID = referenceContainer.GetContainerID()
	fmt.Fprintf(os.Stderr, "reference container: %s\n", referenceContainerID)
	referenceDSN, err = referenceContainer.ConnectionString(ctx, "sslmode=disable", "application_name=")
	if err != nil {
		fmt.Fprintf(os.Stderr, "reference connection string: %v\n", err)
		return
	}
	referenceAdmin, err = sql.Open("postgres", referenceDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reference open: %v\n", err)
		return
	}
	if err = referenceAdmin.PingContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "reference ping: %v\n", err)
		return
	}
	var version, encoding, locale string
	if err = referenceAdmin.QueryRowContext(ctx, "SELECT current_setting('server_version_num'), current_setting('server_encoding'), datcollate FROM pg_database WHERE datname = current_database()").Scan(&version, &encoding, &locale); err != nil {
		fmt.Fprintf(os.Stderr, "reference configuration: %v\n", err)
		return
	}
	if version != "180000" || encoding != "UTF8" || locale != "C" {
		fmt.Fprintf(os.Stderr, "reference configuration mismatch: version=%q encoding=%q locale=%q\n", version, encoding, locale)
		return
	}
	sql.Register("stuntgres_reference_memory", sqlmw.Driver(referenceMemoryDriver, referenceInterceptor{}))
	sql.Register("stuntgres_reference_postgres", sqlmw.Driver(&pq.Driver{}, referenceInterceptor{}))
	code = m.Run()
	return
}

type referencePair struct {
	memory, postgres, native     *sql.DB
	memoryRecord, postgresRecord *referenceRecorder
	ctx                          context.Context
}

func newReferencePair(t *testing.T) *referencePair {
	t.Helper()
	if referenceContainer.GetContainerID() != referenceContainerID {
		t.Fatal("reference container identity changed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	reset := func() {
		resetCtx, resetCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer resetCancel()
		if _, err := referenceAdmin.ExecContext(resetCtx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"); err != nil {
			t.Errorf("reference schema reset: %v", err)
		}
	}
	reset()
	if t.Failed() {
		t.FailNow()
	}
	p := &referencePair{ctx: ctx, memoryRecord: &referenceRecorder{}, postgresRecord: &referenceRecorder{}}
	var err error
	connector, err := referenceMemoryDriver.OpenConnector("reference/" + t.Name())
	if err != nil {
		t.Fatal(err)
	}
	p.native = sql.OpenDB(connector)
	p.memory, err = sql.Open("stuntgres_reference_memory", "reference/"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	p.postgres, err = sql.Open("stuntgres_reference_postgres", referenceDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, db := range []*sql.DB{p.memory, p.postgres, p.native} {
			if err := db.Close(); err != nil {
				t.Errorf("case close: %v", err)
			}
		}
		reset()
	})
	for _, db := range []*sql.DB{p.memory, p.postgres, p.native} {
		if err := db.PingContext(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

type referenceDiagnostic struct {
	Severity            string
	SeverityUnlocalized string
	Code                string
	Message             string
	Detail              string
	Hint                string
	Position            string
	InternalPosition    string
	InternalQuery       string
	Context             string
	Schema              string
	Table               string
	Column              string
	DataType            string
	Constraint          string
}

func referenceExtractDiagnostic(t *testing.T, err error) referenceDiagnostic {
	t.Helper()
	var native *stuntgres.Error
	if errors.As(err, &native) {
		got := referenceDiagnostic{
			Severity: native.Severity, SeverityUnlocalized: native.SeverityUnlocalized,
			Code: native.Code, Message: native.Message, Detail: native.Detail, Hint: native.Hint,
			Position: native.Position, InternalPosition: native.InternalPosition,
			InternalQuery: native.InternalQuery, Context: native.Context, Schema: native.Schema,
			Table: native.Table, Column: native.Column, DataType: native.DataType,
			Constraint: native.Constraint,
		}
		if native.File != "" || native.Line != "" || native.Routine != "" {
			t.Fatalf("native source fields must not claim PostgreSQL C locations: %#v", native)
		}
		if len(got.Code) != 5 || got.InternalPosition != "" && got.InternalQuery == "" {
			t.Fatalf("invalid native diagnostic contract: %#v", got)
		}
		return got
	}
	var postgres *pq.Error
	if errors.As(err, &postgres) {
		got := referenceDiagnostic{
			Severity: postgres.Severity, SeverityUnlocalized: postgres.Severity,
			Code: string(postgres.Code), Message: postgres.Message, Detail: postgres.Detail, Hint: postgres.Hint,
			Position: postgres.Position, InternalPosition: postgres.InternalPosition,
			InternalQuery: postgres.InternalQuery, Context: postgres.Where, Schema: postgres.Schema,
			Table: postgres.Table, Column: postgres.Column, DataType: postgres.DataTypeName,
			Constraint: postgres.Constraint,
		}
		if len(got.Code) != 5 || got.InternalPosition != "" && got.InternalQuery == "" {
			t.Fatalf("invalid PostgreSQL diagnostic contract: %#v", got)
		}
		return got
	}
	t.Fatalf("unknown SQL diagnostic %T: %v", err, err)
	return referenceDiagnostic{}
}

func referenceAssertDiagnostic(t *testing.T, err error, want *referenceDiagnostic) referenceDiagnostic {
	t.Helper()
	if want == nil {
		t.Fatal("diagnostic expectation is required for structured-error fixtures")
	}
	got := referenceExtractDiagnostic(t, err)
	if !reflect.DeepEqual(got, *want) {
		t.Fatalf("diagnostic got %#v want %#v", got, *want)
	}
	return got
}

type referenceStep struct {
	query              string
	args               []any
	prepared           bool
	selectRows         bool
	bag                bool
	affected           int64
	state              string
	diagnostic         *referenceDiagnostic
	postgresDiagnostic *referenceDiagnostic
	errorStage         string
	columns, types     []string
	rows               [][]any
}

// Each stream must independently match the expected occurrences. Bag results
// retain their observed values while canonicalizing order for backend parity.
func referenceCompareRows(values, expected [][]any, bag bool) ([][]any, bool) {
	if expected == nil {
		expected = make([][]any, 0)
	}
	if !bag {
		return values, reflect.DeepEqual(values, expected)
	}
	if len(values) != len(expected) {
		return values, false
	}
	ordered := make([][]any, len(expected))
	used := make([]bool, len(expected))
	for _, row := range values {
		found := false
		for i, want := range expected {
			if !used[i] && reflect.DeepEqual(row, want) {
				used[i] = true
				ordered[i] = row
				found = true
				break
			}
		}
		if !found {
			return values, false
		}
	}
	return ordered, true
}

func referenceRecordedRowsMatch(captured [][]any, step referenceStep) bool {
	_, matched := referenceCompareRows(captured, step.rows, step.bag)
	return matched
}

func referenceExec(query string, count int64, args ...any) referenceStep {
	return referenceStep{query: query, args: args, affected: count}
}
func referenceError(query, state string, args ...any) referenceStep {
	return referenceStep{query: query, args: args, state: state}
}
func referenceErrorDiagnostic(query string, want referenceDiagnostic, args ...any) referenceStep {
	return referenceStep{query: query, args: args, state: want.Code, diagnostic: &want}
}
func referenceImplicitError(query string, want referenceDiagnostic, args ...any) referenceStep {
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	return referenceErrorDiagnostic(query, want, args...)
}

func referencePosition(position int) string {
	if position == 0 {
		return ""
	}
	return strconv.Itoa(position)
}
func referenceSelect(query string, columns, types []string, rows [][]any, args ...any) referenceStep {
	return referenceStep{query: query, args: args, selectRows: true, columns: columns, types: types, rows: rows}
}
func referencePrepared(s referenceStep) referenceStep { s.prepared = true; return s }

type referenceObserved struct {
	state          string
	diagnostic     *referenceDiagnostic
	affected       int64
	columns, types []string
	rows           [][]any
	errorStage     string
}

func referenceState(t *testing.T, err error, want string) string {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("unexpected SQL error: %v", err)
		}
		return ""
	}
	if err == nil {
		t.Fatalf("expected SQLSTATE %s; got success", want)
	}
	var state interface{ SQLState() string }
	if !errors.As(err, &state) {
		t.Fatalf("expected SQLSTATE %s, got non-SQL error %T: %v", want, err, err)
	}
	if state.SQLState() != want {
		t.Fatalf("SQLSTATE got %s want %s: %v", state.SQLState(), want, err)
	}
	return state.SQLState()
}
func referenceCheckDiagnostic(t *testing.T, err error, s referenceStep, stage string) *referenceDiagnostic {
	t.Helper()
	if s.diagnostic == nil {
		return nil
	}
	want := s.diagnostic
	var native *stuntgres.Error
	if s.postgresDiagnostic != nil && !errors.As(err, &native) {
		want = s.postgresDiagnostic
	}
	got := referenceAssertDiagnostic(t, err, want)
	if s.errorStage != "" && stage != s.errorStage {
		t.Errorf("error stage got %q want %q", stage, s.errorStage)
	}
	// Assert the complete backend record first; normalize only the declared
	// position/context differences for the subsequent complete-record parity.
	if s.postgresDiagnostic != nil {
		got.Position = s.diagnostic.Position
		got.Context = s.diagnostic.Context
	}
	return &got
}

func referenceNormalize(v any) any {
	switch value := v.(type) {
	case time.Time:
		// pq and native may use different zero-offset Location pointers.
		// Preserve instant/civil precision while removing pointer identity.
		return value.Round(0).UTC()
	case []byte:
		return string(value)
	case int:
		return int64(value)
	case int32:
		return int64(value)
	case int16:
		return int64(value)
	case int8:
		return int64(value)
	default:
		return v
	}
}

type referenceExecutor interface {
	PrepareContext(context.Context, string) (*sql.Stmt, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func referenceRunStep(t *testing.T, ctx context.Context, db referenceExecutor, recorder *referenceRecorder, s referenceStep) referenceObserved {
	t.Helper()
	ctx = context.WithValue(ctx, referenceRecorderKey{}, recorder)
	var stmt *sql.Stmt
	var err error
	if s.prepared {
		stmt, err = db.PrepareContext(ctx, s.query)
		if err != nil {
			if stmt != nil {
				stmt.Close()
				t.Fatal("failed Prepare exposed a statement")
			}
			stage := "prepare"
			diag := referenceCheckDiagnostic(t, err, s, stage)
			return referenceObserved{state: referenceState(t, err, s.state), diagnostic: diag, errorStage: stage}
		}
		defer func() {
			if err := stmt.Close(); err != nil {
				t.Errorf("statement close: %v", err)
			}
		}()
	}
	if !s.selectRows {
		var result sql.Result
		if stmt != nil {
			result, err = stmt.ExecContext(ctx, s.args...)
		} else {
			result, err = db.ExecContext(ctx, s.query, s.args...)
		}
		state := referenceState(t, err, s.state)
		if err != nil {
			stage := "execute"
			diag := referenceCheckDiagnostic(t, err, s, stage)
			return referenceObserved{state: state, diagnostic: diag, errorStage: stage}
		}
		count, err := result.RowsAffected()
		if err != nil {
			t.Fatal(err)
		}
		if count != s.affected {
			t.Fatalf("RowsAffected got %d want %d", count, s.affected)
		}
		return referenceObserved{affected: count}
	}
	var rows *sql.Rows
	if stmt != nil {
		rows, err = stmt.QueryContext(ctx, s.args...)
	} else {
		rows, err = db.QueryContext(ctx, s.query, s.args...)
	}
	if rows != nil {
		// State/diagnostic assertions may Fatal even when Query succeeds.
		// Register cursor cleanup first; it must precede statement cleanup.
		defer rows.Close()
	}
	stage := "execute"
	state := referenceState(t, err, s.state)
	if err != nil {
		if rows != nil {
			t.Fatal("failed Query exposed a cursor")
		}
		diag := referenceCheckDiagnostic(t, err, s, stage)
		return referenceObserved{state: state, diagnostic: diag, errorStage: stage}
	}
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	types := make([]string, len(columnTypes))
	for i, typ := range columnTypes {
		types[i] = typ.DatabaseTypeName()
	}
	if !reflect.DeepEqual(columns, s.columns) {
		t.Fatalf("columns got %#v want %#v", columns, s.columns)
	}
	if !reflect.DeepEqual(types, s.types) {
		t.Fatalf("types got %#v want %#v", types, s.types)
	}
	values := make([][]any, 0)
	for rows.Next() {
		row := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, v := range row {
			row[i] = referenceNormalize(v)
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	compared, matched := referenceCompareRows(values, s.rows, s.bag)
	if !matched {
		t.Fatalf("rows bag=%v got %#v want %#v", s.bag, values, s.rows)
	}
	values = compared
	// Compare recorded consumed values, not middleware call counts or fast-path choices.
	recorder.mu.Lock()
	var recorded *referenceOperation
	for _, op := range recorder.operations {
		if op.SQL == s.query && op.Err == nil && op.Columns != nil {
			recorded = op
		}
	}
	if recorded == nil {
		recorder.mu.Unlock()
		t.Fatal("successful query has no recorded result")
	}
	captured := make([][]any, 0, len(recorded.Rows))
	for _, row := range recorded.Rows {
		converted := make([]any, len(row))
		for i, v := range row {
			converted[i] = referenceNormalize(v)
		}
		captured = append(captured, converted)
	}
	capturedColumns := make([]string, len(recorded.Columns))
	copy(capturedColumns, recorded.Columns)
	closed, closeErr := recorded.Closed, recorded.CloseErr
	recorder.mu.Unlock()
	if !referenceRecordedRowsMatch(captured, s) || !reflect.DeepEqual(capturedColumns, s.columns) {
		t.Fatalf("recorded query data columns=%#v rows=%#v", capturedColumns, captured)
	}
	if !closed || closeErr != nil {
		t.Fatalf("recorded cursor close: closed=%v error=%v", closed, closeErr)
	}
	return referenceObserved{columns: columns, types: types, rows: values}
}

func (p *referencePair) step(t *testing.T, s referenceStep) {
	t.Helper()
	var memory, postgres referenceObserved
	t.Run("memory", func(t *testing.T) { memory = referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, s) })
	t.Run("postgres", func(t *testing.T) { postgres = referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s) })
	if !t.Failed() && !reflect.DeepEqual(memory, postgres) {
		t.Fatalf("parity memory=%#v postgres=%#v", memory, postgres)
	}
}

type noticeReferenceDriver struct{ connector driver.Connector }

func (d noticeReferenceDriver) OpenConnector(string) (driver.Connector, error) {
	return d.connector, nil
}
func (d noticeReferenceDriver) Open(string) (driver.Conn, error) {
	return d.connector.Connect(context.Background())
}
func noticeReferencePool(t *testing.T, p *referencePair, postgres bool, records *[]referenceDiagnostic) (*sql.DB, *sql.Conn, *referenceRecorder) {
	t.Helper()
	var connector driver.Connector
	if postgres {
		base, err := pq.NewConnector(referenceDSN)
		if err != nil {
			t.Fatal(err)
		}
		connector = pq.ConnectorWithNoticeHandler(base, func(n *pq.Error) { *records = append(*records, referenceExtractDiagnostic(t, n)) })
	} else {
		base, err := referenceMemoryDriver.OpenConnector("reference/" + t.Name())
		if err != nil {
			t.Fatal(err)
		}
		connector = base.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { *records = append(*records, referenceExtractDiagnostic(t, &n)) })
	}
	wrapped := sqlmw.Driver(noticeReferenceDriver{connector}, referenceInterceptor{})
	// sqlmw's DriverContext connector preserves direct and prepared recorder hooks.
	configured, err := wrapped.(driver.DriverContext).OpenConnector("")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(configured)
	t.Cleanup(func() { db.Close() })
	c, err := db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return db, c, &referenceRecorder{}
}

func roleReferenceCleanup(t *testing.T, names []string) {
	t.Helper()
	// Registered before pair cleanup: physical handles and schema are gone first.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, name := range names {
			if _, err := referenceAdmin.ExecContext(ctx, "DROP ROLE IF EXISTS "+pq.QuoteIdentifier(name)); err != nil {
				t.Errorf("role cleanup %s: %v", name, err)
			}
		}
	})
}

func referenceTransactionRows(prepared bool, q string, values ...int64) referenceStep {
	rows := make([][]any, len(values))
	for i, v := range values {
		rows[i] = []any{v}
	}
	return referenceStep{query: q, prepared: prepared, selectRows: true, columns: []string{"v"}, types: []string{"INT4"}, rows: rows}
}
func referenceTransactionError(prepared bool, q string, want referenceDiagnostic) referenceStep {
	return referenceStep{query: q, prepared: prepared, state: want.Code, diagnostic: &want}
}
func referenceTransactionAborted() referenceDiagnostic {
	return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}
}
func referenceTransactionNull() referenceDiagnostic {
	return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "v"}
}
func referenceTransactionValues(t *testing.T, rows *sql.Rows, want ...int64) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"v"}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
		t.Fatal("metadata", types, err)
	}
	// As in referenceRunStep, assert SQL types and actual returned values;
	// ScanType is adapter metadata, not a shared PostgreSQL type contract.
	transactionRowValues(t, rows, want...)
}

func transactionRowValues(t *testing.T, rows *sql.Rows, want ...int64) {
	t.Helper()
	got := make([]int64, 0)
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		n, ok := v.(int64)
		if !ok {
			t.Fatalf("value type %T", v)
		}
		got = append(got, n)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if len(got) != len(want) {
		t.Fatalf("rows %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("rows %v want %v", got, want)
		}
	}
}

// A constraint witness must prepare successfully before execution fails.
func transactionConstraintExec(t *testing.T, ctx context.Context, x referenceExecutor, q string, prepared bool) error {
	t.Helper()
	if !prepared {
		_, err := x.ExecContext(ctx, q)
		return err
	}
	stmt, err := x.PrepareContext(ctx, q)
	if err != nil {
		t.Fatalf("constraint INSERT must prepare successfully: %v", err)
	}
	defer stmt.Close()
	_, err = stmt.ExecContext(ctx)
	return err
}

// Close rows before their retained prepared statement, including on failure.
func transactionQuery(ctx context.Context, x referenceExecutor, q string, prepared bool) (*sql.Rows, func() error, error) {
	if !prepared {
		rows, err := x.QueryContext(ctx, q)
		if err != nil {
			return nil, nil, err
		}
		return rows, rows.Close, nil
	}
	stmt, err := x.PrepareContext(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	rows, err := stmt.QueryContext(ctx)
	if err != nil {
		stmt.Close()
		return nil, nil, err
	}
	return rows, func() error { return errors.Join(rows.Close(), stmt.Close()) }, nil
}

func transactionOrdinary(t *testing.T, err error, message string) {
	t.Helper()
	if err == nil || err.Error() != message {
		t.Fatalf("ordinary error %v want %q", err, message)
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		t.Fatal("fabricated SQLSTATE", err)
	}
}

type referenceRecorderKey struct{}
type referenceRowsKey struct{}

type referenceOperation struct {
	SQL      string
	Args     []driver.NamedValue
	Affected int64
	Err      error
	Columns  []string
	Rows     [][]driver.Value
	Closed   bool
	CloseErr error
}

type referenceRecorder struct {
	mu         sync.Mutex
	operations []*referenceOperation
}

func referenceCopyValue(v driver.Value) driver.Value {
	if b, ok := v.([]byte); ok {
		return append([]byte(nil), b...)
	}
	return v
}

func (r *referenceRecorder) begin(query string, args []driver.NamedValue) *referenceOperation {
	op := &referenceOperation{SQL: query, Args: make([]driver.NamedValue, len(args))}
	for i, arg := range args {
		op.Args[i] = arg
		op.Args[i].Value = referenceCopyValue(arg.Value)
	}
	r.mu.Lock()
	r.operations = append(r.operations, op)
	r.mu.Unlock()
	return op
}

func referenceBegin(ctx context.Context, query string, args []driver.NamedValue) (*referenceRecorder, *referenceOperation) {
	r, _ := ctx.Value(referenceRecorderKey{}).(*referenceRecorder)
	if r == nil {
		return nil, nil
	}
	return r, r.begin(query, args)
}

func referenceRecordExec(r *referenceRecorder, op *referenceOperation, result driver.Result, err error) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	op.Err = err
	if err == nil && result != nil {
		op.Affected, op.Err = result.RowsAffected()
	}
}

func referenceRecordQuery(ctx context.Context, r *referenceRecorder, op *referenceOperation, rows driver.Rows, err error) context.Context {
	if r == nil {
		return ctx
	}
	r.mu.Lock()
	op.Err = err
	if err == nil && rows != nil {
		// A successful zero-column cursor is distinct from no query result.
		columns := rows.Columns()
		op.Columns = make([]string, len(columns))
		copy(op.Columns, columns)
	}
	r.mu.Unlock()
	if err != nil {
		return ctx
	}
	return context.WithValue(ctx, referenceRowsKey{}, op)
}

type referenceInterceptor struct{ sqlmw.NullInterceptor }

func (referenceInterceptor) ConnExecContext(ctx context.Context, conn driver.ExecerContext, query string, args []driver.NamedValue) (driver.Result, error) {
	r, op := referenceBegin(ctx, query, args)
	result, err := conn.ExecContext(ctx, query, args)
	referenceRecordExec(r, op, result, err)
	return result, err
}
func (referenceInterceptor) StmtExecContext(ctx context.Context, stmt driver.StmtExecContext, query string, args []driver.NamedValue) (driver.Result, error) {
	r, op := referenceBegin(ctx, query, args)
	result, err := stmt.ExecContext(ctx, args)
	referenceRecordExec(r, op, result, err)
	return result, err
}
func (referenceInterceptor) ConnQueryContext(ctx context.Context, conn driver.QueryerContext, query string, args []driver.NamedValue) (context.Context, driver.Rows, error) {
	r, op := referenceBegin(ctx, query, args)
	rows, err := conn.QueryContext(ctx, query, args)
	return referenceRecordQuery(ctx, r, op, rows, err), rows, err
}
func (referenceInterceptor) StmtQueryContext(ctx context.Context, stmt driver.StmtQueryContext, query string, args []driver.NamedValue) (context.Context, driver.Rows, error) {
	r, op := referenceBegin(ctx, query, args)
	rows, err := stmt.QueryContext(ctx, args)
	return referenceRecordQuery(ctx, r, op, rows, err), rows, err
}
func (referenceInterceptor) RowsNext(ctx context.Context, rows driver.Rows, dest []driver.Value) error {
	err := rows.Next(dest)
	r, _ := ctx.Value(referenceRecorderKey{}).(*referenceRecorder)
	op, _ := ctx.Value(referenceRowsKey{}).(*referenceOperation)
	if r != nil && op != nil {
		r.mu.Lock()
		if err == nil {
			values := make([]driver.Value, len(dest))
			for i, v := range dest {
				values[i] = referenceCopyValue(v)
			}
			op.Rows = append(op.Rows, values)
		} else if err != io.EOF {
			op.Err = err
		}
		r.mu.Unlock()
	}
	return err
}
func (referenceInterceptor) RowsClose(ctx context.Context, rows driver.Rows) error {
	err := rows.Close()
	r, _ := ctx.Value(referenceRecorderKey{}).(*referenceRecorder)
	op, _ := ctx.Value(referenceRowsKey{}).(*referenceOperation)
	if r != nil && op != nil {
		r.mu.Lock()
		op.Closed = true
		op.CloseErr = err
		r.mu.Unlock()
	}
	return err
}

func roleReferenceError(query, code, message, detail string, prepared bool, stage string) referenceStep {
	s := referenceErrorDiagnostic(query, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail})
	s.prepared = prepared
	s.errorStage = "execute"
	if prepared {
		s.errorStage = stage
	}
	return s
}

type controlsReferenceFixture struct {
	pair     *referencePair
	db       *sql.DB
	a, b     *sql.Conn
	notices  *[]referenceDiagnostic
	recorder *referenceRecorder
}

func newControlsReferenceFixture(t *testing.T, pair *referencePair, postgres bool) controlsReferenceFixture {
	t.Helper()
	notices := new([]referenceDiagnostic)
	db, a, recorder := noticeReferencePool(t, pair, postgres, notices)
	// The peer comes from this exact notice connector/DSN, not the parent
	// pair: subtest names otherwise select a different embedded engine.
	b, err := db.Conn(pair.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.ExecContext(context.Background(), "ROLLBACK"); b.Close() })
	return controlsReferenceFixture{pair: pair, db: db, a: a, b: b, notices: notices, recorder: recorder}
}

func controlsReferenceDiagnostic(code, message string) referenceDiagnostic {
	return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func (f controlsReferenceFixture) noticesWant(t *testing.T, code, message string) {
	t.Helper()
	var want []referenceDiagnostic
	if code != "" {
		n := controlsReferenceDiagnostic(code, message)
		n.Severity, n.SeverityUnlocalized = "WARNING", "WARNING"
		want = []referenceDiagnostic{n}
	}
	if len(*f.notices) != len(want) || (len(want) > 0 && !reflect.DeepEqual(*f.notices, want)) {
		t.Fatalf("notices got %#v want %#v", *f.notices, want)
	}
	*f.notices = nil
}

func (f controlsReferenceFixture) exec(t *testing.T, x referenceExecutor, query string, prepared bool, count int64) {
	t.Helper()
	referenceRunStep(t, f.pair.ctx, x, f.recorder, referenceStep{query: query, prepared: prepared, affected: count})
}
func (f controlsReferenceFixture) rows(t *testing.T, x referenceExecutor, values ...int64) {
	t.Helper()
	referenceRunStep(t, f.pair.ctx, x, f.recorder, referenceTransactionRows(false, "SELECT v FROM t ORDER BY v", values...))
}
func isolationReferenceConnections(t *testing.T, postgres bool) (*sql.DB, *sql.Conn, *sql.Conn) {
	t.Helper()
	pair := newReferencePair(t)
	db := pair.memory
	if postgres {
		db = pair.postgres
	}
	a, err := db.Conn(pair.ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.Conn(pair.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		a.ExecContext(context.Background(), "ROLLBACK")
		b.ExecContext(context.Background(), "ROLLBACK")
		a.Close()
		b.Close()
	})
	return db, a, b
}
func isolationReferenceAffected(t *testing.T, ctx context.Context, x referenceExecutor, query string, prepared bool, want int64) {
	t.Helper()
	referenceRunStep(t, ctx, x, &referenceRecorder{}, referenceStep{query: query, prepared: prepared, affected: want})
}
func isolationReferenceSelect(t *testing.T, ctx context.Context, x referenceExecutor, query string, prepared bool, want ...int64) {
	t.Helper()
	referenceRunStep(t, ctx, x, &referenceRecorder{}, referenceTransactionRows(prepared, query, want...))
}
func isolationReferenceWant(code, message string) referenceDiagnostic {
	return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}
func isolationReferenceDiagnostic(t *testing.T, err error, want referenceDiagnostic) {
	t.Helper()
	referenceAssertDiagnostic(t, err, &want)
}
