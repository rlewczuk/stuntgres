package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// The original direct bound seed is distinct from the reference Restore input.
func numericReferenceOriginalControlSeed(t *testing.T, p *fixture.Pair, exact string) {
	t.Helper()
	for _, step := range []fixture.Step{
		fixture.Exec("CREATE TABLE n(id INT,v NUMERIC,a NUMERIC(2,-3),b NUMERIC(3,5))", 0),
		fixture.Exec("INSERT INTO n VALUES(1,$1,1500,.009994),(2,'NaN',NULL,NULL),(3,'Infinity',NULL,NULL),(4,'-Infinity',NULL,NULL),(5,NULL,NULL,NULL)", 5, []byte(exact)),
		fixture.Select("SELECT * FROM n ORDER BY id", []string{"id", "v", "a", "b"}, []string{"INT4", "NUMERIC", "NUMERIC", "NUMERIC"}, [][]any{{int64(1), exact, "2000", "0.00999"}, {int64(2), "NaN", nil, nil}, {int64(3), "Infinity", nil, nil}, {int64(4), "-Infinity", nil, nil}, {int64(5), nil, nil, nil}}),
	} {
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, step)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceNumericControlsAndBounds(t *testing.T) {
	p := fixture.NewScenario(t)
	exact := "12345678901234567890.1234567890123456789000"
	numericReferenceOriginalControlSeed(t, p, exact)
	script := "CREATE TABLE n(id INT,v DECIMAL,a NUMERIC(2,-3),b NUMERIC(3,5)); INSERT INTO n VALUES(1,'" + exact + "',1500,.009994),(2,'NaN',NULL,NULL),(3,'Infinity',NULL,NULL),(4,'-Infinity',NULL,NULL),(5,NULL,NULL,NULL)"
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		query := raw.(driver.QueryerContext)
		owned := []byte(exact)
		cursor, err := query.QueryContext(p.Ctx, "SELECT id,v,a,b FROM n WHERE v=$1", []driver.NamedValue{{Ordinal: 1, Value: owned}})
		if err != nil {
			return err
		}
		defer cursor.Close()
		owned[0] = 'X'
		var snapshot bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &snapshot); err != nil {
			return err
		}
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec("INSERT INTO n VALUES(6,0,NULL,NULL)", 1))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(snapshot.Bytes())); err != nil {
			return err
		}
		precision, scale, known := cursor.(driver.RowsColumnTypePrecisionScale).ColumnTypePrecisionScale(2)
		if cursor.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(1) != "NUMERIC" || precision != 2 || scale != -3 || !known {
			t.Fatal(precision, scale, known)
		}
		dst := make([]driver.Value, 4)
		if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, []driver.Value{int64(1), exact, "2000", "0.00999"}) {
			t.Fatal(dst, err)
		}
		if err := cursor.Next(dst); err != io.EOF {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		defer cancel()
		canceled, err := query.QueryContext(ctx, "SELECT v FROM n", nil)
		if err != nil {
			return err
		}
		defer canceled.Close()
		cancel()
		if err := canceled.Next(make([]driver.Value, 1)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	held, err := p.Native.QueryContext(p.Ctx, "SELECT * FROM n ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	metadata, err := held.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	precision, scale, ok := metadata[2].DecimalSize()
	if !ok || precision != 2 || scale != -3 {
		t.Fatal(precision, scale, ok)
	}
	if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if p.Postgres != nil {
		if _, err = p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
	}
	want := [][]any{{int64(1), exact, "2000", "0.00999"}, {int64(2), "NaN", nil, nil}, {int64(3), "Infinity", nil, nil}, {int64(4), "-Infinity", nil, nil}, {int64(5), nil, nil, nil}}
	step := fixture.Select("SELECT * FROM n ORDER BY id", []string{"id", "v", "a", "b"}, []string{"INT4", "NUMERIC", "NUMERIC", "NUMERIC"}, want)
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
	var redump bytes.Buffer
	if err = stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(redump.String(), err)
	}
	for i := range want {
		if !held.Next() {
			t.Fatal(i, held.Err())
		}
		var id int64
		var v, a, b any
		if err = held.Scan(&id, &v, &a, &b); err != nil {
			t.Fatal(err)
		}
		if id != want[i][0] || fixture.Normalize(v) != want[i][1] || fixture.Normalize(a) != want[i][2] || fixture.Normalize(b) != want[i][3] {
			t.Fatal(id, v, a, b)
		}
	}
	if held.Next() || held.Err() != nil {
		t.Fatal(held.Err())
	}
	held.Close()
	for _, script := range []string{"CREATE TABLE staged(v NUMERIC(3,1)); INSERT INTO staged VALUES(1.2),(99.95)", "CREATE TABLE staged(v NUMERIC); INSERT INTO staged VALUES(1),('oops')", "CREATE TABLE staged(v NUMERIC); INSERT INTO staged VALUES(1); SELECT 1.2__3 FROM staged"} {
		if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
			t.Fatal("invalid numeric restore succeeded")
		}
		p.Step(t, step)
	}
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err = stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	p.Step(t, step)
}

func TestReferenceNumericFormatBounds(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE n(id INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO n VALUES(1)", 1))
	for _, backend := range p.Backends() {
		for _, tc := range []struct {
			in     string
			length int
			last   byte
		}{{"1e131071", 131072, '0'}, {"1e-16383", 16385, '1'}} {
			q := "SELECT " + tc.in + " FROM n WHERE id=1"
			rows, err := backend.DB.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder), q)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			var raw any
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			fixture.AssertRawValue(t, backend.PG, "NUMERIC", raw)
			text := fixture.Normalize(raw).(string)
			if len(text) != tc.length || text[len(text)-1] != tc.last {
				t.Fatal(len(text))
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("extra rows", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			backend.Recorder.Mu.Lock()
			var recorded *fixture.Operation
			for _, op := range backend.Recorder.Operations {
				if op.SQL == q && op.Columns != nil {
					recorded = op
				}
			}
			backend.Recorder.Mu.Unlock()
			if recorded == nil || len(recorded.Rows) != 1 || !recorded.Closed || recorded.CloseErr != nil {
				t.Fatal("missing consumed bounds observation")
			}
			fixture.AssertRawValue(t, backend.PG, "NUMERIC", recorded.Rows[0][0])
			if fixture.Normalize(recorded.Rows[0][0]) != text {
				t.Fatal("recorded bounds differ")
			}
		}
	}
}
