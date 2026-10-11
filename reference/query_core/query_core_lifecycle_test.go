package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"sync/atomic"
	"testing"
)

func TestReferenceQueryCoreCapturedOwnership(t *testing.T) {
	for _, replacement := range []string{"reset", "restore", "reset-restore"} {
		t.Run(replacement, func(t *testing.T) {
			p := fixture.NewScenario(t)
			replacementValue := "99"
			if replacement == "reset-restore" {
				replacementValue = "after"
			}
			p.Step(t, fixture.Exec("CREATE TABLE t(id INT NOT NULL)", 0))
			p.Step(t, fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
			original := fixture.Select("SELECT id,$1 FROM t ORDER BY id", []string{"id", "?column?"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "before"}, {int64(2), "before"}}, []byte("before"))
			p.Step(t, original)
			p.Step(t, fixture.Prepared(original))
			step := fixture.Select("SELECT id,$1,NULL FROM t ORDER BY id", []string{"id", "?column?", "?column?"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "before", nil}, {int64(2), "before", nil}}, []byte("before"))
			p.Step(t, step)
			p.Step(t, fixture.Prepared(step))
			conn, err := p.Native.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			err = conn.Raw(func(raw any) error {
				query := raw.(driver.QueryerContext)
				direct, err := query.QueryContext(p.Ctx, "SELECT id FROM t ORDER BY id", nil)
				if err != nil {
					return err
				}
				defer direct.Close()
				owned := []byte("before")
				computed, err := query.QueryContext(p.Ctx, step.Query, []driver.NamedValue{{Ordinal: 1, Value: owned}})
				if err != nil {
					return err
				}
				defer computed.Close()
				originalComputed, err := query.QueryContext(p.Ctx, original.Query, []driver.NamedValue{{Ordinal: 1, Value: owned}})
				if err != nil {
					return err
				}
				defer originalComputed.Close()
				owned[0] = 'X'
				fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec("INSERT INTO t VALUES(3)", 1))
				if replacement == "reset-restore" {
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						return err
					}
				}
				if replacement == "reset" {
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						return err
					}
				} else if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE t(replacement TEXT); INSERT INTO t VALUES('"+replacementValue+"')")); err != nil {
					return err
				}
				for _, cursor := range []driver.Rows{direct, originalComputed, computed} {
					names := cursor.Columns()
					names[0] = "changed"
					if cursor.Columns()[0] != "id" || cursor.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "INT4" {
						t.Fatal("metadata replaced or borrowed")
					}
					wantNames, wantTypes := []string{"id"}, []string{"INT4"}
					if cursor == computed {
						wantNames, wantTypes = step.Columns, step.Types
					}
					if cursor == originalComputed {
						wantNames, wantTypes = original.Columns, original.Types
					}
					if !reflect.DeepEqual(cursor.Columns(), wantNames) {
						t.Fatal(cursor.Columns())
					}
					for i, name := range wantTypes {
						if cursor.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(i) != name {
							t.Fatal("metadata replaced")
						}
					}
					dst := make([]driver.Value, len(cursor.Columns()))
					for i := range 2 {
						if err := cursor.Next(dst); err != nil || dst[0] != int64(i+1) {
							t.Fatal(dst, err)
						}
						if len(dst) >= 2 && dst[1] != "before" || len(dst) == 3 && dst[2] != nil {
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
				if replacement != "reset" {
					fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT replacement FROM t", []string{"replacement"}, []string{"TEXT"}, [][]any{{replacementValue}}))
					err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES($1)"))
					want := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
					want.Position = "65"
					fixture.AssertDiagnostic(t, err, &want)
					fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT replacement FROM t", []string{"replacement"}, []string{"TEXT"}, [][]any{{replacementValue}}))
				} else {
					failure := fixture.ImplicitError("SELECT id FROM t", fixture.Diagnostic{Code: "42P01", Message: `relation "t" does not exist`, Position: "16"})
					failure.SelectRows = true
					fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, failure)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if p.Postgres != nil {
				owned := []byte("before")
				rows, err := p.Postgres.QueryContext(p.Ctx, step.Query, owned)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				owned[0] = 'X'
				run := func(s fixture.Step) { fixture.ScalarRunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s) }
				run(fixture.Exec("INSERT INTO t VALUES(3)", 1))
				fixture.ScalarRows(t, true, rows, step.Columns, step.Types, step.Rows, false)
				run(fixture.Exec("DROP TABLE t", 0))
				run(fixture.Exec("CREATE TABLE t(replacement TEXT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES('after')", 1))
				run(fixture.Select("SELECT replacement FROM t", []string{"replacement"}, []string{"TEXT"}, [][]any{{"after"}}))
			}
		})
	}
}

func TestReferenceQueryCoreRawCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,v TIMESTAMP)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,'infinity'),(2,'infinity')", 2))
	step := fixture.Select("SELECT id,v,v FROM t ORDER BY id", []string{"id", "v", "v"}, []string{"INT4", "TIMESTAMP", "TIMESTAMP"}, [][]any{{int64(1), "infinity", "infinity"}, {int64(2), "infinity", "infinity"}})
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		query := raw.(driver.QueryerContext)
		ctx, cancel := context.WithCancel(p.Ctx)
		defer cancel()
		cursor, err := query.QueryContext(ctx, step.Query, nil)
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
		other, err := query.QueryContext(p.Ctx, "SELECT v FROM t ORDER BY id", nil)
		if err != nil {
			return err
		}
		defer other.Close()
		if err := other.Next(make([]driver.Value, 0)); err == nil {
			t.Fatal("destination width accepted")
		}
		again, err := query.QueryContext(p.Ctx, "SELECT v FROM t ORDER BY id", nil)
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
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
	step := fixture.Select("SELECT id,id FROM t", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}})
	step.Bag = true
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		ctx := &queryCoreExportContext{Context: p.Ctx}
		cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, step.Query, nil)
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
