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

func TestReferenceExtremaLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE DOMAIN extrema_life_int AS INTEGER", 0),
				fixture.Exec("CREATE DOMAIN extrema_life_text AS VARCHAR(4) COLLATE \"C\"", 0),
				fixture.Exec("CREATE TABLE extrema_life(v extrema_life_int,s extrema_life_text,n NUMERIC(10,4),b BYTEA)", 0),
				fixture.Exec("INSERT INTO extrema_life VALUES(4,'z',1.2300,'\\x00ff'),(-2,'a',9.8700,'\\xff00'),(NULL,NULL,NULL,NULL)", 3),
				fixture.Exec("CREATE VIEW extrema_life_view AS SELECT min(v) AS lo,max(v) AS hi,min(s) AS s,max(n) AS n,min(b) AS b FROM extrema_life", 0),
				fixture.Exec("CREATE VIEW extrema_life_nested AS SELECT lo,hi,s,n,b FROM extrema_life_view", 0),
				fixture.Exec("CREATE VIEW extrema_life_literal AS SELECT min('constant') AS v FROM extrema_life", 0),
				fixture.Exec("CREATE VIEW extrema_life_catalog AS SELECT min(relname) AS name,max(relkind) AS kind FROM pg_catalog.pg_class WHERE relname='extrema_life'", 0),
			} {
				run(s)
			}
			view := fixture.Select("SELECT lo,hi,s,n,b FROM extrema_life_nested", []string{"lo", "hi", "s", "n", "b"}, []string{"INT4", "INT4", "TEXT", "NUMERIC", "BYTEA"}, [][]any{{int64(-2), int64(4), "a", "9.8700", string([]byte{0, 255})}})
			check := func() {
				fixture.AnyValueRoutes(t, p, backend, view, fixture.Select("SELECT v FROM extrema_life_literal", []string{"v"}, []string{"TEXT"}, [][]any{{"constant"}}))
				fixture.AnyValueRoutes(t, p, backend, fixture.Select("SELECT name,kind FROM extrema_life_catalog", []string{"name", "kind"}, []string{"TEXT", "TEXT"}, [][]any{{"extrema_life", "r"}}))
			}
			check()
			const q = "SELECT min(v) AS lo,max(v) AS hi FROM extrema_life"
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
			backend.Recorder.Mu.Lock()
			heldRecord := backend.Recorder.Operations[len(backend.Recorder.Operations)-1]
			backend.Recorder.Mu.Unlock()
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
			run(fixture.Exec("INSERT INTO extrema_life VALUES(NULL,NULL,NULL,NULL)", 1))
			if got := fixture.AnyValueConsume(t, held, backend.Recorder, q, []string{"lo", "hi"}, []string{"INT4", "INT4"}, heldRecord); !reflect.DeepEqual(got, [][]any{{int64(-2), int64(4)}}) {
				t.Fatalf("old owned rows %#v", got)
			}
			check()
			executor := fixture.HeldStatement{backend.DB, stmt, q}
			fresh := func(lo, hi any) {
				fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select(q, []string{"lo", "hi"}, []string{"INT4", "INT4"}, [][]any{{lo, hi}}))
			}
			fresh(int64(-2), int64(4))
			run(fixture.Exec("INSERT INTO extrema_life(v) VALUES(-9),(12)", 2))
			fresh(int64(-9), int64(12))
			run(fixture.Exec("DELETE FROM extrema_life", 6))
			fresh(nil, nil)
			empty := fixture.Select("SELECT lo,hi,s,n,b FROM extrema_life_nested", []string{"lo", "hi", "s", "n", "b"}, []string{"INT4", "INT4", "TEXT", "NUMERIC", "BYTEA"}, [][]any{{nil, nil, nil, nil, nil}})
			fixture.AnyValueRoutes(t, p, backend, empty)
			if !backend.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				fixture.AnyValueRoutes(t, p, backend, empty)
				fresh(nil, nil)
			}
		})
	}
}

func TestReferenceExtremaOwnedBytea(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE extrema_owned(v INT)", 0))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO extrema_owned VALUES(1),(2)", 2))
			const q = "SELECT min($1::bytea) AS lo,max($1::bytea) AS hi FROM extrema_owned"
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
			backend.Recorder.Mu.Lock()
			op := backend.Recorder.Operations[len(backend.Recorder.Operations)-1]
			owned := op.Closed && op.CloseErr == nil && len(op.Rows) == 1 && bytes.Equal(op.Rows[0][0].([]byte), []byte{0, 255, 92}) && bytes.Equal(op.Rows[0][1].([]byte), []byte{0, 255, 92})
			backend.Recorder.Mu.Unlock()
			if !owned {
				t.Fatal("middleware rows alias exported extrema")
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, stmt, q}, backend.Recorder, fixture.Select(q, []string{"lo", "hi"}, []string{"BYTEA", "BYTEA"}, [][]any{{string([]byte{99, 255, 92}), string([]byte{99, 255, 92})}}, input))
		})
	}
}
