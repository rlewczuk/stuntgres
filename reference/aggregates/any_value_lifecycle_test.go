package reference_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func anyValueViewLifecycle(t *testing.T, p *fixture.Pair, backend fixture.Backend) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE av_source(v INT,b BYTEA,n NUMERIC(10,4),s VARCHAR(4) COLLATE \"C\")", 0),
		fixture.Exec("INSERT INTO av_source VALUES(41,'\\x00ff',1.2300,'xy')", 1),
		fixture.Exec("CREATE VIEW av_columns AS SELECT any_value(v) AS v,any_value(b) AS b,any_value(n) AS n,any_value(s) AS s FROM av_source", 0),
		fixture.Exec("CREATE VIEW av_literal AS SELECT any_value(9::integer) AS v FROM av_source", 0),
		fixture.Exec("CREATE VIEW av_nested AS SELECT v,b,n,s FROM av_columns", 0),
	} {
		run(s)
	}
	check := func() {
		fixture.AnyValueRoutes(t, p, backend, fixture.Select("SELECT v,b,n,s FROM av_nested", []string{"v", "b", "n", "s"}, []string{"INT4", "BYTEA", "NUMERIC", "VARCHAR"}, [][]any{{int64(41), string([]byte{0, 255}), "1.2300", "xy"}}), fixture.Select("SELECT v FROM av_literal", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
	}
	check()
	const q = "SELECT any_value(b) AS b,any_value(n) AS n FROM av_source"
	stmt, err := backend.DB.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held, err := stmt.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if !backend.PG {
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
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE broken(")); err == nil {
			t.Fatal("invalid Restore succeeded")
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Restore: %v", err)
		}
	}
	got := fixture.AnyValueConsume(t, held, backend.Recorder, q, []string{"b", "n"}, []string{"BYTEA", "NUMERIC"})
	if !reflect.DeepEqual(got, [][]any{{string([]byte{0, 255}), "1.2300"}}) {
		t.Fatalf("held rows %#v", got)
	}
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, stmt, q}, backend.Recorder, fixture.Select(q, []string{"b", "n"}, []string{"BYTEA", "NUMERIC"}, [][]any{{string([]byte{0, 255}), "1.2300"}}))
	check()
	run(fixture.Exec("DELETE FROM av_source", 1))
	empty := fixture.Select("SELECT v,b,n,s FROM av_nested", []string{"v", "b", "n", "s"}, []string{"INT4", "BYTEA", "NUMERIC", "VARCHAR"}, [][]any{{nil, nil, nil, nil}})
	fixture.AnyValueRoutes(t, p, backend, empty, fixture.Select("SELECT v FROM av_literal", []string{"v"}, []string{"INT4"}, [][]any{{nil}}))
	if !backend.PG {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		fixture.AnyValueRoutes(t, p, backend, empty)
	}
}

func TestReferenceAnyValueViews(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { anyValueViewLifecycle(t, p, backend) })
	}
}

func TestReferenceAnyValuePrepared(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(v INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(41)", 1))
			const q = "SELECT any_value(v) FROM t"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			check := func(value any) {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, stmt, q}, backend.Recorder, fixture.Select(q, []string{"any_value"}, []string{"INT4"}, [][]any{{value}}))
			}
			check(int64(41))
			held, err := stmt.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder))
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			backend.Recorder.Mu.Lock()
			heldRecord := backend.Recorder.Operations[len(backend.Recorder.Operations)-1]
			backend.Recorder.Mu.Unlock()
			run(fixture.Exec("INSERT INTO t VALUES(NULL)", 1))
			check(int64(41))
			run(fixture.Exec("DELETE FROM t", 2))
			run(fixture.Exec("INSERT INTO t VALUES(73)", 1))
			check(int64(73))
			if got := fixture.AnyValueConsume(t, held, backend.Recorder, q, []string{"any_value"}, []string{"INT4"}, heldRecord); !reflect.DeepEqual(got, [][]any{{int64(41)}}) {
				t.Fatalf("held %#v", got)
			}
			run(fixture.Exec("DROP TABLE t", 0))
			run(fixture.Exec("CREATE TABLE t(v TEXT)", 0))
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "cached plan must not change result type"})
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, stmt, q}, backend.Recorder, s)
		})
	}
}

func TestReferenceAnyValueOwnership(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE t(v INT)", 0))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(1)", 1))
			const q = "SELECT any_value($1::bytea) AS b FROM t"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			input := []byte{0, 255, 92}
			rows, err := stmt.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder), input)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			input[0] = 99
			if !rows.Next() {
				t.Fatal("missing BYTEA result")
			}
			var value any
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			payload, ok := value.([]byte)
			if !ok || !bytes.Equal(payload, []byte{0, 255, 92}) {
				t.Fatalf("owned BYTEA %#v", value)
			}
			payload[0] = 77
			if rows.Next() || rows.Err() != nil {
				t.Fatalf("unexpected rows/error %v", rows.Err())
			}
			rows.Close()
			backend.Recorder.Mu.Lock()
			op := backend.Recorder.Operations[len(backend.Recorder.Operations)-1]
			owned := op.Closed && op.CloseErr == nil && len(op.Rows) == 1 && bytes.Equal(op.Rows[0][0].([]byte), []byte{0, 255, 92})
			backend.Recorder.Mu.Unlock()
			if !owned {
				t.Fatal("middleware BYTEA aliased exported cell")
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, stmt, q}, backend.Recorder, fixture.Select(q, []string{"b"}, []string{"BYTEA"}, [][]any{{string([]byte{99, 255, 92})}}, input))
		})
	}
}
