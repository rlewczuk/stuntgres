package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
)

func TestReferenceSmallintInputs(t *testing.T) {
	p := newReferenceScenario(t)
	for i, spelling := range []string{"SMALLINT", "int2", `"int2"`} {
		p.step(t, referencePrepared(referenceExec(fmt.Sprintf("CREATE TABLE alias%d(s %s)", i, spelling), 0)))
	}
	p.step(t, referenceExec("CREATE TABLE typed(s SMALLINT NOT NULL,i INTEGER,b BIGINT,n int2)", 0))
	p.step(t, referenceExec("INSERT INTO typed VALUES(-32768,-2147483648,-9223372036854775808,NULL),(32767,2147483647,9223372036854775807,NULL)", 2))
	p.step(t, referenceSelect("SELECT * FROM typed ORDER BY s", []string{"s", "i", "b", "n"}, []string{"INT2", "INT4", "INT8", "INT2"}, [][]any{{int64(-32768), int64(-2147483648), int64(-9223372036854775808), nil}, {int64(32767), int64(2147483647), int64(9223372036854775807), nil}}))
	p.step(t, referenceExec("CREATE TABLE inputs(s SMALLINT,n int2)", 0))
	for _, tc := range []struct {
		text string
		n    int64
	}{{" +0007 ", 7}, {"010", 10}, {"08", 8}, {"0x_FF", 255}, {"0o17", 15}, {"0b10_10", 10}, {"1_000", 1000}, {"-0X_10", -16}, {"-0", 0}, {"\t\n\r\f\v-8\t", -8}, {"-0x8000", -32768}, {"0x7fff", 32767}} {
		p.step(t, referencePrepared(referenceExec("INSERT INTO inputs VALUES($1,$2)", 1, []byte(tc.text), nil)))
		p.step(t, referenceSelect("SELECT s FROM inputs WHERE s=$1", []string{"s"}, []string{"INT2"}, [][]any{{tc.n}}, tc.n))
		p.step(t, referenceExec("DROP TABLE inputs", 0))
		p.step(t, referenceExec("CREATE TABLE inputs(s SMALLINT,n int2)", 0))
	}
	p.step(t, referenceExec("INSERT INTO inputs(s) VALUES(9)", 1))
	p.step(t, referenceSelect("SELECT * FROM inputs", []string{"s", "n"}, []string{"INT2", "INT2"}, [][]any{{int64(9), nil}}))
	for _, q := range []string{"SELECT s FROM typed WHERE s=32768", "SELECT s FROM typed WHERE s=2147483648"} {
		p.step(t, referenceSelect(q, []string{"s"}, []string{"INT2"}, nil))
		p.step(t, referencePrepared(referenceSelect(q, []string{"s"}, []string{"INT2"}, nil)))
	}
	p.step(t, referencePrepared(referenceSelect("SELECT s FROM typed WHERE s=$1", []string{"s"}, []string{"INT2"}, nil, nil)))
	p.step(t, referenceSelect("SELECT s FROM typed WHERE s=$1", []string{"s"}, []string{"INT2"}, nil, nil))
}

