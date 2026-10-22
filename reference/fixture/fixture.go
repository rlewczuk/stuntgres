package fixture

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"flag"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/lib/pq"
	"github.com/ngrok/sqlmw"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"io"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"stuntgres"
	"sync"
	"testing"
	"time"
)

const referenceImage = "postgres:18.6-bookworm@sha256:afc7e2d441324c0388fa80c3d24f733b4194a4eb7f47dd8ee2b08eb1a24a647c"

var Container *postgres.PostgresContainer
var Admin *sql.DB
var DSN, ContainerID string
var referenceMemoryDriver = stuntgres.NewDriver()
var Mode = flag.Bool("reference", false, "run migrated scenarios against real pinned PostgreSQL as well as native")
var referenceStartup sync.Once
var referenceStartupErr error
var Started bool

// StartupOption configures the package-owned oracle before its first startup.
type StartupOption func()

var trackCommitTimestamp bool

// WithCommitTimestampTracking enables tracking on the package's single oracle.
func WithCommitTimestampTracking() StartupOption {
	return func() { trackCommitTimestamp = true }
}

// Tests run serially; one test can create multiple pairs before its cleanup.

func Main(m *testing.M, options ...StartupOption) (code int) {
	for _, option := range options {
		option()
	}
	defer func() {
		if err := cleanupServerBuild(); err != nil {
			fmt.Fprintf(os.Stderr, "server fixture binary cleanup: %v\n", err)
			code = 1
		}
	}()
	return runReferenceMain(m)
}

func runReferenceMain(m *testing.M) (code int) {
	code = 1
	defer func() {
		if p := recover(); p != nil {
			fmt.Fprintf(os.Stderr, "reference startup/runtime panic: %v\n", p)
			code = 1
		}
		if err := referenceReleaseResources(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
		}
	}()
	flag.Parse()
	sql.Register("stuntgres_reference_memory", sqlmw.Driver(referenceMemoryDriver, Interceptor{}))
	sql.Register("stuntgres_reference_postgres", sqlmw.Driver(&pq.Driver{}, Interceptor{}))
	if *Mode {
		if err := EnsureStarted(); err != nil {
			fmt.Fprintf(os.Stderr, "reference startup: %v\n", err)
			return
		}
	}
	code = m.Run()
	if Admin != nil && referenceStartupErr == nil {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if _, err := Admin.ExecContext(cleanupCtx, "DROP OWNED BY postgres CASCADE; DROP ROLE postgres"); err != nil {
			fmt.Fprintf(os.Stderr, "reference native bootstrap cleanup: %v\n", err)
			code = 1
		}
	}
	return
}

type Pair struct {
	Memory, Postgres, Native *sql.DB
	// CopyPostgres is an unwrapped, genuine stream transport on the same oracle.
	// Ordinary SQL middleware and pq coverage continue to use Postgres.
	CopyPostgres                 *sql.DB
	MemoryRecord, PostgresRecord *Recorder
	Ctx                          context.Context
	NativeConnector              *stuntgres.Connector
}

func NewPair(t *testing.T) *Pair {
	t.Helper()
	return NewFixture(t, true, false)
}

func NewFixture(t *testing.T, withPostgres, nativeRaw bool) *Pair {
	t.Helper()
	return newFixture(t, withPostgres, nativeRaw, nil)
}

