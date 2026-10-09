package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"math"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceFloatControlSnapshots(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE f(id INTEGER,r REAL,d DOUBLE PRECISION)", 0))
	p.step(t, referenceExec("INSERT INTO f VALUES(1,'33554448','-0'),(2,'1e-45','5e-324'),(3,'NaN','Infinity'),(4,'-Infinity','NaN'),(5,'7.038531e-26','0.1')", 5))
	p.step(t, referenceSelect("SELECT id FROM f ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}))
	conn, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		query := raw.(driver.QueryerContext)
		cursor, err := query.QueryContext(p.ctx, "SELECT * FROM f ORDER BY id", nil)
		if err != nil {
			return err
		}
		defer cursor.Close()
		p.step(t, referenceExec("INSERT INTO f VALUES(6,NULL,NULL)", 1))
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			return err
		}
		for _, fragment := range []string{"REAL", "DOUBLE PRECISION", "'3.3554448e+07'", "'-0'", "'1e-45'", "'5e-324'", "'NaN'"} {
			if !strings.Contains(dump.String(), fragment) {
				t.Fatal(fragment, dump.String())
			}
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		var again bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || again.String() != dump.String() {
			t.Fatal(again.String(), err)
		}
		for _, script := range []string{"CREATE TABLE replacement(r REAL); INSERT INTO replacement VALUES('1'),('oops')", "CREATE TABLE replacement(r DOUBLE PRECISION); INSERT INTO replacement VALUES('1'),('1e999')"} {
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err == nil {
				t.Fatal("bad restore accepted")
			}
			again.Reset()
			if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || again.String() != dump.String() {
				t.Fatal("restore changed state", err)
			}
		}
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE replacement(t TEXT); INSERT INTO replacement VALUES('new')")); err != nil {
			return err
		}
		metadata := cursor.(driver.RowsColumnTypeDatabaseTypeName)
		if metadata.ColumnTypeDatabaseTypeName(1) != "FLOAT4" || metadata.ColumnTypeDatabaseTypeName(2) != "FLOAT8" {
			t.Fatal("old metadata changed")
		}
		dst := make([]driver.Value, 3)
		for i := 1; i <= 5; i++ {
			if err := cursor.Next(dst); err != nil || dst[0] != int64(i) {
				t.Fatal(dst, err)
			}
			if i == 1 && (dst[1] != float64(33554448) || !math.Signbit(dst[2].(float64))) {
				t.Fatal(dst)
			}
			if i == 2 && (math.Float32bits(float32(dst[1].(float64))) != 1 || math.Float64bits(dst[2].(float64)) != 1) {
				t.Fatal(dst)
			}
			if i == 3 && (!math.IsNaN(dst[1].(float64)) || !math.IsInf(dst[2].(float64), 1)) {
				t.Fatal(dst)
			}
			if i == 4 && (!math.IsInf(dst[1].(float64), -1) || !math.IsNaN(dst[2].(float64))) {
				t.Fatal(dst)
			}
			if i == 5 && (math.Float32bits(float32(dst[1].(float64))) != 0x15ae43fd || math.Float64bits(dst[2].(float64)) != math.Float64bits(.1)) {
				t.Fatal(dst)
			}
		}
		if err := cursor.Next(dst); err != io.EOF {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(ctx, p.native); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		iteration, stop := context.WithCancel(p.ctx)
		defer stop()
		canceled, err := query.QueryContext(iteration, "SELECT * FROM replacement", nil)
		if err != nil {
			return err
		}
		defer canceled.Close()
		stop()
		if err := canceled.Next(make([]driver.Value, 1)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT * FROM replacement", []string{"t"}, []string{"TEXT"}, [][]any{{"new"}}))
	if p.postgres != nil {
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT id FROM f ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}, {int64(6)}}))
	}
}
