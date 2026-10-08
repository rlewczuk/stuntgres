package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"stuntgres"
)

func TestReferenceQueryCoreCapturedOwnership(t *testing.T) {
	for _, replacement := range []string{"reset", "restore"} {
		t.Run(replacement, func(t *testing.T) {
			p := newReferenceScenario(t)
			p.step(t, referenceExec("CREATE TABLE t(id INT NOT NULL)", 0))
			p.step(t, referenceExec("INSERT INTO t VALUES(1),(2)", 2))
			step := referenceSelect("SELECT id,$1 FROM t ORDER BY id", []string{"id", "?column?"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "before"}, {int64(2), "before"}}, []byte("before"))
			p.step(t, step)
			p.step(t, referencePrepared(step))
			conn, err := p.native.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			err = conn.Raw(func(raw any) error {
				query := raw.(driver.QueryerContext)
				direct, err := query.QueryContext(p.ctx, "SELECT id FROM t ORDER BY id", nil)
				if err != nil {
					return err
				}
				defer direct.Close()
				owned := []byte("before")
				computed, err := query.QueryContext(p.ctx, step.query, []driver.NamedValue{{Ordinal: 1, Value: owned}})
				if err != nil {
					return err
				}
				defer computed.Close()
				owned[0] = 'X'
				referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("INSERT INTO t VALUES(3)", 1))
				if replacement == "reset" {
					if err := stuntgres.Reset(p.ctx, p.native); err != nil {
						return err
					}
				} else if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE t(replacement TEXT); INSERT INTO t VALUES('99')")); err != nil {
					return err
				}
				for _, cursor := range []driver.Rows{direct, computed} {
					names := cursor.Columns()
					names[0] = "changed"
					if cursor.Columns()[0] != "id" || cursor.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "INT4" {
						t.Fatal("metadata replaced or borrowed")
					}
					dst := make([]driver.Value, len(cursor.Columns()))
					for i := range 2 {
						if err := cursor.Next(dst); err != nil || dst[0] != int64(i+1) {
							t.Fatal(dst, err)
						}
						if len(dst) == 2 && dst[1] != "before" {
							t.Fatal(dst)
						}
					}
					if err := cursor.Next(dst); err != io.EOF {
						t.Fatal(err)
					}
					if cursor.Columns()[0] != "id" {
						t.Fatal("EOF erased schema")
					}
				}
				if replacement == "restore" {
					referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT replacement FROM t", []string{"replacement"}, []string{"TEXT"}, [][]any{{"99"}}))
				} else {
					failure := referenceImplicitError("SELECT id FROM t", referenceDiagnostic{Code: "42P01", Message: `relation "t" does not exist`, Position: "16"})
					failure.selectRows = true
					referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, failure)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReferenceQueryCoreRawCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,v TIMESTAMP)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,'infinity'),(2,'infinity')", 2))
	step := referenceSelect("SELECT id,v,v FROM t ORDER BY id", []string{"id", "v", "v"}, []string{"INT4", "TIMESTAMP", "TIMESTAMP"}, [][]any{{int64(1), "infinity", "infinity"}, {int64(2), "infinity", "infinity"}})
	p.step(t, step)
	p.step(t, referencePrepared(step))
	conn, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		query := raw.(driver.QueryerContext)
		ctx, cancel := context.WithCancel(p.ctx)
		defer cancel()
		cursor, err := query.QueryContext(ctx, step.query, nil)
		if err != nil {
			return err
		}
		defer cursor.Close()
		dst := make([]driver.Value, 3)
		if err := cursor.Next(dst); err != nil {
			return err
		}
		saved := append([]driver.Value(nil), dst...)
		first, second := saved[1].([]byte), saved[2].([]byte)
		first[0] = 'X'
		if string(second) != "infinity" {
			t.Fatal("exports alias")
		}
		cancel()
		for range 2 {
			if err := cursor.Next(dst); !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation not sticky", err)
			}
		}
		if !reflect.DeepEqual(cursor.Columns(), []string{"id", "v", "v"}) || saved[0] != int64(1) || string(second) != "infinity" {
			t.Fatal("terminal cleanup invalidated saved values/schema")
		}
		if err := cursor.Close(); err != nil {
			return err
		}
		if err := cursor.Close(); err != nil {
			return err
		}
		if err := cursor.Next(dst); err != io.EOF {
			t.Fatal(err)
		}
		other, err := query.QueryContext(p.ctx, "SELECT v FROM t ORDER BY id", nil)
		if err != nil {
			return err
		}
		defer other.Close()
		if err := other.Next(make([]driver.Value, 0)); err == nil {
			t.Fatal("destination width accepted")
		}
		again, err := query.QueryContext(p.ctx, "SELECT v FROM t ORDER BY id", nil)
		if err != nil {
			return err
		}
		defer again.Close()
		one := make([]driver.Value, 1)
		if err := again.Next(one); err != nil || string(one[0].([]byte)) != "infinity" {
			t.Fatal(one, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

type queryCoreExportContext struct {
	context.Context
	armed     atomic.Bool
	remaining atomic.Int64
}

func (c *queryCoreExportContext) Err() error {
	if c.armed.Load() && c.remaining.Add(-1) <= 0 {
		return context.Canceled
	}
	return c.Context.Err()
}

func TestReferenceQueryCoreExportCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1),(2)", 2))
	step := referenceSelect("SELECT id,id FROM t", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}})
	step.bag = true
	p.step(t, step)
	p.step(t, referencePrepared(step))
	conn, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		ctx := &queryCoreExportContext{Context: p.ctx}
		cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, step.query, nil)
		if err != nil {
			return err
		}
		defer cursor.Close()
		ctx.remaining.Store(4)
		ctx.armed.Store(true)
		dst := make([]driver.Value, 2)
		if err := cursor.Next(dst); err != context.Canceled {
			t.Fatal("export failure not propagated", err)
		}
		ctx.armed.Store(false)
		if err := cursor.Next(dst); err != context.Canceled {
			t.Fatal("export failure not sticky", err)
		}
		if !reflect.DeepEqual(cursor.Columns(), []string{"id", "id"}) {
			t.Fatal(cursor.Columns())
		}
		if err := cursor.Close(); err != nil {
			return err
		}
		if err := cursor.Next(dst); err != io.EOF {
			t.Fatal(err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
