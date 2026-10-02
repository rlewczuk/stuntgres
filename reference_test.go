//go:build reference

package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres"
)

type referenceStep struct {
	query          string
	args           []any
	prepared       bool
	selectRows     bool
	affected       int64
	state          string
	columns, types []string
	rows           [][]any
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
			return referenceObserved{state: referenceState(t, err, s.state)}
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
			return referenceObserved{state: state}
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
		return referenceObserved{state: state}
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
