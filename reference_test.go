//go:build reference

package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lib/pq"
	"stuntgres"
)

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
	affected           int64
	state              string
	diagnostic         *referenceDiagnostic
	postgresDiagnostic *referenceDiagnostic
	errorStage         string
	columns, types     []string
	rows               [][]any
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
	stage := "execute"
	state := referenceState(t, err, s.state)
	if err != nil {
		diag := referenceCheckDiagnostic(t, err, s, stage)
		return referenceObserved{state: state, diagnostic: diag, errorStage: stage}
	}
	defer rows.Close()
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
	expected := s.rows
	if expected == nil {
		expected = make([][]any, 0)
	}
	if !reflect.DeepEqual(values, expected) {
		t.Fatalf("rows got %#v want %#v", values, expected)
	}
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
	capturedColumns := append([]string(nil), recorded.Columns...)
	closed, closeErr := recorded.Closed, recorded.CloseErr
	recorder.mu.Unlock()
	if !reflect.DeepEqual(captured, expected) || !reflect.DeepEqual(capturedColumns, s.columns) {
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

func TestReferenceFixtures(t *testing.T) {
	// Deliberately serialized: every fixture shares the one PostgreSQL public schema.
	fixtures := []struct {
		name  string
		steps []referenceStep
	}{
		{"smoke", []referenceStep{
			referenceExec("CREATE TABLE people (id INTEGER NOT NULL, name TEXT, active BOOLEAN NOT NULL)", 0),
			referencePrepared(referenceExec("INSERT INTO people (id, name, active) VALUES ($1, $2, $3)", 1, 2, "O'Brien", false)),
			referencePrepared(referenceExec("INSERT INTO people (id, name, active) VALUES ($1, $2, $3)", 1, 1, "Ada", true)),
			referenceSelect("SELECT id, name, active FROM people ORDER BY id", []string{"id", "name", "active"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), "Ada", true}, {int64(2), "O'Brien", false}}),
			referenceExec("SELECT id, name, active FROM people ORDER BY id", 2),
			referencePrepared(referenceExec("SELECT id FROM people WHERE id = $1", 1, 1)),
		}},
		{"quoted_and_omitted_columns", []referenceStep{
			referenceExec(`CREATE TABLE "Odd" ("Id" BIGINT NOT NULL, "a""b" TEXT, flag BOOL)`, 0),
			referenceExec(`INSERT INTO "Odd" ("a""b", "Id") VALUES ('line
O''Brien\tail', 2), ('', 1)`, 2),
			referenceSelect(`SELECT * FROM "Odd" ORDER BY "Id"`, []string{"Id", `a"b`, "flag"}, []string{"INT8", "TEXT", "BOOL"}, [][]any{{int64(1), "", nil}, {int64(2), "line\nO'Brien\\tail", nil}}),
		}},
		{"null_equality_and_order", []referenceStep{
			referenceExec("CREATE TABLE ordering (id INTEGER, label TEXT, flag BOOLEAN)", 0),
			referenceExec("INSERT INTO ordering VALUES (NULL, NULL, NULL), (2, 'z', TRUE), (1, 'A', FALSE)", 3),
			referenceSelect("SELECT id FROM ordering WHERE id = NULL", []string{"id"}, []string{"INT4"}, nil),
			referencePrepared(referenceSelect("SELECT id FROM ordering WHERE id = $1", []string{"id"}, []string{"INT4"}, nil, nil)),
			referenceSelect("SELECT id FROM ordering ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {nil}}),
			referenceSelect("SELECT id FROM ordering ORDER BY id DESC", []string{"id"}, []string{"INT4"}, [][]any{{nil}, {int64(2)}, {int64(1)}}),
			referenceSelect("SELECT label FROM ordering ORDER BY label", []string{"label"}, []string{"TEXT"}, [][]any{{"A"}, {"z"}, {nil}}),
			referenceSelect("SELECT flag FROM ordering ORDER BY flag DESC", []string{"flag"}, []string{"BOOL"}, [][]any{{nil}, {true}, {false}}),
		}},
		{"integer_and_text_assignment", []referenceStep{
			referenceExec("CREATE TABLE numbers (small INT4, big INT8, textvalue TEXT)", 0),
			referenceExec("INSERT INTO numbers VALUES (-2147483648, -9223372036854775808, TRUE), (2147483647, 9223372036854775807, 42)", 2),
			referencePrepared(referenceExec("INSERT INTO numbers VALUES ($1, $2, $3)", 1, " +0007 ", []byte(" -8 "), false)),
			referenceSelect("SELECT * FROM numbers ORDER BY small", []string{"small", "big", "textvalue"}, []string{"INT4", "INT8", "TEXT"}, [][]any{{int64(-2147483648), int64(-9223372036854775808), "true"}, {int64(7), int64(-8), "false"}, {int64(2147483647), int64(9223372036854775807), "42"}}),
			referenceSelect("SELECT small FROM numbers WHERE small = ' +7 '", []string{"small"}, []string{"INT4"}, [][]any{{int64(7)}}),
		}},
		{"boolean_inputs", []referenceStep{
			referenceExec("CREATE TABLE booleans (id INTEGER, value BOOLEAN)", 0),
			referenceExec("INSERT INTO booleans VALUES (1, 't'), (2, 'F'), (3, ' ye '), (4, 'of'), (5, '1'), (6, '0'), (7, 'ON'), (8, 'n')", 8),
			referencePrepared(referenceExec("INSERT INTO booleans VALUES ($1, $2)", 1, 9, []byte(" true "))),
			referenceSelect("SELECT * FROM booleans ORDER BY id", []string{"id", "value"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), true}, {int64(4), false}, {int64(5), true}, {int64(6), false}, {int64(7), true}, {int64(8), false}, {int64(9), true}}),
			referenceError("INSERT INTO booleans VALUES (10, 'o')", "22P02"),
			referenceError("INSERT INTO booleans VALUES (10, 'truth')", "22P02"),
			referenceError("INSERT INTO booleans VALUES (10, 1)", "42804"),
			referenceStep{query: "SELECT id FROM booleans WHERE value = 1", selectRows: true, state: "42883"},
		}},
		{"catalog_errors", []referenceStep{
			referenceError("SELECT missing FROM absent", "42P01"),
			referenceExec("CREATE TABLE empty (id INTEGER)", 0),
			referenceError("CREATE TABLE empty (other TEXT)", "42P07"),
			referenceError("CREATE TABLE duplicate (x INTEGER, x TEXT)", "42701"),
			referenceError("INSERT INTO empty (id, id) VALUES (1, 2)", "42701"),
			referenceError("INSERT INTO empty (absent) VALUES (1)", "42703"),
			referenceError("SELECT absent FROM empty", "42703"),
			referenceError("SELECT id FROM empty WHERE absent = 1", "42703"),
			referenceError("SELECT id FROM empty ORDER BY absent", "42703"),
			referenceSelect("SELECT * FROM empty", []string{"id"}, []string{"INT4"}, nil),
		}},
		{"atomic_insert_errors", []referenceStep{
			referenceExec("CREATE TABLE atomic_rows (id INTEGER NOT NULL, big BIGINT, flag BOOLEAN)", 0),
			referenceError("INSERT INTO atomic_rows VALUES (1, 1, TRUE), (NULL, 2, FALSE)", "23502"),
			referenceError("INSERT INTO atomic_rows (big) VALUES (1)", "23502"),
			referenceError("INSERT INTO atomic_rows VALUES (1, 1, TRUE), (2147483648, 2, FALSE)", "22003"),
			referenceError("INSERT INTO atomic_rows VALUES (1, 1, TRUE), (2, '9223372036854775808', FALSE)", "22003"),
			referenceError("INSERT INTO atomic_rows VALUES (-2147483649, 1, TRUE)", "22003"),
			referenceError("INSERT INTO atomic_rows VALUES (1, '-9223372036854775809', TRUE)", "22003"),
			referenceError("INSERT INTO atomic_rows (id, big, flag) VALUES (1, 2)", "42601"),
			referenceStep{query: "SELECT id FROM atomic_rows WHERE id = TRUE", selectRows: true, state: "42883"},
			referenceError("INSERT INTO atomic_rows VALUES ('not an integer', 1, TRUE)", "22P02"),
			referenceError("INSERT INTO atomic_rows VALUES ('1.2', 1, TRUE)", "22P02"),
			referenceError("INSERT INTO atomic_rows VALUES (TRUE, 1, TRUE)", "42804"),
			referencePrepared(referenceError("INSERT INTO atomic_rows VALUES ($1, $2, $3)", "22003", "2147483648", 1, true)),
			referencePrepared(referenceError("INSERT INTO atomic_rows VALUES ($1, $2, $3)", "22P02", "x", 1, true)),
			referenceSelect("SELECT * FROM atomic_rows ORDER BY id", []string{"id", "big", "flag"}, []string{"INT4", "INT8", "BOOL"}, nil),
			referenceExec("INSERT INTO atomic_rows VALUES (-2147483648, -9223372036854775808, FALSE), (2147483647, 9223372036854775807, TRUE)", 2),
		}},
		{"repeated_parameters", []referenceStep{
			referenceExec("CREATE TABLE repeated (a INTEGER, b INTEGER, label TEXT)", 0),
			referenceExec("INSERT INTO repeated VALUES ($1, $1, $2)", 1, 7, "direct"),
			referencePrepared(referenceExec("INSERT INTO repeated VALUES ($1, $1, $2)", 1, 9, "prepared")),
			referencePrepared(referenceSelect("SELECT a, b, label FROM repeated WHERE b = $1 ORDER BY a", []string{"a", "b", "label"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(9), int64(9), "prepared"}}, "9")),
			referenceSelect("SELECT * FROM repeated ORDER BY a", []string{"a", "b", "label"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(7), int64(7), "direct"}, {int64(9), int64(9), "prepared"}}),
		}},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			pair := newReferencePair(t)
			for i, step := range fixture.steps {
				t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) { pair.step(t, step) })
			}
		})
	}
}

