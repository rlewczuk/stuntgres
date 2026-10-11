package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceTypeCoreInputsInferenceAndMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE typed(i INTEGER NOT NULL,b BIGINT,t TEXT,f BOOLEAN)", 0))
	p.Step(t, fixture.Exec("INSERT INTO typed VALUES(-2147483648,-9223372036854775808,TRUE,' ye '),(2147483647,9223372036854775807,FALSE,'of')", 2))
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			q := "INSERT INTO typed VALUES($1,$2,$3,$4)"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			owned := []byte("owned")
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, "0x_FF", "0b10_10", owned, "on"))
			owned[0] = 'X'
			for _, s := range []string{" +0007 ", "0o17", "1_000"} {
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, s, s, "x", nil))
			}
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			rows, err := backend.DB.QueryContext(ctx, "SELECT * FROM typed")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			scans := []reflect.Type{reflect.TypeFor[int64](), reflect.TypeFor[int64](), reflect.TypeFor[string](), reflect.TypeFor[bool]()}
			if backend.PG {
				scans[0] = reflect.TypeFor[int32]()
			}
			for i, name := range []string{"INT4", "INT8", "TEXT", "BOOL"} {
				nullable, known := types[i].Nullable()
				if types[i].DatabaseTypeName() != name || types[i].ScanType() != scans[i] || known != !backend.PG || nullable != (!backend.PG && i != 0) {
					t.Fatal(types[i], nullable, known)
				}
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
	p.Step(t, fixture.Select("SELECT * FROM typed ORDER BY i", []string{"i", "b", "t", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(-2147483648), int64(-9223372036854775808), "true", true}, {int64(7), int64(7), "x", nil}, {int64(15), int64(15), "x", nil}, {int64(255), int64(10), "owned", true}, {int64(1000), int64(1000), "x", nil}, {int64(2147483647), int64(9223372036854775807), "false", false}}))
	// The original reference literal routes remain distinct from held frames.
	p.Step(t, fixture.Exec("CREATE TABLE literal_inputs(i INT,b BIGINT)", 0))
	for _, s := range []string{" +0007 ", "0o17", "1_000"} {
		p.Step(t, fixture.Exec("INSERT INTO literal_inputs VALUES('"+s+"','"+s+"')", 1))
	}
	p.Step(t, fixture.Select("SELECT * FROM literal_inputs ORDER BY i", []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(7), int64(7)}, {int64(15), int64(15)}, {int64(1000), int64(1000)}}))
	p.Step(t, fixture.Select("SELECT i FROM typed WHERE i=2147483648", []string{"i"}, []string{"INT4"}, nil))
	p.Step(t, fixture.Select("SELECT i FROM typed WHERE i=$1", []string{"i"}, []string{"INT4"}, nil, nil))
	p.Step(t, fixture.Prepared(fixture.Select("SELECT i FROM typed WHERE i=2147483648", []string{"i"}, []string{"INT4"}, nil)))
	p.Step(t, fixture.Prepared(fixture.Select("SELECT i FROM typed WHERE i=$1", []string{"i"}, []string{"INT4"}, nil, nil)))
}
func TestReferenceTypeCoreDiagnosticsAndAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE failures(i INT NOT NULL,b BIGINT,t TEXT,f BOOL)", 0))
	p.Step(t, fixture.Exec("INSERT INTO failures VALUES(99,99,'sentinel',TRUE)", 1))
	unchanged := fixture.Select("SELECT * FROM failures", []string{"i", "b", "t", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(99), int64(99), "sentinel", true}})
	for _, tc := range []struct{ column, text, name, code string }{{"i", "oops", "integer", "22P02"}, {"i", "_1", "integer", "22P02"}, {"i", "1_", "integer", "22P02"}, {"i", "1__0", "integer", "22P02"}, {"i", "0b2", "integer", "22P02"}, {"i", "\u00a01", "integer", "22P02"}, {"i", "2147483648", "integer", "22003"}, {"i", "-2147483649", "integer", "22003"}, {"b", "9223372036854775808", "bigint", "22003"}, {"b", "-9223372036854775809", "bigint", "22003"}, {"i", "2147483648x", "integer", "22P02"}, {"b", "9223372036854775808x", "bigint", "22P02"}, {"i", "0x80000000x", "integer", "22P02"}, {"i", "999999999999999999999999x", "integer", "22003"}, {"i", "0xFFFFFFFFFFFFFFFFFFFFFFFFx", "integer", "22003"}, {"f", "o", "boolean", "22P02"}, {"f", "truth", "boolean", "22P02"}} {
		message := fmt.Sprintf("invalid input syntax for type %s: \"%s\"", tc.name, tc.text)
		if tc.code == "22003" {
			message = fmt.Sprintf("value \"%s\" is out of range for type %s", tc.text, tc.name)
		}
		q := "/*é*/ INSERT INTO failures(" + tc.column + ") VALUES('" + tc.text + "')"
		position := strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "'")]) + 1)
		step := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: message, Position: position})
		step.ErrorStage = "prepare"
		p.Step(t, fixture.Prepared(step))
		step.ErrorStage = "execute"
		p.Step(t, step)
		bound := fixture.ImplicitError("INSERT INTO failures("+tc.column+") VALUES($1)", fixture.Diagnostic{Code: tc.code, Message: message}, tc.text)
		pg := *bound.Diagnostic
		pg.Context = "unnamed portal parameter $1 = '...'"
		bound.Diagnostic = &pg
		bound.ErrorStage = "execute"
		p.Step(t, bound)
		p.Step(t, fixture.Prepared(bound))
		p.Step(t, unchanged)
	}
	for _, tc := range []struct {
		value   int64
		message string
	}{{2147483648, `value "2147483648" is out of range for type integer`}, {-2147483649, `value "-2147483649" is out of range for type integer`}} {
		bound := fixture.ImplicitError("INSERT INTO failures(i) VALUES($1)", fixture.Diagnostic{Code: "22003", Message: tc.message}, tc.value)
		pg := *bound.Diagnostic
		pg.Context = "unnamed portal parameter $1 = '...'"
		bound.Diagnostic = &pg
		bound.ErrorStage = "execute"
		p.Step(t, bound)
		p.Step(t, unchanged)
		p.Step(t, fixture.Prepared(bound))
		p.Step(t, unchanged)
	}
	q := "SELECT i FROM failures WHERE i=TRUE"
	step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer = boolean", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: strconv.Itoa(strings.Index(q, "=") + 1)})
	step.SelectRows = true
	p.Step(t, step)
	step.ErrorStage = "prepare"
	p.Step(t, fixture.Prepared(step))
	q = "INSERT INTO failures(i) VALUES(TRUE)"
	step = fixture.ImplicitError(q, fixture.Diagnostic{Code: "42804", Message: `column "i" is of type integer but expression is of type boolean`, Hint: "You will need to rewrite or cast the expression.", Position: strconv.Itoa(strings.Index(q, "TRUE") + 1)})
	p.Step(t, step)
	step.ErrorStage = "prepare"
	p.Step(t, fixture.Prepared(step))
	for _, q := range []string{"INSERT INTO failures(i) VALUES(1),(2147483648)", "INSERT INTO failures(i) VALUES(1),(-2147483649)"} {
		step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22003", Message: "integer out of range"})
		step.ErrorStage = "execute"
		p.Step(t, step)
		p.Step(t, unchanged)
		p.Step(t, fixture.Prepared(step))
		p.Step(t, unchanged)
	}
	quoted := fixture.ImplicitError("INSERT INTO failures(i) VALUES(1),('oops')", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "36"})
	p.Step(t, quoted)
	quoted.ErrorStage = "prepare"
	p.Step(t, fixture.Prepared(quoted))
	p.Step(t, unchanged)
	q = "INSERT INTO failures(i) VALUES(1),(NULL)"
	step = fixture.ImplicitError(q, fixture.Diagnostic{Code: "23502", Message: `null value in column "i" of relation "failures" violates not-null constraint`, Detail: "Failing row contains (null, null, null, null).", Schema: "public", Table: "failures", Column: "i"})
	step.ErrorStage = "execute"
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
	p.Step(t, unchanged)
	for _, backend := range p.Backends() {
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		_, err := backend.DB.ExecContext(ctx, "INSERT INTO failures(i) VALUES(1),(2)")
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, unchanged)
	}
}

func TestReferenceTypeCoreQuotedWhereDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE failures(i INTEGER,b BIGINT,f BOOLEAN)", 0))
	// Literal expectations are from root's pinned PostgreSQL 18.0 UTF8/C
	// empty-table captures, not native outcomes or computed parser offsets.
	for _, tc := range []struct{ column, text, code, message string }{
		{"i", "oops", "22P02", `invalid input syntax for type integer: "oops"`},
		{"f", "truth", "22P02", `invalid input syntax for type boolean: "truth"`},
		{"i", "2147483648", "22003", `value "2147483648" is out of range for type integer`},
	} {
		q := "/*é*/ SELECT " + tc.column + " FROM failures WHERE " + tc.column + "='" + tc.text + "'"
		step := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: "38"})
		step.SelectRows = true
		step.ErrorStage = "execute"
		p.Step(t, step)
		step.ErrorStage = "prepare"
		p.Step(t, fixture.Prepared(step))

		bound := fixture.ImplicitError("/*é*/ SELECT "+tc.column+" FROM failures WHERE "+tc.column+"=$1", fixture.Diagnostic{Code: tc.code, Message: tc.message}, tc.text)
		bound.SelectRows = true
		bound.ErrorStage = "execute"
		pg := *bound.Diagnostic
		pg.Context = "unnamed portal parameter $1 = '...'"
		bound.Diagnostic = &pg
		p.Step(t, bound)
		p.Step(t, fixture.Prepared(bound))
	}
	p.Step(t, fixture.Select("SELECT * FROM failures", []string{"i", "b", "f"}, []string{"INT4", "INT8", "BOOL"}, nil))
}
func TestReferenceTypeCoreDumpRoundTrip(t *testing.T) {
	p := fixture.NewScenario(t)
	// Preserve the original bound setup before the separate reference Restore.
	for _, step := range []fixture.Step{
		fixture.Exec("CREATE TABLE typed(i INT NOT NULL,b BIGINT,t TEXT,f BOOL)", 0),
		fixture.Exec("CREATE TABLE empty(i INT)", 0),
		fixture.Exec("INSERT INTO typed VALUES($1,$2,$3,$4)", 1, -2147483648, int64(-9223372036854775808), "O'Brien\n\\tail", true),
		fixture.Exec("INSERT INTO typed VALUES(2147483647,9223372036854775807,'',FALSE),(0,NULL,NULL,NULL)", 2),
		fixture.Select("SELECT * FROM typed ORDER BY i", []string{"i", "b", "t", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(-2147483648), int64(-9223372036854775808), "O'Brien\n\\tail", true}, {int64(0), nil, nil, nil}, {int64(2147483647), int64(9223372036854775807), "", false}}),
		fixture.Select("SELECT * FROM empty", []string{"i"}, []string{"INT4"}, nil),
	} {
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, step)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	script := "CREATE TABLE typed(i INT NOT NULL,b BIGINT,t TEXT,f BOOL); CREATE TABLE empty(i INT); INSERT INTO typed VALUES(-2147483648,-9223372036854775808,'O''Brien\n\\tail',TRUE),(0,NULL,NULL,NULL),(2147483647,9223372036854775807,'',FALSE)"
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		cursor, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT * FROM typed ORDER BY i", nil)
		if err != nil {
			return err
		}
		defer cursor.Close()
		for range 2 {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				return err
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				return err
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				return err
			}
		}
		checkMetadata := func() {
			for i, name := range []string{"INT4", "INT8", "TEXT", "BOOL"} {
				nullable, known := cursor.(driver.RowsColumnTypeNullable).ColumnTypeNullable(i)
				if cursor.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(i) != name || !known || nullable != (i != 0) {
					t.Fatal("retained metadata changed", name, nullable, known)
				}
			}
		}
		checkMetadata()
		dst := make([]driver.Value, 4)
		for _, row := range [][]any{{int64(-2147483648), int64(-9223372036854775808), "O'Brien\n\\tail", true}, {int64(0), nil, nil, nil}, {int64(2147483647), int64(9223372036854775807), "", false}} {
			if err := cursor.Next(dst); err != nil {
				return err
			}
			for i, v := range row {
				if dst[i] != v {
					t.Fatal(dst, row)
				}
			}
		}
		if err := cursor.Next(dst); err != io.EOF {
			t.Fatal(err)
		}
		checkMetadata()
		if err := cursor.Close(); err != nil {
			return err
		}
		checkMetadata()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(redump.String(), err)
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
	}
	p.Step(t, fixture.Select("SELECT * FROM typed ORDER BY i", []string{"i", "b", "t", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(-2147483648), int64(-9223372036854775808), "O'Brien\n\\tail", true}, {int64(0), nil, nil, nil}, {int64(2147483647), int64(9223372036854775807), "", false}}))
	p.Step(t, fixture.Select("SELECT * FROM empty", []string{"i"}, []string{"INT4"}, nil))
	for _, script := range []string{"CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES(1),('oops')", "CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES(1),(2147483648)", "CREATE TABLE replacement(i INT NOT NULL); INSERT INTO replacement VALUES(1),(NULL)", "CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES($1)"} {
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
			t.Fatal("bad restore accepted")
		}
		p.Step(t, fixture.Select("SELECT * FROM typed ORDER BY i", []string{"i", "b", "t", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(-2147483648), int64(-9223372036854775808), "O'Brien\n\\tail", true}, {int64(0), nil, nil, nil}, {int64(2147483647), int64(9223372036854775807), "", false}}))
	}
}
