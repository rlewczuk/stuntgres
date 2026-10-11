package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSmokeQueryCoreSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		func() {
			ctx, db := p.Ctx, backend.DB
			run := func(step fixture.Step) { t.Helper(); fixture.RunStep(t, ctx, db, backend.Recorder, step) }
			exec := func(q string, want int64) { t.Helper(); run(fixture.Exec(q, want)) }
			diagnostic := func(q string, want fixture.Diagnostic) {
				t.Helper()
				step := fixture.ImplicitError(q, want)
				step.SelectRows, step.ErrorStage = true, "execute"
				run(step)
				step.ErrorStage = "prepare"
				run(fixture.Prepared(step))
			}
			for _, tc := range []struct{ query, position string }{
				{"SELECT id FROM t", "16"},
				{"SELECT /*é*/ id FROM t", "22"},
			} {
				diagnostic(tc.query, fixture.Diagnostic{Code: "42P01", Message: `relation "t" does not exist`, Position: tc.position})
			}
			exec("CREATE TABLE t(id INT NOT NULL,v INT,s TEXT,f BOOL)", 0)
			exec("INSERT INTO t VALUES(1,1,'x',TRUE),(2,NULL,'x',NULL),(3,2,'y',FALSE),(4,1,'x',TRUE)", 4)
			exec("CREATE TABLE empty(id INT NOT NULL,v INT,s TEXT,f BOOL)", 0)
			for _, tc := range []struct {
				q            string
				names, types []string
				want         [][]any
				args         []any
				bag          bool
			}{
				{"SELECT id,7,NULL,f FROM t ORDER BY id", []string{"id", "?column?", "?column?", "f"}, []string{"INT4", "INT4", "TEXT", "BOOL"}, [][]any{{int64(1), int64(7), nil, true}, {int64(2), int64(7), nil, nil}, {int64(3), int64(7), nil, false}, {int64(4), int64(7), nil, true}}, nil, false},
				{"SELECT id,7,NULL,f FROM empty", []string{"id", "?column?", "?column?", "f"}, []string{"INT4", "INT4", "TEXT", "BOOL"}, [][]any{}, nil, true},
				{"SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}, nil, true},
				{"SELECT 7 FROM t WHERE v=1", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}, nil, true},
				{"SELECT v FROM t WHERE v=NULL", []string{"v"}, []string{"INT4"}, [][]any{}, nil, true},
				{"SELECT id FROM t WHERE FALSE", []string{"id"}, []string{"INT4"}, [][]any{}, nil, true},
				{"SELECT id FROM t WHERE NULL", []string{"id"}, []string{"INT4"}, [][]any{}, nil, true},
				{"SELECT id FROM t WHERE f", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}}, nil, true},
				{"SELECT $1 FROM t WHERE s=$1", []string{"?column?"}, []string{"TEXT"}, [][]any{{"x"}, {"x"}, {"x"}}, []any{"x"}, true},
				{"SELECT v FROM t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}, {nil}}, nil, false},
				{"SELECT v FROM t ORDER BY v DESC", []string{"v"}, []string{"INT4"}, [][]any{{nil}, {int64(2)}, {int64(1)}, {int64(1)}}, nil, false},
			} {
				step := fixture.Select(tc.q, tc.names, tc.types, tc.want, tc.args...)
				step.Bag = tc.bag
				run(step)
				run(fixture.Prepared(step))
			}
			held, err := db.PrepareContext(ctx, "SELECT id FROM t WHERE v=$1 ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for i, arg := range []any{1, 2, nil} {
				want := [][]any{{int64(1)}, {int64(4)}}
				if i == 1 {
					want = [][]any{{int64(3)}}
				}
				if i == 2 {
					want = [][]any{}
				}
				step := fixture.Select("SELECT id FROM t WHERE v=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, want, arg)
				fixture.RunStep(t, ctx, fixture.HeldStatement{db, held, step.Query}, backend.Recorder, step)
			}
			for _, tc := range []struct{ q, position string }{
				{"SELECT id FROM t ORDER BY missing", "27"},
				{"SELECT /*é*/ id FROM t ORDER BY missing", "33"},
			} {
				diagnostic(tc.q, fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: tc.position})
			}
			t.Run("malformed shared syntax", func(t *testing.T) {
				step := fixture.ImplicitError("SELECT id FROM t WHERE", fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: "23"})
				step.SelectRows, step.ErrorStage = true, "execute"
				fixture.RunStep(t, ctx, db, backend.Recorder, step)
				step.ErrorStage = "prepare"
				fixture.RunStep(t, ctx, db, backend.Recorder, fixture.Prepared(step))
			})
			// Width does not erase the two row occurrences on either backend.
			exec("CREATE TABLE zero(v INT)", 0)
			exec("INSERT INTO zero VALUES(1),(2)", 2)
			exec("ALTER TABLE zero DROP COLUMN v", 0)
			zero := fixture.Select("SELECT * FROM zero", []string{}, []string{}, [][]any{{}, {}})
			zero.Bag = true
			run(zero)
			if backend.PG {
				return
			}
			// Raw driver iteration makes EOF/error cleanup and sticky errors observable.
			raw, err := p.NativeConnector.Connect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			query := raw.(driver.QueryerContext)
			cancelCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			cursor, err := query.QueryContext(cancelCtx, "SELECT id,7 FROM t ORDER BY id", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cursor.Close()
			dst := make([]driver.Value, 2)
			if err := cursor.Next(dst); err != nil {
				t.Fatal(err)
			}
			saved := append([]driver.Value(nil), dst...)
			cancel()
			for range 2 {
				if err := cursor.Next(dst); !errors.Is(err, context.Canceled) {
					t.Fatal("nonsticky cancellation", err)
				}
			}
			if !reflect.DeepEqual(saved, []driver.Value{int64(1), int64(7)}) || !reflect.DeepEqual(cursor.Columns(), []string{"id", "?column?"}) {
				t.Fatal(saved, cursor.Columns())
			}
			cursor.Close()
			cursor.Close()
			if err := cursor.Next(dst); err != io.EOF {
				t.Fatal(err)
			}
			direct, err := query.QueryContext(ctx, "SELECT id FROM t ORDER BY id", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer direct.Close()
			owned := []byte("owned")
			computed, err := query.QueryContext(ctx, "SELECT id,$1 FROM t ORDER BY id", []driver.NamedValue{{Ordinal: 1, Value: owned}})
			if err != nil {
				t.Fatal(err)
			}
			defer computed.Close()
			owned[0] = 'X'
			exec("INSERT INTO t VALUES(5,5,'later',TRUE)", 1)
			if err := stuntgres.Reset(ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE t(replacement TEXT); INSERT INTO t VALUES('99')")); err != nil {
				t.Fatal(err)
			}
			for _, rows := range []driver.Rows{direct, computed} {
				if rows.Columns()[0] != "id" || rows.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "INT4" {
					t.Fatal("replacement metadata")
				}
				dst := make([]driver.Value, len(rows.Columns()))
				for i := range 4 {
					if err := rows.Next(dst); err != nil || dst[0] != int64(i+1) {
						t.Fatal(dst, err)
					}
					if len(dst) == 2 && dst[1] != "owned" {
						t.Fatal(dst)
					}
				}
				if err := rows.Next(dst); err != io.EOF {
					t.Fatal(err)
				}
				if rows.Columns()[0] != "id" {
					t.Fatal("EOF erased schema")
				}
			}
			run(fixture.Select("SELECT replacement FROM t", []string{"replacement"}, []string{"TEXT"}, [][]any{{"99"}}))
			exec("CREATE TABLE times(v TIMESTAMP)", 0)
			exec("INSERT INTO times VALUES('infinity'),('infinity')", 2)
			bytesRows, err := query.QueryContext(ctx, "SELECT v,v FROM times", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer bytesRows.Close()
			bytesDst := make([]driver.Value, 2)
			if err := bytesRows.Next(bytesDst); err != nil {
				t.Fatal(err)
			}
			first, second := bytesDst[0].([]byte), bytesDst[1].([]byte)
			first[0] = 'X'
			if string(second) != "infinity" {
				t.Fatal("mutable exports alias")
			}
			if err := bytesRows.Next(bytesDst); err != nil || string(bytesDst[0].([]byte)) != "infinity" {
				t.Fatal(bytesDst, err)
			}
			bytesRows.Close()
			if string(second) != "infinity" {
				t.Fatal("Close invalidated export")
			}
		}()
	}
}