func TestReferenceDumpRoundTrip(t *testing.T) {
	p := newReferencePair(t)
	for _, query := range []string{
		`CREATE TABLE "Empty" ("spare" BIGINT)`,
		`CREATE TABLE "Round" ("id" INTEGER NOT NULL, "a""b" TEXT, "flag" BOOLEAN, "big" BIGINT)`,
		`INSERT INTO "Round" VALUES (0, '', FALSE, 0), (1, NULL, NULL, NULL), (2, 'first
O''Brien\last', TRUE, -9223372036854775808)`,
		`CREATE TABLE public."a.b" ("q""x" INTEGER, note TEXT)`,
		`INSERT INTO public."a.b" VALUES (9, 'qualified')`,
	} {
		if _, err := p.memory.ExecContext(p.ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	// PostgreSQL executes the exact memory-produced script, not reconstructed fixtures.
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal(err)
	}
	p.step(t, referenceSelect(`SELECT * FROM "Empty"`, []string{"spare"}, []string{"INT8"}, nil))
	p.step(t, referenceSelect(`SELECT * FROM "Round" ORDER BY id`, []string{"id", `a"b`, "flag", "big"}, []string{"INT4", "TEXT", "BOOL", "INT8"}, [][]any{
		{int64(0), "", false, int64(0)}, {int64(1), nil, nil, nil}, {int64(2), "first\nO'Brien\\last", true, int64(-9223372036854775808)},
	}))
	p.step(t, referenceSelect(`SELECT "q""x",note FROM public."a.b"`, []string{`q"x`, "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(9), "qualified"}}))
	var restored bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &restored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), restored.Bytes()) {
		t.Fatalf("dump changed across restore:\nbefore:\n%s\nafter:\n%s", dump.String(), restored.String())
	}
}

func TestReferenceErrorFields(t *testing.T) {
	p := newReferencePair(t)
	syntax := &referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601",
		Message: `syntax error at or near ")"`, Position: "28",
	}
	notNull := &referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502",
		Message: `null value in column "id" of relation "diagnostic_rows" violates not-null constraint`,
		Detail:  "Failing row contains (null, bad).", Schema: "public",
		Table: "diagnostic_rows", Column: "id",
	}
	p.step(t, referenceExec("CREATE TABLE diagnostic_rows (id INT NOT NULL,note TEXT)", 0))
	for _, step := range []referenceStep{
		{query: "INSERT INTO t VALUES ('é', )", state: "42601", diagnostic: syntax},
		{query: "INSERT INTO t VALUES ('é', )", state: "42601", diagnostic: syntax, selectRows: true},
		{query: "INSERT INTO t VALUES ('é', )", state: "42601", diagnostic: syntax, prepared: true},
		{query: "INSERT INTO diagnostic_rows VALUES (NULL,'bad')", state: "23502", diagnostic: notNull},
		{query: "INSERT INTO diagnostic_rows VALUES ($1,$2)", state: "23502", diagnostic: notNull, prepared: true, args: []any{nil, "bad"}},
		{query: "INSERT INTO diagnostic_rows VALUES (1,'good'),(NULL,'bad')", state: "23502", diagnostic: &referenceDiagnostic{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502",
			Message: notNull.Message, Detail: "Failing row contains (null, bad).",
			Schema: "public", Table: "diagnostic_rows", Column: "id",
		}},
	} {
		p.step(t, step)
	}
	for _, tc := range []struct {
		query, message, position string
	}{
		{"INSERT INTO t VALUES ('😀', )", `syntax error at or near ")"`, "28"},
		{"CREATE TABLE t (x INT", "syntax error at end of input", "22"},
		{"CREATE TABLE t (x INT, \"Bad\")", `syntax error at or near ")"`, "29"},
		{"CREATE TABLE t (x INT) GARBAGE", `syntax error at or near "GARBAGE"`, "24"},
		{"/*é*/ /* never closed", `unterminated /* comment at or near "/* never closed"`, "7"},
		{"INSERT INTO t VALUES ('é", `unterminated quoted string at or near "'é"`, "23"},
		{`SELECT * FROM "Bad`, `unterminated quoted identifier at or near ""Bad"`, "15"},
		{"INSERT INTO t VALUES ('first'\n 'later", "unterminated quoted string at or near \"'first'\n 'later\"", "23"},
		{`SELECT * FROM "" trailing`, `zero-length delimited identifier at or near """"`, "15"},
	} {
		want := referenceDiagnostic{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601",
			Message: tc.message, Position: tc.position,
		}
		p.step(t, referenceErrorDiagnostic(tc.query, want))
		p.step(t, referencePrepared(referenceErrorDiagnostic(tc.query, want)))
	}
	duplicate := referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P07",
		Message: `relation "diagnostic_rows" already exists`,
	}
	p.step(t, referenceErrorDiagnostic("CREATE TABLE diagnostic_rows (id INT)", duplicate))
	p.step(t, referencePrepared(referenceErrorDiagnostic("CREATE TABLE diagnostic_rows (id INT)", duplicate)))
	p.step(t, referenceSelect("SELECT id,note FROM diagnostic_rows ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, nil))
	p.step(t, referenceExec("CREATE TABLE row_formats (id INT NOT NULL,big BIGINT,flag BOOL,note TEXT)", 0))
	for _, tc := range []struct {
		name, big, flag, note, detail string
	}{
		{"values", "-9223372036854775808", "TRUE", "a,b\nc", "Failing row contains (null, -9223372036854775808, t, a,b\nc)."},
		{"false", "9223372036854775807", "FALSE", "edge", "Failing row contains (null, 9223372036854775807, f, edge)."},
		{"exact_64", "NULL", "NULL", strings.Repeat("x", 64), "Failing row contains (null, null, null, " + strings.Repeat("x", 64) + ")."},
		{"long_ascii", "NULL", "NULL", strings.Repeat("x", 65), "Failing row contains (null, null, null, " + strings.Repeat("x", 64) + "...)."},
		{"long_utf8", "NULL", "NULL", strings.Repeat("x", 63) + "é", "Failing row contains (null, null, null, " + strings.Repeat("x", 63) + "...)."},
	} {
		message := `null value in column "id" of relation "row_formats" violates not-null constraint`
		expected := &referenceDiagnostic{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: message,
			Detail: tc.detail, Schema: "public", Table: "row_formats", Column: "id",
		}
		query := fmt.Sprintf("INSERT INTO row_formats VALUES (NULL,%s,%s,'%s')", tc.big, tc.flag, tc.note)
		p.step(t, referenceStep{query: query, state: "23502", diagnostic: expected})
	}
}

func TestReferenceErrorFieldsConversion(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE diagnostics (id INT, flag BOOL)", 0))
	for _, tc := range []struct {
		query, value, code, message string
	}{
		{"INSERT INTO diagnostics VALUES ($1,TRUE)", "not an integer", "22P02", `invalid input syntax for type integer: "not an integer"`},
		{"SELECT id FROM diagnostics WHERE id=$1", "not an integer", "22P02", `invalid input syntax for type integer: "not an integer"`},
		{"SELECT id FROM diagnostics WHERE id=$1", "2147483648", "22003", `value "2147483648" is out of range for type integer`},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%t/%s/%s", prepared, tc.code, tc.query), func(t *testing.T) {
				native := referenceDiagnostic{
					Severity: "ERROR", SeverityUnlocalized: "ERROR",
					Code: tc.code, Message: tc.message,
				}
				postgres := native
				postgres.Context = "unnamed portal parameter $1 = '...'"
				p.step(t, referenceStep{
					query: tc.query, args: []any{tc.value}, prepared: prepared,
					selectRows: strings.HasPrefix(tc.query, "SELECT "),
					state:      tc.code, diagnostic: &native, postgresDiagnostic: &postgres,
					errorStage: "execute",
				})
			})
		}
	}
	p.step(t, referenceSelect("SELECT id FROM diagnostics ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
}

func TestReferenceConditionalErrorFields(t *testing.T) {
	p := newReferencePair(t)
	assertPostgres := func(query string) referenceDiagnostic {
		t.Helper()
		_, err := p.postgres.ExecContext(p.ctx, query)
		if err == nil {
			t.Fatalf("query succeeded, want diagnostic: %s", query)
		}
		return referenceExtractDiagnostic(t, fmt.Errorf("wrapped: %w", err))
	}
	for _, tc := range []struct {
		name, query string
		want        referenceDiagnostic
	}{
		{"column", `DO $$ BEGIN RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='independent fields', COLUMN='only_column'; END $$;`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: "independent fields", Column: "only_column", Context: "PL/pgSQL function inline_code_block line 1 at RAISE"}},
		{"schema", `DO $$ BEGIN RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='independent fields', SCHEMA='only_object'; END $$;`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: "independent fields", Schema: "only_object", Context: "PL/pgSQL function inline_code_block line 1 at RAISE"}},
		{"table", `DO $$ BEGIN RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='independent fields', TABLE='only_object'; END $$;`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: "independent fields", Table: "only_object", Context: "PL/pgSQL function inline_code_block line 1 at RAISE"}},
		{"datatype", `DO $$ BEGIN RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='independent fields', DATATYPE='only_object'; END $$;`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: "independent fields", DataType: "only_object", Context: "PL/pgSQL function inline_code_block line 1 at RAISE"}},
		{"constraint", `DO $$ BEGIN RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='independent fields', CONSTRAINT='only_object'; END $$;`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: "independent fields", Constraint: "only_object", Context: "PL/pgSQL function inline_code_block line 1 at RAISE"}},
		{"detail", `DO $$ BEGIN RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='independent fields', DETAIL='facts'; END $$;`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: "independent fields", Detail: "facts", Context: "PL/pgSQL function inline_code_block line 1 at RAISE"}},
		{"hint", `DO $$ BEGIN RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='independent fields', HINT='advice'; END $$;`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: "independent fields", Hint: "advice", Context: "PL/pgSQL function inline_code_block line 1 at RAISE"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := assertPostgres(tc.query)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("conditional diagnostic = %#v, want %#v", got, tc.want)
			}
		})
	}
	internal := assertPostgres(`DO $$ BEGIN EXECUTE 'SELECT 1 +'; END $$;`)
	if internal.Severity != "ERROR" || internal.SeverityUnlocalized != "ERROR" ||
		internal.Code != "42601" || internal.Message != "syntax error at end of input" ||
		internal.Position != "" || internal.Detail != "" || internal.Hint != "" ||
		internal.Schema != "" || internal.Table != "" || internal.Column != "" ||
		internal.DataType != "" || internal.Constraint != "" ||
		internal.InternalQuery != "SELECT 1 +" || internal.InternalPosition != "11" || internal.Context == "" {
		t.Fatalf("internal query diagnostic = %#v", internal)
	}
}

