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

func TestReferenceExtremaLifecycle(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			for _, s := range []referenceStep{
				referenceExec("CREATE DOMAIN extrema_life_int AS INTEGER", 0),
				referenceExec("CREATE DOMAIN extrema_life_text AS VARCHAR(4) COLLATE \"C\"", 0),
				referenceExec("CREATE TABLE extrema_life(v extrema_life_int,s extrema_life_text,n NUMERIC(10,4),b BYTEA)", 0),
				referenceExec("INSERT INTO extrema_life VALUES(4,'z',1.2300,'\\x00ff'),(-2,'a',9.8700,'\\xff00'),(NULL,NULL,NULL,NULL)", 3),
				referenceExec("CREATE VIEW extrema_life_view AS SELECT min(v) AS lo,max(v) AS hi,min(s) AS s,max(n) AS n,min(b) AS b FROM extrema_life", 0),
				referenceExec("CREATE VIEW extrema_life_nested AS SELECT lo,hi,s,n,b FROM extrema_life_view", 0),
				referenceExec("CREATE VIEW extrema_life_literal AS SELECT min('constant') AS v FROM extrema_life", 0),
				referenceExec("CREATE VIEW extrema_life_catalog AS SELECT min(relname) AS name,max(relkind) AS kind FROM pg_catalog.pg_class WHERE relname='extrema_life'", 0),
			} {
				run(s)
			}
			view := referenceSelect("SELECT lo,hi,s,n,b FROM extrema_life_nested", []string{"lo", "hi", "s", "n", "b"}, []string{"INT4", "INT4", "TEXT", "NUMERIC", "BYTEA"}, [][]any{{int64(-2), int64(4), "a", "9.8700", string([]byte{0, 255})}})
			check := func() {
				anyValueRoutes(t, p, backend, view, referenceSelect("SELECT v FROM extrema_life_literal", []string{"v"}, []string{"TEXT"}, [][]any{{"constant"}}))
				anyValueRoutes(t, p, backend, referenceSelect("SELECT name,kind FROM extrema_life_catalog", []string{"name", "kind"}, []string{"TEXT", "TEXT"}, [][]any{{"extrema_life", "r"}}))
			}
			check()
			const q = "SELECT min(v) AS lo,max(v) AS hi FROM extrema_life"
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
			backend.recorder.mu.Lock()
			heldRecord := backend.recorder.operations[len(backend.recorder.operations)-1]
			backend.recorder.mu.Unlock()
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
			run(referenceExec("INSERT INTO extrema_life VALUES(NULL,NULL,NULL,NULL)", 1))
			if got := anyValueConsume(t, held, backend.recorder, q, []string{"lo", "hi"}, []string{"INT4", "INT4"}, heldRecord); !reflect.DeepEqual(got, [][]any{{int64(-2), int64(4)}}) {
				t.Fatalf("old owned rows %#v", got)
			}
			check()
			executor := referenceHeldStatement{backend.db, stmt, q}
			fresh := func(lo, hi any) {
				referenceRunStep(t, p.ctx, executor, backend.recorder, referenceSelect(q, []string{"lo", "hi"}, []string{"INT4", "INT4"}, [][]any{{lo, hi}}))
			}
			fresh(int64(-2), int64(4))
			run(referenceExec("INSERT INTO extrema_life(v) VALUES(-9),(12)", 2))
			fresh(int64(-9), int64(12))
			run(referenceExec("DELETE FROM extrema_life", 6))
			fresh(nil, nil)
			empty := referenceSelect("SELECT lo,hi,s,n,b FROM extrema_life_nested", []string{"lo", "hi", "s", "n", "b"}, []string{"INT4", "INT4", "TEXT", "NUMERIC", "BYTEA"}, [][]any{{nil, nil, nil, nil, nil}})
			anyValueRoutes(t, p, backend, empty)
			if !backend.pg {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				anyValueRoutes(t, p, backend, empty)
				fresh(nil, nil)
			}
		})
	}
}

func TestReferenceExtremaOwnedBytea(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("CREATE TABLE extrema_owned(v INT)", 0))
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO extrema_owned VALUES(1),(2)", 2))
			const q = "SELECT min($1::bytea) AS lo,max($1::bytea) AS hi FROM extrema_owned"
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
				t.Fatal("missing BYTEA extrema", rows.Err())
			}
			var lo, hi []byte
			if err := rows.Scan(&lo, &hi); err != nil || !bytes.Equal(lo, []byte{0, 255, 92}) || !bytes.Equal(hi, []byte{0, 255, 92}) {
				t.Fatal(lo, hi, err)
			}
			lo[0], hi[0] = 77, 88
			if rows.Next() || rows.Err() != nil {
				t.Fatal("unexpected rows", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			backend.recorder.mu.Lock()
			op := backend.recorder.operations[len(backend.recorder.operations)-1]
			owned := op.Closed && op.CloseErr == nil && len(op.Rows) == 1 && bytes.Equal(op.Rows[0][0].([]byte), []byte{0, 255, 92}) && bytes.Equal(op.Rows[0][1].([]byte), []byte{0, 255, 92})
			backend.recorder.mu.Unlock()
			if !owned {
				t.Fatal("middleware rows alias exported extrema")
			}
			referenceRunStep(t, p.ctx, referenceHeldStatement{backend.db, stmt, q}, backend.recorder, referenceSelect(q, []string{"lo", "hi"}, []string{"BYTEA", "BYTEA"}, [][]any{{string([]byte{99, 255, 92}), string([]byte{99, 255, 92})}}, input))
		})
	}
}
