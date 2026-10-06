//go:build reference

package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceNumericControlsAndBounds(t *testing.T) {
	p := newReferencePair(t)
	exact := "12345678901234567890.1234567890123456789000"
	script := "CREATE TABLE n(id INT,v DECIMAL,a NUMERIC(2,-3),b NUMERIC(3,5)); INSERT INTO n VALUES(1,'" + exact + "',1500,.009994),(2,'NaN',NULL,NULL),(3,'Infinity',NULL,NULL),(4,'-Infinity',NULL,NULL),(5,NULL,NULL,NULL)"
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "NUMERIC(2,-3)") {
		t.Fatal(dump.String())
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
	if _, err = p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal(err)
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
	// Independently check the format bounds without logging huge successful rows.
	for _, db := range []referenceExecutor{p.memory, p.postgres} {
		for _, tc := range []struct {
			in     string
			length int
			last   byte
		}{{"1e131071", 131072, '0'}, {"1e-16383", 16385, '1'}} {
			rows, err := db.QueryContext(p.ctx, "SELECT "+tc.in+" FROM n WHERE id=1")
			if err != nil {
				t.Fatal(err)
			}
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			var text string
			if err = rows.Scan(&text); err != nil {
				t.Fatal(err)
			}
			if len(text) != tc.length || text[len(text)-1] != tc.last {
				t.Fatal(len(text))
			}
			rows.Close()
		}
	}
}