func TestReferencePermissionAndCancellationFields(t *testing.T) {
	p := newReferencePair(t)
	if _, err := referenceAdmin.ExecContext(p.ctx, "CREATE TABLE diagnostic_rows (id INT NOT NULL,note TEXT); CREATE ROLE error_fields_denied NOLOGIN; GRANT USAGE ON SCHEMA public TO error_fields_denied"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := referenceAdmin.ExecContext(cleanupCtx, "DROP OWNED BY error_fields_denied; DROP ROLE IF EXISTS error_fields_denied"); err != nil {
			t.Errorf("drop reference role: %v", err)
		}
	}()
	roleConn, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer roleConn.Close()
	if _, err := roleConn.ExecContext(p.ctx, "SET ROLE error_fields_denied"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := roleConn.ExecContext(cleanupCtx, "RESET ROLE"); err != nil {
			t.Errorf("reset reference role: %v", err)
		}
	}()
	rows, err := roleConn.QueryContext(p.ctx, "SELECT id FROM diagnostic_rows")
	if rows != nil {
		rows.Close()
	}
	referenceAssertDiagnostic(t, fmt.Errorf("wrapped: %w", err), &referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501",
		Message: "permission denied for table diagnostic_rows",
	})
	cancelConn, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelConn.Close()
	if _, err := cancelConn.ExecContext(p.ctx, "SET statement_timeout='50ms'"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := cancelConn.ExecContext(cleanupCtx, "SET statement_timeout=0"); err != nil {
			t.Errorf("reset reference statement timeout: %v", err)
		}
	}()
	timeoutCtx, timeoutCancel := context.WithTimeout(p.ctx, 5*time.Second)
	_, err = cancelConn.ExecContext(timeoutCtx, "SELECT pg_sleep(10)")
	timeoutCancel()
	referenceAssertDiagnostic(t, fmt.Errorf("wrapped: %w", err), &referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014",
		Message: "canceling statement due to statement timeout",
	})
	if _, err := cancelConn.ExecContext(p.ctx, "SET statement_timeout=0"); err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := cancelConn.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	queryCtx, queryCancel := context.WithTimeout(p.ctx, 8*time.Second)
	defer queryCancel()
	result := make(chan error, 1)
	go func() {
		_, queryErr := cancelConn.ExecContext(queryCtx, "SELECT pg_sleep(10)")
		result <- queryErr
	}()
	deadline := time.Now().Add(5 * time.Second)
	active := false
	for time.Now().Before(deadline) {
		var waitEvent sql.NullString
		if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT wait_event FROM pg_stat_activity WHERE pid=$1 AND state='active'", pid).Scan(&waitEvent); err == nil && waitEvent.Valid && waitEvent.String == "PgSleep" {
			active = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !active {
		queryCancel()
		<-result
		t.Fatal("timed out waiting for reference query to enter PgSleep")
	}
	var canceled bool
	if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
		queryCancel()
		<-result
		t.Fatalf("cancel backend: canceled=%v error=%v", canceled, err)
	}
	userErr := <-result
	queryCancel()
	referenceAssertDiagnostic(t, fmt.Errorf("wrapped: %w", userErr), &referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014",
		Message: "canceling statement due to user request",
	})
}
func TestReferenceInsertImplicitShortTargets(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT NOT NULL,big BIGINT,label TEXT,flag BOOL)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES (1)", 1))
	p.step(t, referencePrepared(referenceExec("INSERT INTO t VALUES ($1,$2)", 1, 2, "20")))
	p.step(t, referencePrepared(referenceExec("INSERT INTO t VALUES ($1)", 1, "5")))
	p.step(t, referenceExec("INSERT INTO t VALUES ($1)", 1, []byte("6")))
	p.step(t, referenceExec("CREATE TABLE repeats(a INT,b INT,tail TEXT)", 0))
	p.step(t, referencePrepared(referenceExec("INSERT INTO repeats VALUES ($1,$1),($2,$2)", 2, 11, 12)))
	p.step(t, referenceExec("INSERT INTO t VALUES (3,30,'three')", 1))
	p.step(t, referenceExec("INSERT INTO t(label,id) VALUES ('reordered',4)", 1))
	p.step(t, referenceExec("INSERT INTO t VALUES (7,70,'full',TRUE)", 1))
	p.step(t, referencePrepared(referenceExec("INSERT INTO t VALUES (8),(9)", 2)))
	p.step(t, referencePrepared(referenceExec("INSERT INTO t(label,id) VALUES ('ten',10),('eleven',11)", 2)))
	p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "big", "label", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{
		{int64(1), nil, nil, nil}, {int64(2), int64(20), nil, nil}, {int64(3), int64(30), "three", nil}, {int64(4), nil, "reordered", nil}, {int64(5), nil, nil, nil}, {int64(6), nil, nil, nil}, {int64(7), int64(70), "full", true}, {int64(8), nil, nil, nil}, {int64(9), nil, nil, nil}, {int64(10), nil, "ten", nil}, {int64(11), nil, "eleven", nil},
	}))
	p.step(t, referencePrepared(referenceSelect("SELECT * FROM t WHERE id = $1", []string{"id", "big", "label", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(2), int64(20), nil, nil}}, 2)))
	p.step(t, referenceSelect("SELECT * FROM repeats ORDER BY a", []string{"a", "b", "tail"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(11), int64(11), nil}, {int64(12), int64(12), nil}}))
	p.step(t, referenceExec(`CREATE TABLE "Odd"("Id" INT NOT NULL,label TEXT)`, 0))
	p.step(t, referencePrepared(referenceExec(`INSERT INTO "Odd" VALUES ($1)`, 1, "7")))
	p.step(t, referenceSelect(`SELECT * FROM "Odd" ORDER BY "Id"`, []string{"Id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), nil}}))
}

func TestReferenceInsertImplicitShortDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT NOT NULL,big BIGINT,label TEXT,flag BOOL)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES (99)", 1))
	check := func(preparedStage, query, code, message string, position int, detail, hint, schema, table, column string) {
		t.Helper()
		step := referenceImplicitError(query, referenceDiagnostic{Code: code, Message: message, Position: referencePosition(position), Detail: detail, Hint: hint, Schema: schema, Table: table, Column: column})
		step.errorStage = "execute"
		p.step(t, step)
		p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "big", "label", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(99), nil, nil, nil}}))
		step.errorStage = preparedStage
		p.step(t, referencePrepared(step))
		p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "big", "label", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(99), nil, nil, nil}}))
	}
	q := "INSERT INTO t VALUES (20),('oops')"
	check("prepare", q, "22P02", `invalid input syntax for type integer: "oops"`, strings.Index(q, "'oops'")+1, "", "", "", "", "")
	q = "INSERT INTO t VALUES (20),(21,22)"
	check("prepare", q, "42601", "VALUES lists must all be the same length", strings.Index(q, "21,22")+1, "", "", "", "", "")
	q = "INSERT INTO t VALUES (20,30),(21)"
	check("prepare", q, "42601", "VALUES lists must all be the same length", strings.Index(q, "21)")+1, "", "", "", "", "")
	q = "INSERT INTO t VALUES ('2147483648')"
	check("prepare", q, "22003", `value "2147483648" is out of range for type integer`, strings.Index(q, "'2147483648'")+1, "", "", "", "", "")
	for _, query := range []string{
		"INSERT INTO t VALUES (2147483648)",
		"INSERT INTO t VALUES (20),(-2147483649)",
		"INSERT INTO t VALUES (NULL),(2147483648)",
	} {
		step := referenceImplicitError(query, referenceDiagnostic{Code: "22003", Message: "integer out of range"})
		step.errorStage = "execute"
		p.step(t, step)
		p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "big", "label", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(99), nil, nil, nil}}))
		p.step(t, referencePrepared(step))
		p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "big", "label", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(99), nil, nil, nil}}))
	}
	q = "INSERT INTO t VALUES (20,'oops')"
	check("prepare", q, "22P02", `invalid input syntax for type bigint: "oops"`, strings.Index(q, "'oops'")+1, "", "", "", "", "")
	q = "INSERT INTO t VALUES (20,30,NULL,'maybe')"
	check("prepare", q, "22P02", `invalid input syntax for type boolean: "maybe"`, strings.Index(q, "'maybe'")+1, "", "", "", "", "")
	q = "INSERT INTO t VALUES (TRUE)"
	check("prepare", q, "42804", `column "id" is of type integer but expression is of type boolean`, strings.Index(q, "TRUE")+1, "", "You will need to rewrite or cast the expression.", "", "", "")
	q = "INSERT INTO t VALUES (20),(TRUE)"
	check("prepare", q, "42804", `column "id" is of type integer but expression is of type boolean`, strings.Index(q, "TRUE")+1, "", "You will need to rewrite or cast the expression.", "", "", "")
	q = "INSERT INTO t VALUES (20,30,40,TRUE,50)"
	check("prepare", q, "42601", "INSERT has more expressions than target columns", strings.Index(q, "50)")+1, "", "", "", "", "")
	q = "INSERT INTO t (id,label) VALUES (20)"
	check("prepare", q, "42601", "INSERT has more target columns than expressions", strings.Index(q, "label")+1, "", "", "", "", "")
	q = "INSERT INTO t (id,label) VALUES ('oops')"
	check("prepare", q, "42601", "INSERT has more target columns than expressions", strings.Index(q, "label")+1, "", "", "", "", "")
	q = "INSERT INTO t (id,id) VALUES (20,21)"
	check("prepare", q, "42701", `column "id" specified more than once`, strings.LastIndex(q, "id)")+1, "", "", "", "", "")
	q = "INSERT INTO t (missing) VALUES (20)"
	check("prepare", q, "42703", `column "missing" of relation "t" does not exist`, strings.Index(q, "missing")+1, "", "", "", "", "")
	q = "/* é */ INSERT INTO t VALUES (20),('oops')"
	check("prepare", q, "22P02", `invalid input syntax for type integer: "oops"`, utf8.RuneCountInString(q[:strings.Index(q, "'oops'")])+1, "", "", "", "", "")
	parameterError := referenceImplicitError("INSERT INTO t VALUES($1),($2)", referenceDiagnostic{
		Code: "22P02", Message: `invalid input syntax for type integer: "oops"`,
	}, 20, "oops")
	postgresParameterError := *parameterError.diagnostic
	postgresParameterError.Context = "unnamed portal parameter $2 = '...'"
	parameterError.postgresDiagnostic = &postgresParameterError
	parameterError.errorStage = "execute"
	p.step(t, parameterError)
	p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "big", "label", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(99), nil, nil, nil}}))
	p.step(t, referencePrepared(parameterError))
	p.step(t, referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
	q = "INSERT INTO t VALUES (20),(NULL)"
	check("execute", q, "23502", `null value in column "id" of relation "t" violates not-null constraint`, 0, "Failing row contains (null, null, null, null).", "", "public", "t", "id")
	p.step(t, referenceExec("CREATE TABLE required(id INT,flag BOOL NOT NULL)", 0))
	check("execute", "INSERT INTO required VALUES (1)", "23502", `null value in column "flag" of relation "required" violates not-null constraint`, 0, "Failing row contains (1, null).", "", "public", "required", "flag")
	p.step(t, referenceSelect("SELECT * FROM required", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{}))
	p.step(t, referenceExec("CREATE TABLE clip(label TEXT,flag BOOL,required INT NOT NULL)", 0))
	check("execute", "INSERT INTO clip VALUES ('raw',TRUE)", "23502", `null value in column "required" of relation "clip" violates not-null constraint`, 0, "Failing row contains (raw, t, null).", "", "public", "clip", "required")
	long := strings.Repeat("é", 33)
	clipParameterError := referenceImplicitError("INSERT INTO clip VALUES ($1,$2)", referenceDiagnostic{
		Code: "23502", Message: `null value in column "required" of relation "clip" violates not-null constraint`,
		Detail: "Failing row contains (" + strings.Repeat("é", 32) + "..., t, null).", Schema: "public", Table: "clip", Column: "required",
	}, long, true)
	clipParameterError.errorStage = "execute"
	p.step(t, referencePrepared(clipParameterError))
	p.step(t, referenceSelect("SELECT * FROM clip", []string{"label", "flag", "required"}, []string{"TEXT", "BOOL", "INT4"}, [][]any{}))
}

func TestReferenceInsertImplicitShortQuotedDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec(`CREATE TABLE "t""é"("a""b" INT NOT NULL,label TEXT,flag BOOL)`, 0))
	p.step(t, referenceExec(`INSERT INTO "t""é" VALUES(99,'sentinel',TRUE)`, 1))
	for _, tc := range []struct {
		query, code, message, hint, token string
	}{
		{`INSERT INTO "t""é" VALUES(TRUE)`, "42804", `column "a"b" is of type integer but expression is of type boolean`, "You will need to rewrite or cast the expression.", "TRUE"},
		{`INSERT INTO "t""é"("a""b","a""b") VALUES(1,2)`, "42701", `column "a"b" specified more than once`, "", `"a""b"`},
		{`INSERT INTO "t""é"("m""issing") VALUES(1)`, "42703", `column "m"issing" of relation "t"é" does not exist`, "", `"m""issing"`},
	} {
		position := referencePosition(utf8.RuneCountInString(tc.query[:strings.LastIndex(tc.query, tc.token)]) + 1)
		step := referenceImplicitError(tc.query, referenceDiagnostic{
			Code: tc.code, Message: tc.message, Hint: tc.hint, Position: position,
		})
		step.errorStage = "execute"
		p.step(t, step)
		p.step(t, referenceSelect(`SELECT * FROM "t""é" ORDER BY "a""b"`, []string{`a"b`, "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(99), "sentinel", true}}))
		step.errorStage = "prepare"
		p.step(t, referencePrepared(step))
		p.step(t, referenceSelect(`SELECT * FROM "t""é" ORDER BY "a""b"`, []string{`a"b`, "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(99), "sentinel", true}}))
	}
}

func TestReferenceInsertImplicitShortDumpRoundTrip(t *testing.T) {
	p := newReferencePair(t)
	if _, err := p.native.ExecContext(p.ctx, "CREATE TABLE t(id INT NOT NULL,label TEXT,flag BOOL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.native.ExecContext(p.ctx, "INSERT INTO t VALUES(1),(2)"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.native.ExecContext(p.ctx, "INSERT INTO t(label,id) VALUES('reordered',3)"); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatalf("execute native dump on PostgreSQL: %v\\n%s", err, dump.String())
	}
	p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{
		{int64(1), nil, nil}, {int64(2), nil, nil}, {int64(3), "reordered", nil},
	}))
}

func TestReferenceCatalogCore(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE public.t (id integer)", 0))
	type c struct {
		query, state, message, stage, pos string
		prepared                          bool
	}
	cases := []c{
		{"SELECT id FROM public.missing_table", "42P01", `relation "public.missing_table" does not exist`, "execute", "16", false},
		{"SELECT id FROM public.missing_table", "42P01", `relation "public.missing_table" does not exist`, "prepare", "16", true},
		{"INSERT INTO public.missing_table VALUES (1)", "42P01", `relation "public.missing_table" does not exist`, "execute", "13", false},
		{"INSERT INTO public.missing_table VALUES (1)", "42P01", `relation "public.missing_table" does not exist`, "prepare", "13", true},
		{"CREATE TABLE missing_schema.t (id integer)", "3F000", `schema "missing_schema" does not exist`, "execute", "14", false},
		{"CREATE TABLE missing_schema.t (id integer)", "3F000", `schema "missing_schema" does not exist`, "execute", "14", true},
		{"SELECT id FROM missing_schema.t", "42P01", `relation "missing_schema.t" does not exist`, "execute", "16", false},
		{"SELECT id FROM missing_schema.t", "42P01", `relation "missing_schema.t" does not exist`, "prepare", "16", true},
		{"INSERT INTO missing_schema.t VALUES (1)", "42P01", `relation "missing_schema.t" does not exist`, "execute", "13", false},
		{"INSERT INTO missing_schema.t VALUES (1)", "42P01", `relation "missing_schema.t" does not exist`, "prepare", "13", true},
		{"CREATE TABLE public.t (id integer)", "42P07", `relation "t" already exists`, "execute", "", false},
		{"CREATE TABLE public.t (id integer)", "42P07", `relation "t" already exists`, "execute", "", true},
		{"CREATE TABLE t (id integer)", "42P07", `relation "t" already exists`, "execute", "", false},
		{"CREATE TABLE t (id integer)", "42P07", `relation "t" already exists`, "execute", "", true},
		{"SELECT missing_column FROM public.t", "42703", `column "missing_column" does not exist`, "execute", "8", false},
		{"SELECT missing_column FROM public.t", "42703", `column "missing_column" does not exist`, "prepare", "8", true},
		{"INSERT INTO public.t (missing_column) VALUES (1)", "42703", `column "missing_column" of relation "t" does not exist`, "execute", "23", false},
		{"INSERT INTO public.t (missing_column) VALUES (1)", "42703", `column "missing_column" of relation "t" does not exist`, "prepare", "23", true},
	}
	for _, tc := range cases {
		native := referenceDiagnostic{
			Severity: "ERROR", SeverityUnlocalized: "ERROR",
			Code: tc.state, Message: tc.message,
		}
		// The existing INSERT target binder owns this occurrence position.
		// Current missing-relation/SELECT/CREATE producers have no native cursor.
		if strings.HasPrefix(tc.query, "INSERT INTO public.t (missing_column)") {
			native.Position = tc.pos
		}
		postgres := native
		postgres.Position = tc.pos
		step := referenceStep{
			query: tc.query, prepared: tc.prepared, state: tc.state,
			diagnostic: &native, postgresDiagnostic: &postgres, errorStage: tc.stage,
		}
		p.step(t, step)
	}
	p.step(t, referenceExec("INSERT INTO public.t VALUES (1)", 1))
	p.step(t, referenceSelect("SELECT id FROM public.t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	p.step(t, referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	p.step(t, referencePrepared(referenceExec("INSERT INTO public.t VALUES ($1)", 1, 1)))
	p.step(t, referencePrepared(referenceSelect("SELECT id FROM public.t WHERE id = $1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}, 1)))
	p.step(t, referencePrepared(referenceSelect("SELECT id FROM t WHERE id = $1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}, 1)))
}

func TestReferenceStringLiterals(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,label TEXT,flag BOOL)", 0))
	want := [][]any{}
	for i, tc := range stringLiteralValues() {
		for _, prepared := range []bool{false, true} {
			id := len(want) + 1
			s := referenceExec(fmt.Sprintf("INSERT INTO t VALUES (%d,%s,TRUE)", id, tc.source), 1)
			if prepared {
				s = referencePrepared(s)
			}
			p.step(t, s)
			want = append(want, []any{int64(id), tc.text, true})
		}
		if tc.source == `$$a;'b$$` {
			s := referenceSelect(`SELECT id FROM t WHERE label=$$a;'b$$ ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(2*i + 1)}, {int64(2*i + 2)}})
			p.step(t, s)
			p.step(t, referencePrepared(s))
		}
	}
	p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, want))
	p.step(t, referencePrepared(referenceSelect(`SELECT id FROM t WHERE label=E'a\nb' ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})))
	p.step(t, referencePrepared(referenceExec(`INSERT INTO t VALUES ($1,$2,E'\164')`, 1, 100, `a\nb`)))
	p.step(t, referencePrepared(referenceExec(`INSERT INTO t VALUES ($1,$2,E'\164')`, 1, 101, `a\nb`)))
	p.step(t, referenceSelect("SELECT label,flag FROM t WHERE id=100", []string{"label", "flag"}, []string{"TEXT", "BOOL"}, [][]any{{`a\nb`, true}}))
	p.step(t, referenceExec("CREATE TABLE inputs(id INT,big BIGINT,flag BOOL)", 0))
	p.step(t, referenceExec(`INSERT INTO inputs VALUES (E'\061',E'\062',E'\164')`, 1))
	p.step(t, referencePrepared(referenceExec(`INSERT INTO inputs VALUES (E'\061',E'\062',E'\164')`, 1)))
	p.step(t, referenceSelect("SELECT * FROM inputs", []string{"id", "big", "flag"}, []string{"INT4", "INT8", "BOOL"}, [][]any{{int64(1), int64(2), true}, {int64(1), int64(2), true}}))
	p.step(t, referenceExec("CREATE TABLE dollar_params(id INT,label TEXT)", 0))
	p.step(t, referenceExec(`INSERT INTO dollar_params VALUES ($1,$tag$$2;$tag$)`, 1, 7))
	p.step(t, referencePrepared(referenceExec(`INSERT INTO dollar_params VALUES ($1,$tag$$2;$tag$)`, 1, 8)))
	p.step(t, referenceSelect("SELECT * FROM dollar_params ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "$2;"}, {int64(8), "$2;"}}))
	p.step(t, referencePrepared(referenceSelect(`SELECT id FROM dollar_params WHERE label=$$ $1 $$ ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{})))
	p.step(t, referenceExec("CREATE TABLE separated(x$tag$x$tag$ TEXT)", 0))
	p.step(t, referenceExec(`INSERT INTO separated(x$tag$x$tag$) VALUES ($$v$$)`, 1))
	p.step(t, referencePrepared(referenceExec(`INSERT INTO separated(x$tag$x$tag$) VALUES ($$v$$)`, 1)))
	p.step(t, referenceSelect("SELECT x$tag$x$tag$ FROM separated ORDER BY x$tag$x$tag$", []string{"x$tag$x$tag$"}, []string{"TEXT"}, [][]any{{"v"}, {"v"}}))
	phantomNative := referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: "42P01", Message: `relation "phantom" does not exist`,
	}
	// The existing native missing-relation producer omits Position; PostgreSQL
	// supplies the relation occurrence. Assert both full records before parity
	// normalizes this declared difference, as in the conversion fixtures.
	phantomPostgres := phantomNative
	phantomPostgres.Position = "15"
	p.step(t, referenceStep{
		query: "SELECT * FROM phantom", selectRows: true, state: "42P01",
		diagnostic: &phantomNative, postgresDiagnostic: &phantomPostgres, errorStage: "execute",
	})
	for _, prepared := range []bool{false, true} {
		s := referenceErrorDiagnostic("INSERT INTO t VALUES$$v$$", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "VALUES$$v$$"`, Position: "15"})
		s.errorStage = "execute"
		if prepared {
			s = referencePrepared(s)
			s.errorStage = "prepare"
		}
		p.step(t, s)
	}
	for _, tc := range []struct{ column, typ string }{{"id", "integer"}, {"big", "bigint"}, {"flag", "boolean"}} {
		q := "INSERT INTO inputs (" + tc.column + ") VALUES ('1'),(E'\\x71')"
		position := strconv.Itoa(strings.Index(q, "E'") + 1)
		p.step(t, referenceErrorDiagnostic(q, referenceDiagnostic{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02",
			Message: "invalid input syntax for type " + tc.typ + `: "q"`, Position: position,
		}))
		p.step(t, referenceSelect("SELECT * FROM inputs", []string{"id", "big", "flag"}, []string{"INT4", "INT8", "BOOL"}, [][]any{{int64(1), int64(2), true}, {int64(1), int64(2), true}}))
	}
	p.step(t, referenceExec("CREATE TABLE failures(label TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO failures VALUES ('seed')", 1))
	for _, tc := range stringLiteralErrors() {
		for _, prefix := range []string{"", "/*é*/ ", "/*😀*/ "} {
			position := tc.position
			if prefix != "" && position != "" {
				n, _ := strconv.Atoi(position)
				position = strconv.Itoa(n + 6)
			}
			q := prefix + "INSERT INTO t VALUES (" + tc.source
			// Lexical errors precede semantic row-width/type checking.
			diag := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: position, Hint: tc.hint}
			for _, prepared := range []bool{false, true} {
				s := referenceErrorDiagnostic(q, diag)
				s.errorStage = "execute"
				if prepared {
					s = referencePrepared(s)
					s.errorStage = "prepare"
				}
				p.step(t, s)
			}
			queryStep := referenceErrorDiagnostic(q, diag)
			queryStep.selectRows = true
			queryStep.errorStage = "execute"
			p.step(t, queryStep)
		}
		q := "INSERT INTO failures VALUES ('valid'),(" + tc.source
		position := tc.position
		if position != "" {
			n, _ := strconv.Atoi(position)
			position = strconv.Itoa(n + 17)
		}
		diag := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: position, Hint: tc.hint}
		for _, prepared := range []bool{false, true} {
			s := referenceErrorDiagnostic(q, diag)
			s.errorStage = "execute"
			if prepared {
				s = referencePrepared(s)
				s.errorStage = "prepare"
			}
			p.step(t, s)
			p.step(t, referenceSelect("SELECT * FROM failures", []string{"label"}, []string{"TEXT"}, [][]any{{"seed"}}))
		}
	}
	s := referenceErrorDiagnostic("CREATE TABLE fresh(x TEXT); INSERT INTO fresh VALUES ($$a)", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `unterminated dollar-quoted string at or near "$$a)"`, Position: "55"})
	s.errorStage = "execute"
	p.step(t, s)
	freshNative := referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: "42P01", Message: `relation "fresh" does not exist`,
	}
	// Use the same documented missing-relation Position difference as phantom.
	freshPostgres := freshNative
	freshPostgres.Position = "15"
	p.step(t, referenceStep{
		query: "SELECT * FROM fresh", selectRows: true, state: "42P01",
		diagnostic: &freshNative, postgresDiagnostic: &freshPostgres, errorStage: "execute",
	})
	p.step(t, referenceSelect("SELECT * FROM failures", []string{"label"}, []string{"TEXT"}, [][]any{{"seed"}}))
}

func TestReferenceStringLiteralDumpRoundTrip(t *testing.T) {
	p := newReferencePair(t)
	for _, q := range []string{"CREATE TABLE t(id INT,label TEXT)", `INSERT INTO t VALUES (1,E'a\n\b\f\r\t\vO\'Brien\\tail'),(2,E'\303\251\uD83D\uDE00\uFFFF'),(3,E''),(4,NULL)`} {
		if _, err := p.native.ExecContext(p.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.native.ExecContext(p.ctx, "INSERT INTO t VALUES (5,$$a;'b$$),(6,$T$first\nsecond; O'Brien\\tail $1 $other$x$other$$T$),(7,$$$$),(8,$_9$é😀$_9$)"); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatalf("exact dump: %v\n%s", err, dump.String())
	}
	p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{
		{int64(1), "a\n\b\f\r\t\vO'Brien\\tail"}, {int64(2), "é😀\uFFFF"}, {int64(3), ""}, {int64(4), nil},
		{int64(5), "a;'b"}, {int64(6), "first\nsecond; O'Brien\\tail $1 $other$x$other$"}, {int64(7), ""}, {int64(8), "é😀"},
	}))
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("dump changed")
	}
}

func TestReferenceNumericLiteralsValues(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			step := func(s referenceStep) { t.Helper(); s.prepared = prepared; p.step(t, s) }
			step(referenceExec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
			step(referenceExec("INSERT INTO t VALUES (1,0b1010,0o12),(2,0xa,1_000)", 2))
			step(referenceSelect("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(10), int64(10)}, {int64(2), int64(10), int64(1000)}}))
			p.step(t, referencePrepared(referenceExec("INSERT INTO t VALUES ($1,0B_1_010,0X_A)", 1, int64(3))))
			p.step(t, referencePrepared(referenceSelect("SELECT id,i,b FROM t WHERE i=0b1010 ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(10), int64(10)}, {int64(2), int64(10), int64(1000)}, {int64(3), int64(10), int64(10)}})))
			p.step(t, referenceSelect("SELECT id,i,b FROM t WHERE b=1_000", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(2), int64(10), int64(1000)}}))
			step(referenceExec("CREATE TABLE endpoints(id INT,b BIGINT)", 0))
			var want [][]any
			for i, literal := range numericEndpoints() {
				step(referenceExec(fmt.Sprintf("INSERT INTO endpoints VALUES (%d,%s)", i, literal), 1))
				value := int64(9223372036854775807)
				if i%2 == 1 {
					value = -9223372036854775808
				}
				want = append(want, []any{int64(i), value})
			}
			step(referenceSelect("SELECT id,b FROM endpoints ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT8"}, want))
			step(referenceExec("CREATE TABLE bounds(i INT,b BIGINT,label TEXT)", 0))
			step(referenceExec("INSERT INTO bounds VALUES (-0x8000_0000,0x8000_0000,0xa),(0x7FFF_FFFF,010,1_000)", 2))
			step(referenceSelect("SELECT i,b,label FROM bounds ORDER BY i", []string{"i", "b", "label"}, []string{"INT4", "INT8", "TEXT"}, [][]any{{int64(-2147483648), int64(2147483648), "10"}, {int64(2147483647), int64(10), "1000"}}))
		})
	}
}

func TestReferenceNumericLiteralsDiagnosticsAtomicity(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(x INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES (99)", 1))
	check := func(query, code, message, position string) {
		t.Helper()
		want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
		for _, operation := range []string{"exec", "query", "prepare"} {
			t.Run(operation+"/"+query, func(t *testing.T) {
				s := referenceErrorDiagnostic(query, want)
				s.errorStage = "execute"
				if operation == "prepare" {
					s.prepared = true
					s.errorStage = "prepare"
				}
				if operation == "query" {
					s.selectRows = true
				}
				p.step(t, s)
				p.step(t, referenceSelect("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(99)}}))
			})
		}
	}
	for _, c := range numericLiteralErrors() {
		message := c.reason + ` at or near "` + c.token + `"`
		check("INSERT INTO t VALUES ("+c.token+")", "42601", message, "23")
		check("INSERT INTO t VALUES (1),("+c.token+")", "42601", message, "27")
	}
	check("INSERT INTO t VALUES (-0b2)", "42601", `trailing junk after numeric literal at or near "0b2"`, "24")
	check("/*é*/ INSERT INTO t VALUES (0b2)", "42601", `trailing junk after numeric literal at or near "0b2"`, "29")
	check("/*😀*/ INSERT INTO t VALUES (0x_)", "42601", `invalid hexadecimal integer at or near "0x_"`, "29")
	for _, operation := range []string{"exec", "query"} {
		s := referenceErrorDiagnostic("INSERT INTO t VALUES (1); INSERT INTO t VALUES (0b2)", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `trailing junk after numeric literal at or near "0b2"`, Position: "49"})
		s.selectRows = operation == "query"
		s.errorStage = "execute"
		p.step(t, s)
		p.step(t, referenceSelect("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(99)}}))
	}
	// A representable int8 literal prepares successfully; assignment to int4
	// fails only at execution, without publishing the preceding valid row.
	for _, prepared := range []bool{false, true} {
		s := referenceErrorDiagnostic("INSERT INTO t VALUES (1),(0x8000_0000)", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "integer out of range"})
		s.prepared = prepared
		s.errorStage = "execute"
		p.step(t, s)
		p.step(t, referenceSelect("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(99)}}))
	}
	p.step(t, referenceSelect("SELECT x FROM t WHERE x=0x8000_0000", []string{"x"}, []string{"INT4"}, nil))
}

func TestReferenceNumericLiteralsDumpRoundTrip(t *testing.T) {
	p := newReferencePair(t)
	script := "CREATE TABLE t(id INT,b BIGINT); INSERT INTO t VALUES (1,0b1010),(2,1_000),(3,0x7FFF_FFFF_FFFF_FFFF),(4,-0x8000_0000_0000_0000)"
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"10", "1000", "9223372036854775807", "-9223372036854775808"} {
		if !strings.Contains(dump.String(), value) {
			t.Fatalf("dump missing %s: %s", value, dump.String())
		}
	}
	if strings.Contains(dump.String(), "0x") || strings.Contains(dump.String(), "0b") || strings.Contains(dump.String(), "1_000") {
		t.Fatalf("noncanonical dump: %s", dump.String())
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal(err)
	}
	p.step(t, referenceSelect("SELECT id,b FROM t ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(1000)}, {int64(3), int64(9223372036854775807)}, {int64(4), int64(-9223372036854775808)}}))
}

func TestReferenceNumericLiteralsSpellings(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE TABLE t(id INT,b BIGINT)", 0))
			var want [][]any
			for i, c := range numericLiteralSpellings() {
				s := referenceExec(fmt.Sprintf("INSERT INTO t VALUES (%d,%s)", i, c.literal), 1)
				s.prepared = prepared
				p.step(t, s)
				want = append(want, []any{int64(i), c.value})
			}
			s := referenceSelect("SELECT id,b FROM t ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT8"}, want)
			s.prepared = prepared
			p.step(t, s)
		})
	}
}

func TestReferenceBooleanDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE b(id INT,flag BOOL)", 0))
	diagnostic := func(message, position string) referenceDiagnostic {
		return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02",
			Message: message, Position: position}
	}
	literal := func(query, message, position string) {
		t.Helper()
		for _, operation := range []string{"exec", "query", "prepare"} {
			s := referenceErrorDiagnostic(query, diagnostic(message, position))
			s.selectRows = operation == "query"
			s.prepared = operation == "prepare"
			s.errorStage = "execute"
			if s.prepared {
				s.errorStage = "prepare"
			}
			p.step(t, s)
		}
	}
	literal("SELECT id FROM b WHERE flag='o'", `invalid input syntax for type boolean: "o"`, "29")
	p.step(t, referenceExec("INSERT INTO b VALUES (99,TRUE)", 1))
	sentinel := referenceSelect("SELECT id,flag FROM b ORDER BY id", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(99), true}})
	for _, input := range booleanInvalidInputs {
		literal("INSERT INTO b VALUES (1,"+input.source+")", input.message, "25")
		literal("SELECT id FROM b WHERE flag="+input.source, input.message, "29")
		p.step(t, sentinel)
	}
	for _, tc := range []struct{ query, message, position string }{
		{"/*é*/ INSERT INTO b VALUES (1,'o')", `invalid input syntax for type boolean: "o"`, "31"},
		{"/*é*/ SELECT id FROM b WHERE flag='o'", `invalid input syntax for type boolean: "o"`, "35"},
		{`SELECT id FROM b WHERE flag=E'\x6f'`, `invalid input syntax for type boolean: "o"`, "29"},
		{"SELECT id FROM b WHERE flag=$tag$o$tag$", `invalid input syntax for type boolean: "o"`, "29"},
		{"SELECT id FROM b WHERE flag='tr'\n'uth'", `invalid input syntax for type boolean: "truth"`, "29"},
		{"INSERT INTO b VALUES (1,TRUE),(2,'o')", `invalid input syntax for type boolean: "o"`, "34"},
	} {
		literal(tc.query, tc.message, tc.position)
		p.step(t, sentinel)
	}
	parameter := func(query, message, context string, args ...any) {
		t.Helper()
		for _, prepared := range []bool{false, true} {
			for _, selectRows := range []bool{false, true} {
				native := diagnostic(message, "")
				postgres := native
				postgres.Context = context
				s := referenceErrorDiagnostic(query, native, args...)
				s.postgresDiagnostic = &postgres
				s.prepared, s.selectRows, s.errorStage = prepared, selectRows, "execute"
				p.step(t, s)
			}
		}
	}
	for _, input := range booleanInvalidInputs {
		quoted := "'...'"
		if input.value == "" {
			quoted = "''"
		}
		for _, value := range []any{input.value, []byte(input.value)} {
			parameter("INSERT INTO b VALUES ($1,$2)", input.message, "unnamed portal parameter $2 = "+quoted, 1, value)
			parameter("SELECT id FROM b WHERE flag=$1", input.message, "unnamed portal parameter $1 = "+quoted, value)
			p.step(t, sentinel)
		}
	}
	parameter("INSERT INTO b VALUES (1,TRUE),(2,$1)", `invalid input syntax for type boolean: "o"`, "unnamed portal parameter $1 = '...'", "o")
	p.step(t, sentinel)
	p.step(t, referenceExec("CREATE TABLE repeated(flag BOOL)", 0))
	p.step(t, referenceExec("CREATE TABLE precedence(a BOOL,b BOOL)", 0))
	parameter("INSERT INTO repeated VALUES ($1),($1)", `invalid input syntax for type boolean: "o"`, "unnamed portal parameter $1 = '...'", "o")
	parameter("INSERT INTO precedence VALUES ($2,$1)", `invalid input syntax for type boolean: "truth"`, "unnamed portal parameter $1 = '...'", "truth", "o")
	p.step(t, referenceSelect("SELECT flag FROM repeated", []string{"flag"}, []string{"BOOL"}, nil))
	p.step(t, referenceSelect("SELECT a,b FROM precedence", []string{"a", "b"}, []string{"BOOL", "BOOL"}, nil))
	p.step(t, referenceExec("CREATE TABLE accepted(flag BOOL)", 0))
	for _, group := range []struct {
		values []string
		want   bool
	}{
		{[]string{"t", "tr", "tru", "true", "y", "ye", "yes", "on", "1", "TrUe", " YES ", "\tOn\n"}, true},
		{[]string{"f", "fa", "fal", "fals", "false", "n", "no", "of", "off", "0", "FaLsE", " NO ", "\tOfF\n"}, false},
	} {
		for index, value := range group.values {
			p.step(t, referencePrepared(referenceExec("INSERT INTO accepted VALUES ($1)", 1, []byte(value))))
			rows := make([][]any, index+1)
			for i := range rows {
				rows[i] = []any{group.want}
			}
			p.step(t, referenceSelect("SELECT flag FROM accepted WHERE flag=$1",
				[]string{"flag"}, []string{"BOOL"}, rows, value))
		}
	}
	p.step(t, referenceExec("INSERT INTO repeated VALUES (TRUE),(FALSE),(NULL)", 3))
	for _, value := range []any{true, false, nil, int64(1), int64(0)} {
		p.step(t, referenceExec("INSERT INTO repeated VALUES ($1)", 1, value))
	}
	p.step(t, referenceSelect("SELECT flag FROM repeated ORDER BY flag", []string{"flag"}, []string{"BOOL"},
		[][]any{{false}, {false}, {false}, {true}, {true}, {true}, {nil}, {nil}}))
	p.step(t, referenceSelect("SELECT flag FROM repeated WHERE flag=NULL", []string{"flag"}, []string{"BOOL"}, nil))
	// A successfully prepared handle survives conversion failure on each backend.
	for _, backend := range []struct {
		db       *sql.DB
		postgres bool
	}{{p.memory, false}, {p.postgres, true}} {
		stmt, err := backend.db.PrepareContext(p.ctx, "INSERT INTO b VALUES ($1,$2)")
		if err != nil {
			t.Fatal(err)
		}
		_, err = stmt.ExecContext(p.ctx, 1, "o")
		want := diagnostic(`invalid input syntax for type boolean: "o"`, "")
		if backend.postgres {
			want.Context = "unnamed portal parameter $2 = '...'"
		}
		referenceAssertDiagnostic(t, err, &want)
		for i, value := range []any{true, false, nil} {
			if _, err := stmt.ExecContext(p.ctx, i+1, value); err != nil {
				stmt.Close()
				t.Fatal(err)
			}
		}
		stmt.Close()
	}
	p.step(t, referenceSelect("SELECT id,flag FROM b ORDER BY id", []string{"id", "flag"}, []string{"INT4", "BOOL"},
		[][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}, {int64(99), true}}))
	// Controls have no PostgreSQL API analogue: assert native staging, then execute
	// the exact native dump on PostgreSQL as the independent serialization witness.
	cursor, err := p.native.QueryContext(p.ctx, "SELECT flag FROM b ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	err = stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE replacement(flag BOOL); INSERT INTO replacement VALUES (TRUE),('o');"))
	wantBooleanDiagnostic(t, err, `invalid input syntax for type boolean: "o"`, "77")
	wantTuples(t, p.native, "SELECT id,flag FROM b ORDER BY id",
		[][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}, {int64(99), true}})
	wantBooleanMetadata(t, p.native, "SELECT flag FROM b")
	_, err = p.native.Query("SELECT flag FROM replacement")
	sqlState(t, err, "42P01")
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE b,accepted,repeated,precedence"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal(err)
	}
	p.step(t, referenceSelect("SELECT flag FROM b ORDER BY id", []string{"flag"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}, {true}}))
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatalf("boolean redump changed: %v", err)
	}
	for _, want := range []any{true, false, nil, true} {
		if !cursor.Next() {
			t.Fatalf("retained cursor ended: %v", cursor.Err())
		}
		var got any
		if err := cursor.Scan(&got); err != nil || got != want {
			t.Fatalf("retained bool = %#v, %v", got, err)
		}
	}
	if cursor.Next() || cursor.Err() != nil {
		t.Fatalf("retained cursor tail: %v", cursor.Err())
	}
}

