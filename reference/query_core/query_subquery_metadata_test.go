package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceQuerySubqueryMetadata(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE typed(v INT NOT NULL,label VARCHAR(5),stamp TIMESTAMP(3))", 0))
		run(fixture.Exec("INSERT INTO typed VALUES(7,'abc','infinity')", 1))
		for _, empty := range []bool{false, true} {
			q := "SELECT (SELECT v FROM typed WHERE FALSE) AS scalar,d.label,d.stamp FROM (SELECT label,stamp FROM typed) d"
			want := [][]any{{nil, "abc", "infinity"}}
			if empty {
				q += " WHERE FALSE"
				want = nil
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(q, []string{"scalar", "label", "stamp"}, []string{"INT4", "VARCHAR", "TIMESTAMP"}, want)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					meta, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					// pq flags are root-observed; native derives physical source
					// flags but must not transfer child NOT NULL proof to a scalar.
					scans := []reflect.Type{reflect.TypeFor[int64](), reflect.TypeFor[string](), reflect.TypeFor[time.Time]()}
					if b.PG {
						scans[0] = reflect.TypeFor[int32]()
					}
					for i, m := range meta {
						if m.ScanType() != scans[i] {
							t.Fatalf("%s ScanType %v want %v", m.Name(), m.ScanType(), scans[i])
						}
						wantLength, wantLengthKnown := int64(0), false
						if i == 1 {
							wantLength, wantLengthKnown = 5, true
						}
						if length, known := m.Length(); length != wantLength || known != wantLengthKnown {
							t.Fatalf("%s Length %d/%v want %d/%v", m.Name(), length, known, wantLength, wantLengthKnown)
						}
						wantNullable, wantNullableKnown := !b.PG && i != 0, !b.PG && i != 0
						if nullable, known := m.Nullable(); nullable != wantNullable || known != wantNullableKnown {
							t.Fatalf("%s Nullable %v/%v want %v/%v", m.Name(), nullable, known, wantNullable, wantNullableKnown)
						}
						if precision, scale, known := m.DecimalSize(); precision != 0 || scale != 0 || known {
							t.Fatalf("%s DecimalSize %d,%d/%v", m.Name(), precision, scale, known)
						}
					}
				}
				run(s)
			}
		}
		// The main SQL body and mutable export checks run on both backends.
		q := "SELECT (SELECT stamp FROM typed) AS scalar,d.stamp FROM (SELECT stamp FROM typed) d"
		ctx, cancel := context.WithCancel(p.Ctx)
		defer cancel()
		rows, err := c.QueryContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(columns, []string{"scalar", "stamp"}) {
			t.Fatal(columns)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var scalar, derived any
		if err := rows.Scan(&scalar, &derived); err != nil {
			t.Fatal(err)
		}
		a, ok := scalar.([]byte)
		if !ok || string(a) != "infinity" {
			t.Fatalf("scalar raw %T %v", scalar, scalar)
		}
		z, ok := derived.([]byte)
		if !ok || string(z) != "infinity" {
			t.Fatalf("derived raw %T %v", derived, derived)
		}
		a[0] = 'X'
		if string(z) != "infinity" {
			t.Fatal("exports share bytes")
		}
		if b.PG {
			if rows.Next() || rows.Err() != nil {
				t.Fatal(rows.Err())
			}
			rows.Close()
			return
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
		cancel()
		if rows.Next() || !errors.Is(rows.Err(), context.Canceled) {
			t.Fatal("postpublication cancellation", rows.Err())
		}
		if rows.Next() || !errors.Is(rows.Err(), context.Canceled) {
			t.Fatal("sticky cancellation", rows.Err())
		}
		if string(a) != "Xnfinity" || string(z) != "infinity" || !reflect.DeepEqual(columns, []string{"scalar", "stamp"}) {
			t.Fatal("retained exports changed", a, z, columns)
		}
		rows.Close()
		subqueryBoth(run, fixture.Select(q, []string{"scalar", "stamp"}, []string{"TIMESTAMP", "TIMESTAMP"}, [][]any{{"infinity", "infinity"}}))
	})
}

func TestReferenceQuerySubqueryRetainedClose(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE stamps(id INT,v TIMESTAMP)", 0))
		run(fixture.Exec("INSERT INTO stamps VALUES(1,'infinity'),(2,'-infinity')", 2))
		q := "SELECT (SELECT v FROM stamps WHERE id=d.id) AS scalar,d.v FROM (SELECT id,v FROM stamps) d ORDER BY d.id"
		subqueryBoth(run, fixture.Select(q, []string{"scalar", "v"}, []string{"TIMESTAMP", "TIMESTAMP"}, [][]any{{"infinity", "infinity"}, {"-infinity", "-infinity"}}))
		if b.PG {
			return
		}
		raw, err := p.NativeConnector.Connect(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		held, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, q, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		names := held.Columns()
		metadata := held.(driver.RowsColumnTypeDatabaseTypeName)
		if err := raw.Close(); err != nil {
			t.Fatal(err)
		}
		dst := make([]driver.Value, 2)
		var saved []byte
		for _, want := range []string{"infinity", "-infinity"} {
			if err := held.Next(dst); err != nil {
				t.Fatal(err)
			}
			for _, value := range dst {
				cell, ok := value.([]byte)
				if !ok || string(cell) != want {
					t.Fatal(value, want)
				}
			}
			if want == "infinity" {
				saved = dst[0].([]byte)
				saved[0] = 'X'
			}
		}
		if string(saved) != "Xnfinity" {
			t.Fatal("next reused exported bytes", saved)
		}
		if !reflect.DeepEqual(names, []string{"scalar", "v"}) || metadata.ColumnTypeDatabaseTypeName(0) != "TIMESTAMP" || metadata.ColumnTypeDatabaseTypeName(1) != "TIMESTAMP" {
			t.Fatal("closed owner changed metadata")
		}
		if err := held.Next(dst); err != io.EOF {
			t.Fatal(err)
		}
		if err := held.Close(); err != nil {
			t.Fatal(err)
		}
		if err := held.Close(); err != nil {
			t.Fatal(err)
		}
	})
}