func TestReferenceSmallintDiagnosticsAndAtomicity(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE failures(i SMALLINT NOT NULL)", 0))
	for _, tc := range []struct{ text, code string }{{"oops", "22P02"}, {"", "22P02"}, {"+", "22P02"}, {"-", "22P02"}, {"_1", "22P02"}, {"1_", "22P02"}, {"1__0", "22P02"}, {"0x", "22P02"}, {"0x_", "22P02"}, {"0b2", "22P02"}, {"1.2", "22P02"}, {"1e2", "22P02"}, {"+ 1", "22P02"}, {"\u00a01", "22P02"}, {"32768", "22003"}, {"-32769", "22003"}, {"32768x", "22P02"}, {"327680x", "22003"}, {"0x8000x", "22P02"}, {"0x80000x", "22003"}} {
		message := fmt.Sprintf("invalid input syntax for type smallint: \"%s\"", tc.text)
		if tc.code == "22003" {
			message = fmt.Sprintf("value \"%s\" is out of range for type smallint", tc.text)
		}
		for _, q := range []string{"INSERT INTO failures(i) VALUES('" + tc.text + "')", "/*é*/ SELECT i FROM failures WHERE i='" + tc.text + "'"} {
			position := "32"
			selectRows := strings.HasPrefix(q, "/*")
			if selectRows {
				position = "38"
			}
			step := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: message, Position: position})
			step.selectRows = selectRows
			p.step(t, step)
			step.errorStage = "prepare"
			p.step(t, referencePrepared(step))
		}
		for _, raw := range []any{tc.text, []byte(tc.text)} {
			for _, q := range []string{"INSERT INTO failures(i) VALUES($1)", "/*é*/ SELECT i FROM failures WHERE i=$1"} {
				step := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: message}, raw)
				step.selectRows = strings.HasPrefix(q, "/*")
				step.errorStage = "execute"
				pg := *step.diagnostic
				pg.Context = "unnamed portal parameter $1 = '...'"
				if tc.text == "" {
					pg.Context = "unnamed portal parameter $1 = ''"
				}
				step.diagnostic = &pg
				p.step(t, step)
				p.step(t, referencePrepared(step))
			}
		}
	}
	p.step(t, referenceExec("INSERT INTO failures VALUES(99)", 1))
	unchanged := referenceSelect("SELECT * FROM failures", []string{"i"}, []string{"INT2"}, [][]any{{int64(99)}})
	for _, text := range []string{"32768", "-32769", "2147483648"} {
		step := referenceImplicitError("INSERT INTO failures VALUES(1),("+text+")", referenceDiagnostic{Code: "22003", Message: "smallint out of range"})
		step.errorStage = "execute"
		p.step(t, step)
		p.step(t, unchanged)
		p.step(t, referencePrepared(step))
		p.step(t, unchanged)
	}
	for _, n := range []int64{-32769, 32768} {
		step := referenceImplicitError("INSERT INTO failures VALUES(1),($1)", referenceDiagnostic{Code: "22003", Message: fmt.Sprintf("value \"%d\" is out of range for type smallint", n)}, n)
		step.errorStage = "execute"
		pg := *step.diagnostic
		pg.Context = "unnamed portal parameter $1 = '...'"
		step.diagnostic = &pg
		p.step(t, step)
		p.step(t, unchanged)
		p.step(t, referencePrepared(step))
		p.step(t, unchanged)
	}
	for _, tc := range []struct {
		q          string
		diagnostic referenceDiagnostic
		stage      string
		selectRows bool
	}{
		{"INSERT INTO failures VALUES(1),('oops')", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type smallint: "oops"`, Position: "33"}, "prepare", false},
		{"INSERT INTO failures VALUES(1),(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "i" of relation "failures" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "failures", Column: "i"}, "execute", false},
		{"INSERT INTO failures(i) VALUES(TRUE)", referenceDiagnostic{Code: "42804", Message: `column "i" is of type smallint but expression is of type boolean`, Hint: "You will need to rewrite or cast the expression.", Position: "32"}, "prepare", false},
		{"SELECT i FROM failures WHERE i=TRUE", referenceDiagnostic{Code: "42883", Message: "operator does not exist: smallint = boolean", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "31"}, "prepare", true},
		{"INSERT INTO failures VALUES()", referenceDiagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "29"}, "prepare", false},
	} {
		step := referenceImplicitError(tc.q, tc.diagnostic)
		step.selectRows = tc.selectRows
		p.step(t, step)
		if step.selectRows {
			exec := step
			exec.selectRows, exec.errorStage = false, "execute"
			p.step(t, exec)
		}
		step.errorStage = tc.stage
		p.step(t, referencePrepared(step))
		p.step(t, unchanged)
	}
	for _, name := range []string{"smallint", "INT2"} {
		step := referenceImplicitError(`CREATE TABLE bad(i "`+name+`")`, referenceDiagnostic{Code: "42704", Message: `type "` + name + `" does not exist`, Position: "20"})
		p.step(t, step)
		step.errorStage = "execute"
		p.step(t, referencePrepared(step))
	}
	for _, backend := range p.backends() {
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		_, err := backend.db.ExecContext(ctx, "INSERT INTO failures VALUES(1),(2)")
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, unchanged)
	}
}

func TestReferenceSmallintDumpRoundTrip(t *testing.T) {
	p := newReferenceScenario(t)
	// Preserve direct original setup separately from the reference Restore seed.
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE typed(s SMALLINT NOT NULL,i INTEGER,b BIGINT,n int2)", 0),
		referenceExec("CREATE TABLE empty(s SMALLINT)", 0),
		referenceExec("INSERT INTO typed VALUES(-32768,-2147483648,-9223372036854775808,NULL),(32767,2147483647,9223372036854775807,NULL),(0,NULL,NULL,NULL)", 3),
		referenceSelect("SELECT * FROM typed ORDER BY s", []string{"s", "i", "b", "n"}, []string{"INT2", "INT4", "INT8", "INT2"}, [][]any{{int64(-32768), int64(-2147483648), int64(-9223372036854775808), nil}, {int64(0), nil, nil, nil}, {int64(32767), int64(2147483647), int64(9223372036854775807), nil}}),
		referenceSelect("SELECT * FROM empty", []string{"s"}, []string{"INT2"}, nil),
	} {
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, step)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	script := "CREATE TABLE typed(s SMALLINT NOT NULL,i INTEGER,b BIGINT,n int2); CREATE TABLE empty(s SMALLINT); INSERT INTO typed VALUES(-32768,-2147483648,-9223372036854775808,NULL),(0,NULL,NULL,NULL),(32767,2147483647,9223372036854775807,NULL)"
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	conn, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		cursor, err := raw.(driver.QueryerContext).QueryContext(p.ctx, "SELECT * FROM typed ORDER BY s", nil)
		if err != nil {
			return err
		}
		defer cursor.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			return err
		}
		for _, name := range []string{"SMALLINT", "INTEGER", "BIGINT"} {
			if !strings.Contains(dump.String(), name) {
				t.Fatal(dump.String())
			}
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		for i, name := range []string{"INT2", "INT4", "INT8", "INT2"} {
			nullable, known := cursor.(driver.RowsColumnTypeNullable).ColumnTypeNullable(i)
			if cursor.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(i) != name || cursor.(driver.RowsColumnTypeScanType).ColumnTypeScanType(i) != reflect.TypeFor[int64]() || !known || nullable != (i != 0) {
				t.Fatal("retained width changed", name, nullable, known)
			}
		}
		dst := make([]driver.Value, 4)
		for _, row := range [][]any{{int64(-32768), int64(-2147483648), int64(-9223372036854775808), nil}, {int64(0), nil, nil, nil}, {int64(32767), int64(2147483647), int64(9223372036854775807), nil}} {
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
		return cursor.Close()
	})
	if err != nil {
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
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(redump.String(), err)
	}
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
	}
	p.step(t, referenceSelect("SELECT * FROM typed ORDER BY s", []string{"s", "i", "b", "n"}, []string{"INT2", "INT4", "INT8", "INT2"}, [][]any{{int64(-32768), int64(-2147483648), int64(-9223372036854775808), nil}, {int64(0), nil, nil, nil}, {int64(32767), int64(2147483647), int64(9223372036854775807), nil}}))
	p.step(t, referenceSelect("SELECT * FROM empty", []string{"s"}, []string{"INT2"}, nil))
	for _, script := range []string{"CREATE TABLE replacement(s SMALLINT); INSERT INTO replacement VALUES(1),(32768)", "CREATE TABLE replacement(s SMALLINT); INSERT INTO replacement VALUES(1),('oops')"} {
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err == nil {
			t.Fatal("bad restore accepted")
		}
		p.step(t, referenceSelect("SELECT * FROM typed ORDER BY s", []string{"s", "i", "b", "n"}, []string{"INT2", "INT4", "INT8", "INT2"}, [][]any{{int64(-32768), int64(-2147483648), int64(-9223372036854775808), nil}, {int64(0), nil, nil, nil}, {int64(32767), int64(2147483647), int64(9223372036854775807), nil}}))
	}
	ctx, cancel := context.WithCancel(p.ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	p.step(t, referenceSelect("SELECT * FROM typed ORDER BY s", []string{"s", "i", "b", "n"}, []string{"INT2", "INT4", "INT8", "INT2"}, [][]any{{int64(-32768), int64(-2147483648), int64(-9223372036854775808), nil}, {int64(0), nil, nil, nil}, {int64(32767), int64(2147483647), int64(9223372036854775807), nil}}))
	for _, backend := range p.backends() {
		for _, q := range []string{"SELECT * FROM typed", "SELECT * FROM empty"} {
			rows, err := backend.db.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), q)
			if err != nil {
				t.Fatal(err)
			}
			types, err := rows.ColumnTypes()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			for i, c := range types {
				want := reflect.TypeFor[int64]()
				if backend.pg {
					want = []reflect.Type{reflect.TypeFor[int16](), reflect.TypeFor[int32](), reflect.TypeFor[int64](), reflect.TypeFor[int16]()}[i]
				}
				if c.DatabaseTypeName() != []string{"INT2", "INT4", "INT8", "INT2"}[i] || c.ScanType() != want {
					t.Fatal(c)
				}
			}
		}
	}
}

func TestReferenceSmallintAuthorization(t *testing.T) {
	const reader = "smallint_reader"
	roleReferenceCleanup(t, []string{reader})
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE ROLE "+reader, 0))
	p.step(t, referenceExec("CREATE TABLE public.secret(i SMALLINT)", 0))
	p.step(t, referenceExec("INSERT INTO public.secret VALUES(7)", 1))
	// Match the existing role fixture's native PUBLIC namespace baseline.
	// Table INSERT/SELECT remain ungranted, so these are table-ACL witnesses.
	if p.postgres != nil {
		if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT USAGE, CREATE ON SCHEMA public TO PUBLIC"); err != nil {
			t.Fatal(err)
		}
	}
	for _, backend := range p.backends() {
		conn, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, backend.recorder, s) }
			run(referenceExec("SET SESSION AUTHORIZATION "+reader, 0))
			defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{"INSERT INTO public.secret VALUES(9)", "INSERT INTO public.secret VALUES($1)"} {
					s := roleReferenceError(q, "42501", "permission denied for table secret", "", prepared, "execute")
					if strings.Contains(q, "$1") {
						s.args = []any{int64(9)}
					}
					run(s)
				}
				deniedSelect := roleReferenceError("SELECT i FROM public.secret", "42501", "permission denied for table secret", "", prepared, "execute")
				deniedSelect.selectRows = true
				run(deniedSelect)
				s := roleReferenceError("INSERT INTO public.secret VALUES($1)", "22P02", `invalid input syntax for type smallint: "oops"`, "", prepared, "execute")
				s.args = []any{"oops"}
				pg := *s.diagnostic
				pg.Context = "unnamed portal parameter $1 = '...'"
				s.diagnostic = &pg
				run(s)
				run(roleReferenceError("INSERT INTO public.secret VALUES(32768)", "22003", "smallint out of range", "", prepared, "execute"))
			}
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			backend.recorder.mu.Lock()
			recordStart := len(backend.recorder.operations)
			backend.recorder.mu.Unlock()
			var n int64
			if err := conn.QueryRowContext(ctx, "SELECT i FROM public.secret").Scan(&n); err != nil || n != 7 {
				t.Fatal("single-row authorization recovery", n, err)
			}
			backend.recorder.mu.Lock()
			var captured *referenceOperation
			for _, op := range backend.recorder.operations[recordStart:] {
				if op.SQL == "SELECT i FROM public.secret" && op.Columns != nil {
					captured = op
				}
			}
			backend.recorder.mu.Unlock()
			if captured == nil || !reflect.DeepEqual(captured.Columns, []string{"i"}) || len(captured.Rows) != 1 || len(captured.Rows[0]) != 1 || captured.Rows[0][0] != int64(7) || captured.Err != nil || !captured.Closed || captured.CloseErr != nil {
				t.Fatal("single-row authorization recovery recording", captured)
			}
			run(referenceSelect("SELECT i FROM public.secret", []string{"i"}, []string{"INT2"}, [][]any{{int64(7)}}))
		}()
	}
}

func TestReferenceSmallintRemainingIntegerDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE wide(i INTEGER,b BIGINT)", 0))
	for _, tc := range []struct{ column, name, text, code string }{
		{"i", "integer", "oops", "22P02"}, {"b", "bigint", "oops", "22P02"},
		{"i", "integer", "2147483648", "22003"}, {"i", "integer", "-2147483649", "22003"},
		{"b", "bigint", "9223372036854775808", "22003"}, {"b", "bigint", "-9223372036854775809", "22003"},
	} {
		message := fmt.Sprintf("invalid input syntax for type %s: \"%s\"", tc.name, tc.text)
		if tc.code == "22003" {
			message = fmt.Sprintf("value \"%s\" is out of range for type %s", tc.text, tc.name)
		}
		for _, backend := range p.backends() {
			func() {
				q := "INSERT INTO wide(" + tc.column + ") VALUES($1)"
				stmt, err := backend.db.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
				for _, raw := range []any{tc.text, []byte(tc.text)} {
					s := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: message, Context: "unnamed portal parameter $1 = '...'"}, raw)
					s.errorStage = "execute"
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
					referenceRunStep(t, p.ctx, held, backend.recorder, s)
				}
			}()
		}
	}
	p.step(t, referenceSelect("SELECT * FROM wide", []string{"i", "b"}, []string{"INT4", "INT8"}, nil))
}
