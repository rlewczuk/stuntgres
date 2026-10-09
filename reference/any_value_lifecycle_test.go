package reference_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
)

func anyValueViewLifecycle(t *testing.T, p *referencePair, backend referenceBackend) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE av_source(v INT,b BYTEA,n NUMERIC(10,4),s VARCHAR(4) COLLATE \"C\")", 0),
		referenceExec("INSERT INTO av_source VALUES(41,'\\x00ff',1.2300,'xy')", 1),
		referenceExec("CREATE VIEW av_columns AS SELECT any_value(v) AS v,any_value(b) AS b,any_value(n) AS n,any_value(s) AS s FROM av_source", 0),
		referenceExec("CREATE VIEW av_literal AS SELECT any_value(9::integer) AS v FROM av_source", 0),
		referenceExec("CREATE VIEW av_nested AS SELECT v,b,n,s FROM av_columns", 0),
	} {
		run(s)
	}
	check := func() {
		anyValueRoutes(t, p, backend, referenceSelect("SELECT v,b,n,s FROM av_nested", []string{"v", "b", "n", "s"}, []string{"INT4", "BYTEA", "NUMERIC", "VARCHAR"}, [][]any{{int64(41), string([]byte{0, 255}), "1.2300", "xy"}}), referenceSelect("SELECT v FROM av_literal", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
	}
	check()
	const q = "SELECT any_value(b) AS b,any_value(n) AS n FROM av_source"
	stmt, err := backend.db.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held, err := stmt.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if !backend.pg {
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
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE broken(")); err == nil {
			t.Fatal("invalid Restore succeeded")
		}
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Restore: %v", err)
		}
	}
	got := anyValueConsume(t, held, backend.recorder, q, []string{"b", "n"}, []string{"BYTEA", "NUMERIC"})
	if !reflect.DeepEqual(got, [][]any{{string([]byte{0, 255}), "1.2300"}}) {
		t.Fatalf("held rows %#v", got)
	}
	referenceRunStep(t, p.ctx, referenceHeldStatement{backend.db, stmt, q}, backend.recorder, referenceSelect(q, []string{"b", "n"}, []string{"BYTEA", "NUMERIC"}, [][]any{{string([]byte{0, 255}), "1.2300"}}))
	check()
	run(referenceExec("DELETE FROM av_source", 1))
	empty := referenceSelect("SELECT v,b,n,s FROM av_nested", []string{"v", "b", "n", "s"}, []string{"INT4", "BYTEA", "NUMERIC", "VARCHAR"}, [][]any{{nil, nil, nil, nil}})
	anyValueRoutes(t, p, backend, empty, referenceSelect("SELECT v FROM av_literal", []string{"v"}, []string{"INT4"}, [][]any{{nil}}))
	if !backend.pg {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		anyValueRoutes(t, p, backend, empty)
	}
}

func TestReferenceAnyValueViews(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) { anyValueViewLifecycle(t, p, backend) })
	}
}

func TestReferenceAnyValuePrepared(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(v INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(41)", 1))
			const q = "SELECT any_value(v) FROM t"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			check := func(value any) {
				referenceRunStep(t, p.ctx, referenceHeldStatement{backend.db, stmt, q}, backend.recorder, referenceSelect(q, []string{"any_value"}, []string{"INT4"}, [][]any{{value}}))
			}
			check(int64(41))
			held, err := stmt.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder))
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			backend.recorder.mu.Lock()
			heldRecord := backend.recorder.operations[len(backend.recorder.operations)-1]
			backend.recorder.mu.Unlock()
			run(referenceExec("INSERT INTO t VALUES(NULL)", 1))
			check(int64(41))
			run(referenceExec("DELETE FROM t", 2))
			run(referenceExec("INSERT INTO t VALUES(73)", 1))
			check(int64(73))
			if got := anyValueConsume(t, held, backend.recorder, q, []string{"any_value"}, []string{"INT4"}, heldRecord); !reflect.DeepEqual(got, [][]any{{int64(41)}}) {
				t.Fatalf("held %#v", got)
			}
			run(referenceExec("DROP TABLE t", 0))
			run(referenceExec("CREATE TABLE t(v TEXT)", 0))
			s := referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "cached plan must not change result type"})
			s.selectRows = true
			referenceRunStep(t, p.ctx, referenceHeldStatement{backend.db, stmt, q}, backend.recorder, s)
		})
	}
}

func TestReferenceAnyValueOwnership(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("CREATE TABLE t(v INT)", 0))
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO t VALUES(1)", 1))
			const q = "SELECT any_value($1::bytea) AS b FROM t"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			input := []byte{0, 255, 92}
			rows, err := stmt.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), input)
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
			backend.recorder.mu.Lock()
			op := backend.recorder.operations[len(backend.recorder.operations)-1]
			owned := op.Closed && op.CloseErr == nil && len(op.Rows) == 1 && bytes.Equal(op.Rows[0][0].([]byte), []byte{0, 255, 92})
			backend.recorder.mu.Unlock()
			if !owned {
				t.Fatal("middleware BYTEA aliased exported cell")
			}
			referenceRunStep(t, p.ctx, referenceHeldStatement{backend.db, stmt, q}, backend.recorder, referenceSelect(q, []string{"b"}, []string{"BYTEA"}, [][]any{{string([]byte{99, 255, 92})}}, input))
		})
	}
}
