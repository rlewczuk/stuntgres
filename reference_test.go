//go:build reference

package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"github.com/lib/pq"
	"time"
	"strings"
	"testing"

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
	query          string
	args           []any
	prepared       bool
	selectRows     bool
	affected       int64
	state          string
	columns, types []string
	rows           [][]any
	diagnostic     *referenceDiagnostic
}

func referenceExec(query string, count int64, args ...any) referenceStep {
	return referenceStep{query: query, args: args, affected: count}
}
func referenceError(query, state string, args ...any) referenceStep {
	return referenceStep{query: query, args: args, state: state}
}
func referenceSelect(query string, columns, types []string, rows [][]any, args ...any) referenceStep {
	return referenceStep{query: query, args: args, selectRows: true, columns: columns, types: types, rows: rows}
}
func referencePrepared(s referenceStep) referenceStep { s.prepared = true; return s }

type referenceObserved struct {
	state          string
	affected       int64
	columns, types []string
	rows           [][]any
	diagnostic     *referenceDiagnostic
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

func referenceRunStep(t *testing.T, ctx context.Context, db *sql.DB, recorder *referenceRecorder, s referenceStep) referenceObserved {
	t.Helper()
	ctx = context.WithValue(ctx, referenceRecorderKey{}, recorder)
	var stmt *sql.Stmt
	var err error
	if s.prepared {
		stmt, err = db.PrepareContext(ctx, s.query)
		if err != nil {
			state := referenceState(t, err, s.state)
			var diagnostic *referenceDiagnostic
			if s.diagnostic != nil {
				got := referenceAssertDiagnostic(t, err, s.diagnostic)
				diagnostic = &got
			}
			return referenceObserved{state: state, diagnostic: diagnostic}
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
			var diagnostic *referenceDiagnostic
			if s.diagnostic != nil {
				got := referenceAssertDiagnostic(t, err, s.diagnostic)
				diagnostic = &got
			}
			return referenceObserved{state: state, diagnostic: diagnostic}
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
	state := referenceState(t, err, s.state)
	if err != nil {
		var diagnostic *referenceDiagnostic
		if s.diagnostic != nil {
			got := referenceAssertDiagnostic(t, err, s.diagnostic)
			diagnostic = &got
		}
		return referenceObserved{state: state, diagnostic: diagnostic}
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
		Detail: "Failing row contains (null, bad).", Schema: "public",
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

func TestReferenceConditionalErrorFields(t *testing.T) {
	p := newReferencePair(t)
	assertPostgres := func(query string, want referenceDiagnostic) referenceDiagnostic {
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
			got := assertPostgres(tc.query, tc.want)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("conditional diagnostic = %#v, want %#v", got, tc.want)
			}
		})
	}
	internal := assertPostgres(`DO $$ BEGIN EXECUTE 'SELECT 1 +'; END $$;`, referenceDiagnostic{})
	if internal.Code != "42601" || internal.Message != "syntax error at end of input" ||
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
		if _, err := referenceAdmin.ExecContext(context.Background(), "DROP OWNED BY error_fields_denied; DROP ROLE IF EXISTS error_fields_denied"); err != nil {
			t.Errorf("drop reference role: %v", err)
		}
	}()
	roleConn, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := roleConn.ExecContext(p.ctx, "SET ROLE error_fields_denied"); err != nil {
		roleConn.Close()
		t.Fatal(err)
	}
	_, err = roleConn.QueryContext(p.ctx, "SELECT id FROM diagnostic_rows")
	if err == nil {
		roleConn.Close()
		t.Fatal("restricted SELECT succeeded")
	}
	permission := referenceExtractDiagnostic(t, fmt.Errorf("wrapped: %w", err))
	if permission.Code != "42501" || permission.Severity != "ERROR" ||
		permission.Message != "permission denied for table diagnostic_rows" ||
		permission.Schema != "" || permission.Table != "" || permission.Column != "" {
		roleConn.Close()
		t.Fatalf("permission diagnostic = %#v", permission)
	}
	if _, err := roleConn.ExecContext(p.ctx, "RESET ROLE"); err != nil {
		roleConn.Close()
		t.Fatal(err)
	}
	if err := roleConn.Close(); err != nil {
		t.Fatal(err)
	}
	cancelConn, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelConn.Close()
	if _, err := cancelConn.ExecContext(p.ctx, "SET statement_timeout='50ms'"); err != nil {
		t.Fatal(err)
	}
	timeoutCtx, timeoutCancel := context.WithTimeout(p.ctx, 5*time.Second)
	_, err = cancelConn.ExecContext(timeoutCtx, "SELECT pg_sleep(10)")
	timeoutCancel()
	timeout := referenceExtractDiagnostic(t, fmt.Errorf("wrapped: %w", err))
	if timeout.Code != "57014" || timeout.Severity != "ERROR" || timeout.Message != "canceling statement due to statement timeout" {
		t.Fatalf("timeout diagnostic = %#v", timeout)
	}
	if _, err := cancelConn.ExecContext(p.ctx, "SET statement_timeout=0"); err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := cancelConn.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	queryCtx, queryCancel := context.WithTimeout(p.ctx, 8*time.Second)
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
	user := referenceExtractDiagnostic(t, fmt.Errorf("wrapped: %w", userErr))
	if user.Code != "57014" || user.Severity != "ERROR" || user.Message != "canceling statement due to user request" {
		t.Fatalf("user cancellation diagnostic = %#v", user)
	}
}
