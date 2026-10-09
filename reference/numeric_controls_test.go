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
	"testing"
)

// The original direct bound seed is distinct from the reference Restore input.
func numericReferenceOriginalControlSeed(t *testing.T, p *referencePair, exact string) {
	t.Helper()
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE n(id INT,v NUMERIC,a NUMERIC(2,-3),b NUMERIC(3,5))", 0),
		referenceExec("INSERT INTO n VALUES(1,$1,1500,.009994),(2,'NaN',NULL,NULL),(3,'Infinity',NULL,NULL),(4,'-Infinity',NULL,NULL),(5,NULL,NULL,NULL)", 5, []byte(exact)),
		referenceSelect("SELECT * FROM n ORDER BY id", []string{"id", "v", "a", "b"}, []string{"INT4", "NUMERIC", "NUMERIC", "NUMERIC"}, [][]any{{int64(1), exact, "2000", "0.00999"}, {int64(2), "NaN", nil, nil}, {int64(3), "Infinity", nil, nil}, {int64(4), "-Infinity", nil, nil}, {int64(5), nil, nil, nil}}),
	} {
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, step)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceNumericControlsAndBounds(t *testing.T) {
	p := newReferenceScenario(t)
	exact := "12345678901234567890.1234567890123456789000"
	numericReferenceOriginalControlSeed(t, p, exact)
	script := "CREATE TABLE n(id INT,v DECIMAL,a NUMERIC(2,-3),b NUMERIC(3,5)); INSERT INTO n VALUES(1,'" + exact + "',1500,.009994),(2,'NaN',NULL,NULL),(3,'Infinity',NULL,NULL),(4,'-Infinity',NULL,NULL),(5,NULL,NULL,NULL)"
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	conn, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		query := raw.(driver.QueryerContext)
		owned := []byte(exact)
		cursor, err := query.QueryContext(p.ctx, "SELECT id,v,a,b FROM n WHERE v=$1", []driver.NamedValue{{Ordinal: 1, Value: owned}})
		if err != nil {
			return err
		}
		defer cursor.Close()
		owned[0] = 'X'
		var snapshot bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &snapshot); err != nil {
			return err
		}
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("INSERT INTO n VALUES(6,0,NULL,NULL)", 1))
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(snapshot.Bytes())); err != nil {
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
		ctx, cancel := context.WithCancel(p.ctx)
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
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	held, err := p.native.QueryContext(p.ctx, "SELECT * FROM n ORDER BY id")
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
	if err = stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if p.postgres != nil {
		if _, err = p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
	}
	want := [][]any{{int64(1), exact, "2000", "0.00999"}, {int64(2), "NaN", nil, nil}, {int64(3), "Infinity", nil, nil}, {int64(4), "-Infinity", nil, nil}, {int64(5), nil, nil, nil}}
	step := referenceSelect("SELECT * FROM n ORDER BY id", []string{"id", "v", "a", "b"}, []string{"INT4", "NUMERIC", "NUMERIC", "NUMERIC"}, want)
	p.step(t, step)
	p.step(t, referencePrepared(step))
	var redump bytes.Buffer
	if err = stuntgres.Dump(p.ctx, p.native, &redump); err != nil || redump.String() != dump.String() {
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
		if id != want[i][0] || referenceNormalize(v) != want[i][1] || referenceNormalize(a) != want[i][2] || referenceNormalize(b) != want[i][3] {
			t.Fatal(id, v, a, b)
		}
	}
	if held.Next() || held.Err() != nil {
		t.Fatal(held.Err())
	}
	held.Close()
	for _, script := range []string{"CREATE TABLE staged(v NUMERIC(3,1)); INSERT INTO staged VALUES(1.2),(99.95)", "CREATE TABLE staged(v NUMERIC); INSERT INTO staged VALUES(1),('oops')", "CREATE TABLE staged(v NUMERIC); INSERT INTO staged VALUES(1); SELECT 1.2__3 FROM staged"} {
		if err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err == nil {
			t.Fatal("invalid numeric restore succeeded")
		}
		p.step(t, step)
	}
	canceled, cancel := context.WithCancel(p.ctx)
	cancel()
	if err = stuntgres.Restore(canceled, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	p.step(t, step)
}

func TestReferenceNumericFormatBounds(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE n(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO n VALUES(1)", 1))
	for _, backend := range p.backends() {
		for _, tc := range []struct {
			in     string
			length int
			last   byte
		}{{"1e131071", 131072, '0'}, {"1e-16383", 16385, '1'}} {
			q := "SELECT " + tc.in + " FROM n WHERE id=1"
			rows, err := backend.db.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), q)
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
			referenceAssertRawValue(t, backend.pg, "NUMERIC", raw)
			text := referenceNormalize(raw).(string)
			if len(text) != tc.length || text[len(text)-1] != tc.last {
				t.Fatal(len(text))
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("extra rows", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			backend.recorder.mu.Lock()
			var recorded *referenceOperation
			for _, op := range backend.recorder.operations {
				if op.SQL == q && op.Columns != nil {
					recorded = op
				}
			}
			backend.recorder.mu.Unlock()
			if recorded == nil || len(recorded.Rows) != 1 || !recorded.Closed || recorded.CloseErr != nil {
				t.Fatal("missing consumed bounds observation")
			}
			referenceAssertRawValue(t, backend.pg, "NUMERIC", recorded.Rows[0][0])
			if referenceNormalize(recorded.Rows[0][0]) != text {
				t.Fatal("recorded bounds differ")
			}
		}
	}
}