func newFixture(t *testing.T, withPostgres, nativeRaw bool, configure func(*stuntgres.Driver) error) *Pair {
	t.Helper()
	if withPostgres {
		if err := EnsureStarted(); err != nil {
			t.Fatalf("reference startup: %v", err)
		}
		if Container.GetContainerID() != ContainerID {
			t.Fatal("reference container identity changed")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	reset := func() {
		if !withPostgres {
			return
		}
		resetCtx, resetCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer resetCancel()
		// Serialized fixtures return every borrowed admin handle before reset.
		// Rollback and DISCARD must be separate executions: DISCARD ALL cannot
		// run inside a transaction block, including an aborted replay.
		conn, err := Admin.Conn(resetCtx)
		if err != nil {
			t.Errorf("reference reset connection: %v", err)
			return
		}
		defer func() {
			if err := conn.Close(); err != nil {
				t.Errorf("reference reset connection close: %v", err)
			}
		}()
		for _, query := range []string{"ROLLBACK", "DISCARD ALL", "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"} {
			if _, err := conn.ExecContext(resetCtx, query); err != nil {
				t.Errorf("reference schema reset %s: %v", query, err)
				return
			}
		}
	}
	reset()
	if t.Failed() {
		t.FailNow()
	}
	p := &Pair{Ctx: ctx, MemoryRecord: &Recorder{}, PostgresRecord: &Recorder{}}
	if nativeRaw {
		p.MemoryRecord.RawBackend = RawNative
		p.PostgresRecord.RawBackend = RawPostgres
	}
	t.Cleanup(func() {
		for _, db := range []*sql.DB{p.CopyPostgres, p.Memory, p.Postgres, p.Native} {
			if db != nil {
				if err := db.Close(); err != nil {
					t.Errorf("case close: %v", err)
				}
			}
		}
		reset()
	})
	nativeDriver := stuntgres.NewDriverWithConfig(stuntgres.DriverConfig{TrackCommitTimestamp: trackCommitTimestamp})
	if configure != nil {
		if err := configure(nativeDriver); err != nil {
			t.Fatal(err)
		}
	}
	connector, err := nativeDriver.OpenConnector("reference/" + t.Name())
	if err != nil {
		t.Fatal(err)
	}
	p.NativeConnector = connector.(*stuntgres.Connector)
	p.Native = sql.OpenDB(connector)
	wrapped := sqlmw.Driver(NoticeReferenceDriver{connector}, Interceptor{})
	configured, err := wrapped.(driver.DriverContext).OpenConnector("")
	if err != nil {
		t.Fatal(err)
	}
	p.Memory = sql.OpenDB(configured)
	if withPostgres {
		p.Postgres, err = sql.Open("stuntgres_reference_postgres", DSN)
		if err != nil {
			t.Fatal(err)
		}
		config, configErr := pgx.ParseConfig(DSN)
		if configErr != nil {
			t.Fatal(configErr)
		}
		p.CopyPostgres = sql.OpenDB(stdlib.GetConnector(*config))
	}
	for _, db := range []*sql.DB{p.Memory, p.Postgres, p.Native} {
		if db != nil {
			if err := db.PingContext(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	return p
}

type Diagnostic struct {
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

func ExtractDiagnostic(t *testing.T, err error) Diagnostic {
	t.Helper()
	var native *stuntgres.Error
	if errors.As(err, &native) {
		got := Diagnostic{
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
	var stream *pgconn.PgError
	if errors.As(err, &stream) {
		position := func(n int32) string {
			if n == 0 {
				return ""
			}
			return strconv.FormatInt(int64(n), 10)
		}
		got := Diagnostic{
			Severity: stream.Severity, SeverityUnlocalized: stream.SeverityUnlocalized,
			Code: stream.Code, Message: stream.Message, Detail: stream.Detail, Hint: stream.Hint,
			Position: position(stream.Position), InternalPosition: position(stream.InternalPosition),
			InternalQuery: stream.InternalQuery, Context: stream.Where, Schema: stream.SchemaName,
			Table: stream.TableName, Column: stream.ColumnName, DataType: stream.DataTypeName,
			Constraint: stream.ConstraintName,
		}
		if len(got.Code) != 5 || got.InternalPosition != "" && got.InternalQuery == "" {
			t.Fatalf("invalid PostgreSQL stream diagnostic contract: %#v", got)
		}
		return got
	}
	var postgres *pq.Error
	if errors.As(err, &postgres) {
		got := Diagnostic{
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
	return Diagnostic{}
}

func AssertDiagnostic(t *testing.T, err error, want *Diagnostic) Diagnostic {
	t.Helper()
	if want == nil {
		t.Fatal("diagnostic expectation is required for structured-error fixtures")
	}
	got := ExtractDiagnostic(t, err)
	if !reflect.DeepEqual(got, *want) {
		t.Fatalf("diagnostic got %#v want %#v", got, *want)
	}
	return got
}

type Step struct {
	Query    string
	Args     []any
	Prepared bool
	// nonContext preserves original DB/Tx and statement consumer methods.
	// The recorded Context companion remains a separate execution.
	NonContext         bool
	SelectRows         bool
	Bag                bool
	Affected           int64
	State              string
	Diagnostic         *Diagnostic
	PostgresDiagnostic *Diagnostic
	ErrorStage         string
	Columns, Types     []string
	Rows               [][]any
	// rawTypes declares codec families separately from catalog names, for example
	// a user array whose lib/pq DatabaseTypeName is empty.
	RawTypes []string
	// checkMetadata inspects the actual cursor before iteration, including empty results.
	CheckMetadata func(t *testing.T, rows *sql.Rows)
	// checkRaw independently validates each any-scan and middleware-owned value
	// before normalization. Return true only for columns whose complete raw
	// contract the callback checks; false preserves generic validation.
	// SQL NULL is still checked by each stream's complete row comparison.
	CheckRaw func(t *testing.T, columnIndex, rowIndex int, value any) bool
	// checkScan rescans the current row into specialized destinations, never SQL.
	CheckScan func(t *testing.T, rows *sql.Rows, rowIndex int)
}

// Each stream must independently match the expected occurrences. Bag results
// retain their observed values while canonicalizing order for backend parity.
func CompareRows(values, expected [][]any, bag bool) ([][]any, bool) {
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

func RecordedRowsMatch(captured [][]any, step Step) bool {
	_, matched := CompareRows(captured, step.Rows, step.Bag)
	return matched
}

func Exec(query string, count int64, args ...any) Step {
	return Step{Query: query, Args: args, Affected: count}
}
func Error(query, state string, args ...any) Step {
	return Step{Query: query, Args: args, State: state}
}
func ErrorDiagnostic(query string, want Diagnostic, args ...any) Step {
	return Step{Query: query, Args: args, State: want.Code, Diagnostic: &want}
}
func ImplicitError(query string, want Diagnostic, args ...any) Step {
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	return ErrorDiagnostic(query, want, args...)
}

func Position(position int) string {
	if position == 0 {
		return ""
	}
	return strconv.Itoa(position)
}
func Select(query string, columns, types []string, rows [][]any, args ...any) Step {
	return Step{Query: query, Args: args, SelectRows: true, Columns: columns, Types: types, Rows: rows}
}
func Prepared(s Step) Step { s.Prepared = true; return s }

type Observed struct {
	state          string
	Diagnostic     *Diagnostic
	affected       int64
	Columns, types []string
	Rows           [][]any
	ErrorStage     string
}

func State(t *testing.T, err error, want string) string {
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
func CheckDiagnostic(t *testing.T, err error, s Step, stage string) *Diagnostic {
	t.Helper()
	if s.Diagnostic == nil {
		return nil
	}
	want := s.Diagnostic
	var native *stuntgres.Error
	if s.PostgresDiagnostic != nil && !errors.As(err, &native) {
		want = s.PostgresDiagnostic
	}
	got := AssertDiagnostic(t, err, want)
	if s.ErrorStage != "" && stage != s.ErrorStage {
		t.Errorf("error stage got %q want %q", stage, s.ErrorStage)
	}
	// Assert the complete backend record first; normalize only the declared
	// position/context differences for the subsequent complete-record parity.
	if s.PostgresDiagnostic != nil {
		got.Position = s.Diagnostic.Position
		got.Context = s.Diagnostic.Context
	}
	return &got
}

func Normalize(v any) any {
	switch value := v.(type) {
	case time.Time:
		// pq and native may use different zero-offset Location pointers.
		// Preserve instant/civil precision while removing pointer identity.
		return value.Round(0).UTC()
	case []byte:
		return string(value)
	default:
		return v
	}
}

// Check public any-scan representations before the existing value normalization.
// These codec contracts are independent of ColumnType.ScanType descriptors.
func AssertRawValue(t *testing.T, postgres bool, sqlType string, value any) {
	t.Helper()
	if value == nil {
		return // The complete row comparison independently requires SQL NULL.
	}
	var want reflect.Type
	switch sqlType {
	case "INT2", "INT4", "INT8":
		want = reflect.TypeFor[int64]()
	case "OID":
		want = reflect.TypeFor[int64]()
		if postgres {
			want = reflect.TypeFor[[]byte]()
		}
	case "FLOAT4", "FLOAT8":
		want = reflect.TypeFor[float64]()
	case "BOOL":
		want = reflect.TypeFor[bool]()
	case "TEXT", "CHAR", "BPCHAR", "VARCHAR":
		// pq textDecode returns BPCHAR as string despite its any ScanType.
		want = reflect.TypeFor[string]()
	case "MACADDR", "TID", "INET", "CIDR", "INT4RANGE", "BIT", "VARBIT", "NUMERIC", "MONEY", "UUID", "NAME", "INTERVAL", "REGCLASS", "REGPROC", "REGROLE", "REGOPER", "REGOPERATOR", "REGCOLLATION", "REGNAMESPACE", "VOID", "_NAME", "PG_NODE_TREE", "JSON", "JSONB", "JSONPATH", "ACLITEM", "RECORD", "TSVECTOR", "TSQUERY":
		want = reflect.TypeFor[string]()
		if postgres {
			want = reflect.TypeFor[[]byte]()
		}
	case "DATE":
		want = reflect.TypeFor[string]()
		if postgres {
			want = reflect.TypeFor[time.Time]()
			if encoded, ok := value.([]byte); ok && (string(encoded) == "infinity" || string(encoded) == "-infinity") {
				want = reflect.TypeFor[[]byte]()
			}
		}
	case "TIME", "TIMETZ":
		want = reflect.TypeFor[string]()
		if postgres {
			want = reflect.TypeFor[time.Time]()
		}
	case "TIMESTAMP", "TIMESTAMPTZ":
		want = reflect.TypeFor[time.Time]()
		if encoded, ok := value.([]byte); ok {
			switch string(encoded) {
			case "infinity", "-infinity":
				want = reflect.TypeFor[[]byte]()
			}
		}
	case "BYTEA":
		want = reflect.TypeFor[[]byte]()
	case "_BOOL", "_BYTEA", "_INT2", "_INT4", "_INT8", "_TEXT", "_OID", "_BPCHAR", "_VARCHAR", "_FLOAT4", "_FLOAT8", "_NUMERIC", "_DATE", "_TIME", "_TIMETZ", "_TIMESTAMP", "_TIMESTAMPTZ", "_INTERVAL", "_UUID", "_REGCLASS", "_REGROLE", "_JSON", "_JSONB", "_JSONPATH", "_ENUM", "_ACLITEM", "_RECORD":
		want = reflect.TypeFor[string]()
		if postgres {
			want = reflect.TypeFor[[]byte]()
		}
	default:
		t.Fatalf("unspecified raw representation for SQL type %s", sqlType)
	}
	if reflect.TypeOf(value) != want {
		t.Fatalf("raw %s postgres=%v got %T %#v want %v", sqlType, postgres, value, value, want)
	}
	if bytes, ok := value.([]byte); ok && bytes == nil {
		t.Fatalf("raw %s got typed nil bytes rather than SQL NULL or a nonnil payload", sqlType)
	}
}

// Use the same raw contract for the actual scan and the independently owned
// middleware stream. Neither path may validate only normalized representations.
func referenceAssertStepRawValue(t *testing.T, recorder *Recorder, s Step, column, row int, value any) {
	t.Helper()
	if s.CheckRaw != nil && s.CheckRaw(t, column, row, value) {
		return
	}
	if recorder.RawBackend != RawLegacy {
		sqlType := s.Types[column]
		if s.RawTypes != nil {
			sqlType = s.RawTypes[column]
		}
		AssertRawValue(t, recorder.RawBackend == RawPostgres, sqlType, value)
	}
}

type Executor interface {
	PrepareContext(context.Context, string) (*sql.Stmt, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// referenceNonContextExecutor is satisfied by sql.DB and sql.Tx, not sql.Conn.
type NonContextExecutor interface {
	Prepare(string) (*sql.Stmt, error)
	Exec(string, ...any) (sql.Result, error)
	Query(string, ...any) (*sql.Rows, error)
}

// HeldStatement routes fixture execution through an already prepared
// statement while retaining the shared metadata, values and middleware checks.
type HeldStatement struct {
	Executor
	Statement *sql.Stmt
	SQL       string
}

func (s HeldStatement) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if query != s.SQL {
		return s.Executor.ExecContext(ctx, query, args...)
	}
	return s.Statement.ExecContext(ctx, args...)
}

func (s HeldStatement) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if query != s.SQL {
		return s.Executor.QueryContext(ctx, query, args...)
	}
	return s.Statement.QueryContext(ctx, args...)
}

func RunStep(t *testing.T, ctx context.Context, db Executor, recorder *Recorder, s Step) Observed {
	t.Helper()
	var source NonContextExecutor
	if s.NonContext {
		var ok bool
		source, ok = db.(NonContextExecutor)
		if !ok {
			t.Fatalf("non-Context source route requires DB or Tx methods, got %T", db)
		}
	} else {
		ctx = context.WithValue(ctx, RecorderKey{}, recorder)
	}
	var stmt *sql.Stmt
	var err error
	if s.Prepared {
		if s.NonContext {
			stmt, err = source.Prepare(s.Query)
		} else {
			stmt, err = db.PrepareContext(ctx, s.Query)
		}
		if err != nil {
			if stmt != nil {
				stmt.Close()
				t.Fatal("failed Prepare exposed a statement")
			}
			stage := "prepare"
			diag := CheckDiagnostic(t, err, s, stage)
			return Observed{state: State(t, err, s.State), Diagnostic: diag, ErrorStage: stage}
		}
		defer func() {
			if err := stmt.Close(); err != nil {
				t.Errorf("statement close: %v", err)
			}
		}()
	}
	if !s.SelectRows {
		var result sql.Result
		if stmt != nil {
			if s.NonContext {
				result, err = stmt.Exec(s.Args...)
			} else {
				result, err = stmt.ExecContext(ctx, s.Args...)
			}
		} else if s.NonContext {
			result, err = source.Exec(s.Query, s.Args...)
		} else {
			result, err = db.ExecContext(ctx, s.Query, s.Args...)
		}
		state := State(t, err, s.State)
		if err != nil {
			if result != nil {
				t.Fatal("failed Exec exposed a result")
			}
			stage := "execute"
			diag := CheckDiagnostic(t, err, s, stage)
			return Observed{state: state, Diagnostic: diag, ErrorStage: stage}
		}
		count, err := result.RowsAffected()
		if err != nil {
			t.Fatal(err)
		}
		if count != s.Affected {
			t.Fatalf("RowsAffected got %d want %d", count, s.Affected)
		}
		if _, err := result.LastInsertId(); err == nil {
			t.Fatal("LastInsertId unexpectedly supported")
		}
		return Observed{affected: count}
	}
	var rows *sql.Rows
	if stmt != nil {
		if s.NonContext {
			rows, err = stmt.Query(s.Args...)
		} else {
			rows, err = stmt.QueryContext(ctx, s.Args...)
		}
	} else if s.NonContext {
		rows, err = source.Query(s.Query, s.Args...)
	} else {
		rows, err = db.QueryContext(ctx, s.Query, s.Args...)
	}
	if rows != nil {
		// State/diagnostic assertions may Fatal even when Query succeeds.
		// Register cursor cleanup first; it must precede statement cleanup.
		defer rows.Close()
	}
	stage := "execute"
	state := State(t, err, s.State)
	if err != nil {
		if rows != nil {
			t.Fatal("failed Query exposed a cursor")
		}
		diag := CheckDiagnostic(t, err, s, stage)
		return Observed{state: state, Diagnostic: diag, ErrorStage: stage}
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
		if recorder.RawBackend != RawLegacy {
			var scan reflect.Type
			switch types[i] {
			case "INT4":
				scan = reflect.TypeFor[int64]()
				if recorder.RawBackend == RawPostgres {
					scan = reflect.TypeFor[int32]()
				}
			case "INT8":
				scan = reflect.TypeFor[int64]()
			case "OID":
				scan = reflect.TypeFor[int64]()
				if recorder.RawBackend == RawPostgres {
					scan = reflect.TypeFor[any]()
					length, lengthKnown := typ.Length()
					nullable, nullableKnown := typ.Nullable()
					if length != 0 || lengthKnown || nullable || nullableKnown {
						t.Fatalf("pq OID length/nullable descriptor: %d/%t %t/%t", length, lengthKnown, nullable, nullableKnown)
					}
				}
			}
			if scan != nil && typ.ScanType() != scan {
				t.Fatalf("%s ScanType %v, want %v", types[i], typ.ScanType(), scan)
			}
		}
	}
	if !slices.Equal(columns, s.Columns) {
		t.Fatalf("columns got %#v want %#v", columns, s.Columns)
	}
	if !slices.Equal(types, s.Types) {
		t.Fatalf("types got %#v want %#v", types, s.Types)
	}
	if s.CheckMetadata != nil {
		s.CheckMetadata(t, rows)
	}
	if s.RawTypes != nil && len(s.RawTypes) != len(s.Types) {
		t.Fatal("raw codec family count differs from column count")
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
			referenceAssertStepRawValue(t, recorder, s, i, len(values), v)
		}
		if s.CheckScan != nil {
			s.CheckScan(t, rows, len(values))
		}
		for i, v := range row {
			row[i] = Normalize(v)
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	compared, matched := CompareRows(values, s.Rows, s.Bag)
	if !matched {
		t.Fatalf("rows bag=%v got %#v want %#v", s.Bag, values, s.Rows)
	}
	values = compared
	if s.NonContext {
		// Plain methods cannot carry the Context recorder key. All actual
		// cursor assertions above still apply; the companion covers recording.
		return Observed{Columns: columns, types: types, Rows: values}
	}
	// Compare recorded consumed values, not middleware call counts or fast-path choices.
	recorder.Mu.Lock()
	var recorded *Operation
	for i := len(recorder.Operations) - 1; i >= 0; i-- {
		op := recorder.Operations[i]
		if op.SQL == s.Query && op.Err == nil && op.Columns != nil {
			recorded = op
			break
		}
	}
	if recorded == nil {
		recorder.Mu.Unlock()
		t.Fatal("successful query has no recorded result")
	}
	// Recorded rows own immutable cells; retain their slice header under the lock.
	recordedRows := recorded.Rows
	capturedColumns := make([]string, len(recorded.Columns))
	copy(capturedColumns, recorded.Columns)
	closed, closeErr := recorded.Closed, recorded.CloseErr
	recorder.Mu.Unlock()
	captured := make([][]any, 0, len(recordedRows))
	for rowIndex, row := range recordedRows {
		converted := make([]any, len(row))
		for i, v := range row {
			referenceAssertStepRawValue(t, recorder, s, i, rowIndex, v)
			converted[i] = Normalize(v)
		}
		captured = append(captured, converted)
	}
	if !RecordedRowsMatch(captured, s) || !slices.Equal(capturedColumns, s.Columns) {
		t.Fatalf("recorded query data columns=%#v rows=%#v", capturedColumns, captured)
	}
	if !closed || closeErr != nil {
		t.Fatalf("recorded cursor close: closed=%v error=%v", closed, closeErr)
	}
	return Observed{Columns: capturedColumns, types: types, Rows: values}
}

func (p *Pair) Step(t *testing.T, s Step) {
	t.Helper()
	var memory, postgres Observed
	t.Run("memory", func(t *testing.T) { memory = RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s) })
	if p.Postgres == nil {
		return
	}
	t.Run("postgres", func(t *testing.T) { postgres = RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s) })
	if !t.Failed() && !reflect.DeepEqual(memory, postgres) {
		t.Fatalf("parity memory=%#v postgres=%#v", memory, postgres)
	}
}

type NoticeReferenceDriver struct{ Connector driver.Connector }

func (d NoticeReferenceDriver) OpenConnector(string) (driver.Connector, error) {
	return d.Connector, nil
}
func (d NoticeReferenceDriver) Open(string) (driver.Conn, error) {
	return d.Connector.Connect(context.Background())
}

// The command owner supplies joined only for bounded callback workers. It is
// evaluated by test cleanup, never by the failure-cleanup goroutine.
type NoticeReferenceOption struct {
	Handler func(Diagnostic)
	Joined  func() bool
}

func NoticeReferencePool(t *testing.T, p *Pair, postgres bool, records *[]Diagnostic, options ...NoticeReferenceOption) (*sql.DB, *sql.Conn, *Recorder) {
	t.Helper()
	deliver := func(diagnostic Diagnostic) {
		*records = append(*records, diagnostic)
		for _, option := range options {
			if option.Handler != nil {
				option.Handler(diagnostic)
			}
		}
	}
	var connector driver.Connector
	if postgres {
		base, err := pq.NewConnector(DSN)
		if err != nil {
			t.Fatal(err)
		}
		connector = pq.ConnectorWithNoticeHandler(base, func(n *pq.Error) { deliver(ExtractDiagnostic(t, n)) })
	} else {
		base := p.NativeConnector
		if base == nil {
			t.Fatal("notice pool has no owning native connector")
		}
		if p.MemoryRecord.RawBackend == RawLegacy {
			legacy, err := base.Driver().(*stuntgres.Driver).OpenConnector("reference/" + t.Name())
			if err != nil {
				t.Fatal(err)
			}
			base = legacy.(*stuntgres.Connector)
		}
		connector = base.WithNoticeHandler(func(n stuntgres.Error) { deliver(ExtractDiagnostic(t, &n)) })
	}
	wrapped := sqlmw.Driver(NoticeReferenceDriver{connector}, Interceptor{})
	// sqlmw's DriverContext connector preserves direct and prepared recorder hooks.
	configured, err := wrapped.(driver.DriverContext).OpenConnector("")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(configured)
	var c *sql.Conn
	t.Cleanup(func() {
		for _, option := range options {
			if option.Joined != nil && !option.Joined() {
				// Conn.Close waits for active operations. A suspected callback/
				// lease deadlock must not defeat the worker's bounded diagnostic.
				go func() {
					if c != nil {
						c.Close()
					}
					db.Close()
				}()
				return
			}
		}
		if c != nil {
			// A lifecycle witness may already have returned this borrower;
			// ErrConnDone confirms its ownership ended, not a close failure.
			if err := c.Close(); err != nil && err != sql.ErrConnDone {
				t.Error("notice connection close", err)
			}
		}
		if err := db.Close(); err != nil {
			t.Error("notice pool close", err)
		}
	})
	c, err = db.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &Recorder{RawBackend: p.MemoryRecord.RawBackend}
	if postgres {
		recorder.RawBackend = p.PostgresRecord.RawBackend
	}
	return db, c, recorder
}

func RoleReferenceCleanup(t *testing.T, names []string) {
	t.Helper()
	// Registered before pair cleanup: physical handles and schema are gone first.
	t.Cleanup(func() {
		// Some legacy callers register this before constructing their pair.
		if !Started {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, name := range names {
			if _, err := Admin.ExecContext(ctx, "DROP ROLE IF EXISTS "+pq.QuoteIdentifier(name)); err != nil {
				t.Errorf("role cleanup %s: %v", name, err)
			}
		}
	})
}

func SequenceRows(t *testing.T, rows *sql.Rows, label string, want int64) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{label}) {
		t.Fatal("sequence columns", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT8" {
		t.Fatal("sequence metadata", types, err)
	}
	TransactionRowValues(t, rows, want)
}

// Use the independently captured local OID, never a shared table/name fixture:
// rename/drop changes names while an already executing operation keeps its OID.
func SequenceWaitBehind(t *testing.T, ctx context.Context, waiter, holder int, oid int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		var waiting bool
		err := Admin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='relation' AND relation=$3::oid AND mode='RowExclusiveLock' AND NOT granted) AND $2 = ANY(pg_blocking_pids($1))`, waiter, holder, oid).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		runtime.Gosched()
	}
}

func TransactionRows(prepared bool, q string, values ...int64) Step {
	rows := make([][]any, len(values))
	for i, v := range values {
		rows[i] = []any{v}
	}
	return Step{Query: q, Prepared: prepared, SelectRows: true, Columns: []string{"v"}, Types: []string{"INT4"}, Rows: rows}
}
func TransactionError(prepared bool, q string, want Diagnostic) Step {
	return Step{Query: q, Prepared: prepared, State: want.Code, Diagnostic: &want}
}
func TransactionAborted() Diagnostic {
	return Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}
}
func TransactionNull() Diagnostic {
	return Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "v"}
}
func TransactionValues(t *testing.T, rows *sql.Rows, want ...int64) {
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
	// As in RunStep, assert SQL types and actual returned values;
	// ScanType is adapter metadata, not a shared PostgreSQL type contract.
	TransactionRowValues(t, rows, want...)
}

func TransactionRowValues(t *testing.T, rows *sql.Rows, want ...int64) {
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
func TransactionConstraintExec(t *testing.T, ctx context.Context, x Executor, q string, prepared bool) error {
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
func TransactionQuery(ctx context.Context, x Executor, q string, prepared bool) (*sql.Rows, func() error, error) {
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

func TransactionOrdinary(t *testing.T, err error, message string) {
	t.Helper()
	if err == nil || err.Error() != message {
		t.Fatalf("ordinary error %v want %q", err, message)
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		t.Fatal("fabricated SQLSTATE", err)
	}
}

type RecorderKey struct{}
type referenceRowsKey struct{}

type Operation struct {
	SQL      string
	Args     []driver.NamedValue
	Affected int64
	Err      error
	Columns  []string
	Rows     [][]driver.Value
	Closed   bool
	CloseErr error
}

type referenceRawBackend uint8

const (
	RawLegacy referenceRawBackend = iota
	RawNative
	RawPostgres
)

type Recorder struct {
	Mu         sync.Mutex
	Operations []*Operation
	// Immutable fixture provenance; legacy scenarios retain their comparisons.
	RawBackend referenceRawBackend
}

func referenceCopyValue(v driver.Value) driver.Value {
	if b, ok := v.([]byte); ok {
		if b == nil {
			return []byte(nil)
		}
		owned := make([]byte, len(b))
		copy(owned, b)
		return owned
	}
	return v
}

func (r *Recorder) begin(query string, args []driver.NamedValue) *Operation {
	op := &Operation{SQL: query, Args: make([]driver.NamedValue, len(args))}
	for i, arg := range args {
		op.Args[i] = arg
		op.Args[i].Value = referenceCopyValue(arg.Value)
	}
	r.Mu.Lock()
	r.Operations = append(r.Operations, op)
	r.Mu.Unlock()
	return op
}

func referenceBegin(ctx context.Context, query string, args []driver.NamedValue) (*Recorder, *Operation) {
	r, _ := ctx.Value(RecorderKey{}).(*Recorder)
	if r == nil {
		return nil, nil
	}
	return r, r.begin(query, args)
}

func referenceRecordExec(r *Recorder, op *Operation, result driver.Result, err error) {
	if r == nil {
		return
	}
	r.Mu.Lock()
	defer r.Mu.Unlock()
	op.Err = err
	if err == nil && result != nil {
		op.Affected, op.Err = result.RowsAffected()
	}
}

func referenceRecordQuery(ctx context.Context, r *Recorder, op *Operation, rows driver.Rows, err error) context.Context {
	if r == nil {
		return ctx
	}
	r.Mu.Lock()
	op.Err = err
	if err == nil && rows != nil {
		// A successful zero-column cursor is distinct from no query result.
		columns := rows.Columns()
		op.Columns = make([]string, len(columns))
		copy(op.Columns, columns)
	}
	r.Mu.Unlock()
	if err != nil {
		return ctx
	}
	return context.WithValue(ctx, referenceRowsKey{}, op)
}

type Interceptor struct{ sqlmw.NullInterceptor }

func (Interceptor) ConnExecContext(ctx context.Context, conn driver.ExecerContext, query string, args []driver.NamedValue) (driver.Result, error) {
	r, op := referenceBegin(ctx, query, args)
	result, err := conn.ExecContext(ctx, query, args)
	referenceRecordExec(r, op, result, err)
	return result, err
}
func (Interceptor) StmtExecContext(ctx context.Context, stmt driver.StmtExecContext, query string, args []driver.NamedValue) (driver.Result, error) {
	r, op := referenceBegin(ctx, query, args)
	result, err := stmt.ExecContext(ctx, args)
	referenceRecordExec(r, op, result, err)
	return result, err
}
func (Interceptor) ConnQueryContext(ctx context.Context, conn driver.QueryerContext, query string, args []driver.NamedValue) (context.Context, driver.Rows, error) {
	r, op := referenceBegin(ctx, query, args)
	rows, err := conn.QueryContext(ctx, query, args)
	return referenceRecordQuery(ctx, r, op, rows, err), rows, err
}
func (Interceptor) StmtQueryContext(ctx context.Context, stmt driver.StmtQueryContext, query string, args []driver.NamedValue) (context.Context, driver.Rows, error) {
	r, op := referenceBegin(ctx, query, args)
	rows, err := stmt.QueryContext(ctx, args)
	return referenceRecordQuery(ctx, r, op, rows, err), rows, err
}
func (Interceptor) RowsNext(ctx context.Context, rows driver.Rows, dest []driver.Value) error {
	err := rows.Next(dest)
	r, _ := ctx.Value(RecorderKey{}).(*Recorder)
	op, _ := ctx.Value(referenceRowsKey{}).(*Operation)
	if r != nil && op != nil {
		r.Mu.Lock()
		if err == nil {
			values := make([]driver.Value, len(dest))
			for i, v := range dest {
				values[i] = referenceCopyValue(v)
			}
			op.Rows = append(op.Rows, values)
		} else if err != io.EOF {
			op.Err = err
		}
		r.Mu.Unlock()
	}
	return err
}
func (Interceptor) RowsClose(ctx context.Context, rows driver.Rows) error {
	err := rows.Close()
	r, _ := ctx.Value(RecorderKey{}).(*Recorder)
	op, _ := ctx.Value(referenceRowsKey{}).(*Operation)
	if r != nil && op != nil {
		r.Mu.Lock()
		op.Closed = true
		op.CloseErr = err
		r.Mu.Unlock()
	}
	return err
}

func RoleReferenceError(query, code, message, detail string, prepared bool, stage string) Step {
	s := ErrorDiagnostic(query, Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail})
	s.Prepared = prepared
	s.ErrorStage = "execute"
	if prepared {
		s.ErrorStage = stage
	}
	return s
}

type ControlsReferenceFixture struct {
	Pair     *Pair
	DB       *sql.DB
	A, B     *sql.Conn
	Notices  *[]Diagnostic
	Recorder *Recorder
}

func NewControlsReferenceFixture(t *testing.T, pair *Pair, postgres bool) ControlsReferenceFixture {
	t.Helper()
	notices := new([]Diagnostic)
	db, a, recorder := NoticeReferencePool(t, pair, postgres, notices)
	// The peer comes from this exact notice connector/DSN, not the parent
	// pair: subtest names otherwise select a different embedded engine.
	b, err := db.Conn(pair.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		a.ExecContext(ctx, "ROLLBACK")
		b.ExecContext(ctx, "ROLLBACK")
		b.Close()
	})
	return ControlsReferenceFixture{Pair: pair, DB: db, A: a, B: b, Notices: notices, Recorder: recorder}
}

func ControlsReferenceDiagnostic(code, message string) Diagnostic {
	return Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func (f ControlsReferenceFixture) NoticesWant(t *testing.T, code, message string) {
	t.Helper()
	var want []Diagnostic
	if code != "" {
		n := ControlsReferenceDiagnostic(code, message)
		n.Severity, n.SeverityUnlocalized = "WARNING", "WARNING"
		want = []Diagnostic{n}
	}
	if len(*f.Notices) != len(want) || (len(want) > 0 && !reflect.DeepEqual(*f.Notices, want)) {
		t.Fatalf("notices got %#v want %#v", *f.Notices, want)
	}
	*f.Notices = nil
}

func (f ControlsReferenceFixture) Exec(t *testing.T, x Executor, query string, prepared bool, count int64) {
	t.Helper()
	RunStep(t, f.Pair.Ctx, x, f.Recorder, Step{Query: query, Prepared: prepared, Affected: count})
}
func (f ControlsReferenceFixture) Rows(t *testing.T, x Executor, values ...int64) {
	t.Helper()
	RunStep(t, f.Pair.Ctx, x, f.Recorder, TransactionRows(false, "SELECT v FROM t ORDER BY v", values...))
}
func IsolationReferenceConnections(t *testing.T, pair *Pair, postgres bool) (*sql.DB, *sql.Conn, *sql.Conn) {
	t.Helper()
	db := pair.Memory
	if postgres {
		db = pair.Postgres
	}
	a, err := db.Conn(pair.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.Conn(pair.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		a.ExecContext(ctx, "ROLLBACK")
		b.ExecContext(ctx, "ROLLBACK")
		a.Close()
		b.Close()
	})
	return db, a, b
}
func IsolationReferenceAffected(t *testing.T, ctx context.Context, x Executor, query string, prepared bool, want int64) {
	t.Helper()
	RunStep(t, ctx, x, &Recorder{}, Step{Query: query, Prepared: prepared, Affected: want})
}
func IsolationReferenceSelect(t *testing.T, ctx context.Context, x Executor, postgres bool, query string, prepared bool, want ...int64) {
	t.Helper()
	rawBackend := RawNative
	if postgres {
		rawBackend = RawPostgres
	}
	RunStep(t, ctx, x, &Recorder{RawBackend: rawBackend}, TransactionRows(prepared, query, want...))
}
func IsolationReferenceWant(code, message string) Diagnostic {
	return Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}
func IsolationReferenceDiagnostic(t *testing.T, err error, want Diagnostic) {
	t.Helper()
	AssertDiagnostic(t, err, &want)
	State(t, err, want.Code)
}

// Every outstanding operation is canceled and joined before transaction/connection
// cleanup and the shared fixture's schema reset, including on assertion failure.
func LockOperation(t *testing.T, ctx context.Context, call func(context.Context) RelationOutcome, limit ...time.Duration) func() RelationOutcome {
	t.Helper()
	timeout := 5 * time.Second
	if len(limit) != 0 {
		timeout = limit[0]
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	done := make(chan RelationOutcome, 1)
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			o := LockResult(t, done, timeout+2*time.Second)
			if o.Rows != nil {
				o.Rows.Close()
			}
			if o.Stmt != nil {
				o.Stmt.Close()
			}
		}
	})
	go func() { done <- call(ctx) }()
	return func() RelationOutcome {
		t.Helper()
		o := LockResult(t, done, timeout+2*time.Second)
		joined = true
		return o
	}
}

// PostgreSQL statement cancellation preserves its session. lib/pq cancellation
// through a Go context can instead close the transport, and a fast SET can win
// that race even with a pre-canceled context. Use server-side cancellation after
// an observed admission barrier, not a timing assumption about either driver.
func SessionCancel(t *testing.T, parent context.Context, conn *sql.Conn, prepared bool, queryMode ...bool) {
	t.Helper()
	query := "SELECT pg_sleep(600)"
	queryCursor := len(queryMode) != 0 && queryMode[0]
	SessionCancelQuery(t, parent, conn, prepared, query, queryCursor)
}

// SessionCancelQuery observes admission of the supplied SQL itself.
// Existing callers retain their pg_sleep barrier through SessionCancel.
func SessionCancelQuery(t *testing.T, parent context.Context, conn *sql.Conn, prepared bool, query string, queryCursor bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	var pid int
	if err := conn.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	joined := true
	result := make(chan error, 1)
	defer func() {
		cancel()
		if !joined {
			select {
			case <-result:
			case <-time.After(5 * time.Second):
				t.Error("cancellation worker failed to join")
			}
		}
	}()
	var stmt *sql.Stmt
	if prepared {
		var err error
		stmt, err = conn.PrepareContext(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
	}
	// result is buffered so teardown can always release the worker.
	var exposedCursor bool
	joined = false
	go func() {
		var err error
		if queryCursor {
			var rows *sql.Rows
			if stmt != nil {
				rows, err = stmt.QueryContext(ctx)
			} else {
				rows, err = conn.QueryContext(ctx, query)
			}
			if rows != nil {
				exposedCursor = true
				rows.Close()
			}
		} else if stmt != nil {
			_, err = stmt.ExecContext(ctx)
		} else {
			_, err = conn.ExecContext(ctx, query)
		}
		result <- err
	}()
	join := func() error {
		select {
		case err := <-result:
			joined = true
			return err
		case <-ctx.Done():
			cancel()
			select {
			case err := <-result:
				joined = true
				return err
			case <-time.After(5 * time.Second):
				t.Fatal("cancellation worker failed to join after deadline")
				return ctx.Err()
			}
		}
	}
	for {
		var active bool
		err := Admin.QueryRowContext(ctx,
			"SELECT COALESCE((SELECT state = 'active' AND query = $2 FROM pg_stat_activity WHERE pid = $1), FALSE)",
			pid, query).Scan(&active)
		if err != nil {
			cancel()
			statementErr := join()
			t.Fatalf("cancellation admission barrier: %v; statement: %v", err, statementErr)
		}
		if active {
			break
		}
		select {
		case err := <-result:
			joined = true
			t.Fatalf("statement completed before cancellation admission: %v", err)
		default:
		}
	}
	var signaled bool
	if err := Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
		cancel()
		statementErr := join()
		t.Fatalf("cancel backend: signaled=%t error=%v; statement: %v", signaled, err, statementErr)
	}
	err := join()
	if exposedCursor {
		t.Fatal("canceled admitted Query exposed a cursor")
	}
	AssertDiagnostic(t, err, &Diagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014",
		Message: "canceling statement due to user request",
	})
}