func TestReferenceUnicodeEscapesValues(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,label TEXT)", 0))
	want := [][]any{}
	for _, tc := range unicodeEscapeValues() {
		for _, prepared := range []bool{false, true} {
			s := referenceExec(fmt.Sprintf("INSERT INTO t VALUES (%d,%s)", len(want)+1, tc.source), 1)
			s.prepared = prepared
			p.step(t, s)
			want = append(want, []any{int64(len(want) + 1), tc.text})
		}
		ids := [][]any{}
		for _, row := range want {
			if row[1] == tc.text {
				ids = append(ids, []any{row[0]})
			}
		}
		s := referenceSelect("SELECT id FROM t WHERE label="+tc.source+" ORDER BY id", []string{"id"}, []string{"INT4"}, ids)
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, want))
	p.step(t, referencePrepared(referenceExec("INSERT INTO t VALUES ($1,$2)", 1, 100, `\0041`)))
	p.step(t, referenceSelect("SELECT label FROM t WHERE id=100", []string{"label"}, []string{"TEXT"}, [][]any{{`\0041`}}))
	p.step(t, referenceExec(`SET application_name TO U&'\0041'`, 0))
	p.step(t, referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"A"}}))
}

func TestReferenceUnicodeEscapesDiagnosticsAtomicity(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(x TEXT NOT NULL)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES ('seed')", 1))
	check := func(q string, diag referenceDiagnostic) {
		t.Helper()
		for _, op := range []string{"exec", "query", "prepare"} {
			s := referenceErrorDiagnostic(q, diag)
			s.errorStage = "execute"
			if op == "query" {
				s.selectRows = true
			}
			if op == "prepare" {
				s.prepared = true
				s.errorStage = "prepare"
			}
			p.step(t, s)
			p.step(t, referenceSelect("SELECT * FROM t", []string{"x"}, []string{"TEXT"}, [][]any{{"seed"}}))
		}
	}
	for _, tc := range unicodeEscapeErrors() {
		for _, prefix := range []string{"", "/*é*/ ", "/*😀*/ "} {
			n, _ := strconv.Atoi(tc.position)
			if prefix != "" {
				n += 6
			}
			check(prefix+"INSERT INTO t VALUES ("+tc.source, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: strconv.Itoa(n), Hint: tc.hint})
		}
		n, _ := strconv.Atoi(tc.position)
		check("INSERT INTO t VALUES ('valid'),("+tc.source, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: strconv.Itoa(n + 10), Hint: tc.hint})
	}
	for _, tc := range []struct{ q, message, position, hint string }{
		{`CREATE TABLE U&"a""\061"(x TEXT)`, "invalid Unicode escape", "19", `Unicode escapes must be \XXXX or \+XXXXXX.`},
		{`SELECT * FROM U&"a""😀\061"`, "invalid Unicode escape", "22", `Unicode escapes must be \XXXX or \+XXXXXX.`},
		{`CREATE TABLE U&""(x TEXT)`, `zero-length delimited identifier at or near "U&"""`, "14", ""},
		{`CREATE TABLE U&"abc`, `unterminated quoted identifier at or near "U&"abc"`, "14", ""},
	} {
		check(tc.q, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: tc.position, Hint: tc.hint})
	}
	// PostgreSQL rejects prepared multiple commands independently; exercise its
	// direct script parse-before-execute and native's whole-script parsing.
	s := referenceErrorDiagnostic(`CREATE TABLE fresh(x TEXT); INSERT INTO fresh VALUES (U&'\061')`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "invalid Unicode escape", Position: "58", Hint: `Unicode escapes must be \XXXX or \+XXXXXX.`})
	s.errorStage = "execute"
	p.step(t, s)
	s = referenceErrorDiagnostic(`CREATE TABLE fresh(x TEXT); INSERT INTO fresh VALUES (U&'a''😀\061')`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "invalid Unicode escape", Position: "62", Hint: `Unicode escapes must be \XXXX or \+XXXXXX.`})
	s.errorStage = "execute"
	p.step(t, s)
	native := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "fresh" does not exist`}
	pg := native
	pg.Position = "15"
	p.step(t, referenceStep{query: "SELECT * FROM fresh", selectRows: true, state: "42P01", diagnostic: &native, postgresDiagnostic: &pg, errorStage: "execute"})
	// This is the exact malformed script used by the native Restore witness.
	// Exercise PostgreSQL's direct SQL path, not an invented Restore analogue,
	// to independently establish its full Position 101 record and no effects.
	staged := referenceErrorDiagnostic(`CREATE TABLE staged(x TEXT); INSERT INTO staged VALUES ('valid'); INSERT INTO staged VALUES (U&'a''😀\061')`, referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: "42601", Message: "invalid Unicode escape", Position: "101",
		Hint: `Unicode escapes must be \XXXX or \+XXXXXX.`,
	})
	staged.errorStage = "execute"
	p.step(t, staged)
	stagedNative := referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: "42P01", Message: `relation "staged" does not exist`,
	}
	stagedPostgres := stagedNative
	stagedPostgres.Position = "15"
	p.step(t, referenceStep{
		query: "SELECT * FROM staged", selectRows: true, state: "42P01",
		diagnostic: &stagedNative, postgresDiagnostic: &stagedPostgres, errorStage: "execute",
	})
	p.step(t, referenceSelect("SELECT * FROM t", []string{"x"}, []string{"TEXT"}, [][]any{{"seed"}}))
}

func TestReferenceUnicodeEscapesNames(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			for _, tc := range unicodeEscapeNames() {
				quoted := `"` + strings.ReplaceAll(tc.name, `"`, `""`) + `"`
				for _, s := range []referenceStep{
					referenceExec("CREATE TABLE "+tc.source+`(id INT,U&"\+01F600" TEXT)`, 0),
					referenceExec("INSERT INTO "+tc.source+` VALUES (1,U&'\0041')`, 1),
					referenceExec("INSERT INTO "+quoted+` VALUES (2,U&'\+01F600')`, 1),
					referenceSelect("SELECT * FROM "+tc.source+" ORDER BY id", []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}),
					referenceSelect("SELECT * FROM "+quoted+" ORDER BY id", []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}),
				} {
					s.prepared = prepared
					p.step(t, s)
				}
			}
			native := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "a" does not exist`}
			pg := native
			pg.Position = "15"
			s := referenceStep{query: "SELECT * FROM a", selectRows: true, state: "42P01", diagnostic: &native, postgresDiagnostic: &pg, errorStage: "execute"}
			if prepared {
				s.prepared = true
				s.errorStage = "prepare"
			}
			p.step(t, s)
			p.step(t, referenceErrorDiagnostic(`CREATE TABLE U&"`+strings.Repeat(`\0062`, 65)+`"(x TEXT)`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P07", Message: `relation "` + strings.Repeat("b", 63) + `" already exists`}))
			p.step(t, referenceSelect(`SELECT * FROM U&"\0070ublic".U&"\0041" ORDER BY id`, []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}))
		})
	}
}

func TestReferenceUnicodeEscapesDumpRoundTrip(t *testing.T) {
	p := newReferencePair(t)
	for _, q := range []string{
		`CREATE TABLE U&"\0041"(id INT NOT NULL,U&"\+01F600" TEXT)`,
		`INSERT INTO "A" VALUES (1,U&'\0041'),(2,U&'\D83D\DE00'),(3,U&''),(4,NULL),(5,U&'O''Brien\\\0001')`,
	} {
		if _, err := p.native.ExecContext(p.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "U&") || !strings.Contains(dump.String(), `"😀"`) {
		t.Fatalf("canonical names %s", dump.String())
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatalf("exact native dump %v\n%s", err, dump.String())
	}
	p.step(t, referenceSelect(`SELECT * FROM "A" ORDER BY id`, []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}, {int64(3), ""}, {int64(4), nil}, {int64(5), "O'Brien\\\x01"}}))
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("redump changed")
	}
}
