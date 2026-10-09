package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceEnumValues(t *testing.T) {
	enumReferenceScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn) {
		enumReferenceSetup(t, p, backend, conn)
		run := func(s referenceStep) { enumReferenceRunStep(t, p.ctx, conn, backend.recorder, s) }
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				order string
				rows  [][]any
			}{
				{"id", [][]any{{int64(1), "happy", "sad"}, {int64(2), "sad", "happy"}, {int64(3), "ok", "ok"}, {int64(4), nil, nil}}},
				{"m ASC", [][]any{{int64(2), "sad", "happy"}, {int64(3), "ok", "ok"}, {int64(1), "happy", "sad"}, {int64(4), nil, nil}}},
				{"m DESC", [][]any{{int64(4), nil, nil}, {int64(1), "happy", "sad"}, {int64(3), "ok", "ok"}, {int64(2), "sad", "happy"}}},
				{"h ASC", [][]any{{int64(2), "sad", "happy"}, {int64(3), "ok", "ok"}, {int64(1), "happy", "sad"}, {int64(4), nil, nil}}},
				{"m ASC NULLS FIRST", [][]any{{int64(4), nil, nil}, {int64(2), "sad", "happy"}, {int64(3), "ok", "ok"}, {int64(1), "happy", "sad"}}},
				{"m DESC NULLS LAST", [][]any{{int64(1), "happy", "sad"}, {int64(3), "ok", "ok"}, {int64(2), "sad", "happy"}, {int64(4), nil, nil}}},
			} {
				s := referenceSelect("SELECT id,m,h FROM enum_rows ORDER BY "+tc.order, []string{"id", "m", "h"}, []string{"INT4", "", ""}, tc.rows)
				s.prepared = prepared
				run(s)
			}
		}
		run(referenceExec("CREATE TYPE exact_labels AS ENUM ('','sad','Sad',' sad','sad ','it''s',E'back\\\\slash')", 0))
		run(referenceExec("CREATE TABLE enum_labels(id integer,v exact_labels)", 0))
		labels := []string{"", "sad", "Sad", " sad", "sad ", "it's", "back\\slash"}
		want := make([][]any, 0, len(labels))
		for i, label := range labels {
			s := referenceExec("INSERT INTO enum_labels VALUES ($1,$2)", 1, i, label)
			s.prepared = i%2 == 1
			run(s)
			want = append(want, []any{int64(i), label})
		}
		run(referenceSelect("SELECT id,v FROM enum_labels ORDER BY id", []string{"id", "v"}, []string{"INT4", ""}, want))
		run(referencePrepared(referenceSelect("SELECT id,v FROM enum_labels ORDER BY id", []string{"id", "v"}, []string{"INT4", ""}, want)))
		run(referenceExec("CREATE TYPE empty_enum AS ENUM ()", 0))
		run(referenceExec("CREATE TABLE enum_empty(v empty_enum)", 0))
		run(referencePrepared(referenceExec("INSERT INTO enum_empty VALUES ($1)", 1, nil)))
		run(referenceSelect("SELECT v FROM enum_empty", []string{"v"}, []string{""}, [][]any{{nil}}))
	})
}

func TestReferenceEnumComparisons(t *testing.T) {
	enumReferenceScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn) {
		enumReferenceSetup(t, p, backend, conn)
		for _, tc := range []struct {
			op  string
			ids []int64
		}{{"=", []int64{3}}, {"<>", []int64{1, 2}}, {"<", []int64{2}}, {"<=", []int64{2, 3}}, {">", []int64{1}}, {">=", []int64{1, 3}}} {
			want := make([][]any, 0, len(tc.ids))
			for _, id := range tc.ids {
				want = append(want, []any{id})
			}
			for _, prepared := range []bool{false, true} {
				s := referenceSelect("SELECT id FROM enum_rows WHERE m "+tc.op+" 'ok' ORDER BY id", []string{"id"}, []string{"INT4"}, want)
				s.prepared = prepared
				referenceRunStep(t, p.ctx, conn, backend.recorder, s)
			}
			for _, query := range []string{
				"SELECT 'ok'::mood " + tc.op + " 'ok'::happiness",
				"SELECT NULL::mood " + tc.op + " NULL::happiness",
				"SELECT m " + tc.op + " h FROM enum_rows WHERE id=99",
			} {
				enumReferenceError(t, p, conn, query, "42883", true)
			}
		}
		for _, prepared := range []bool{false, true} {
			s := referenceSelect("SELECT NULL::mood = 'ok'::mood AS v", []string{"v"}, []string{"BOOL"}, [][]any{{nil}})
			s.prepared = prepared
			referenceRunStep(t, p.ctx, conn, backend.recorder, s)
			s = referenceSelect("SELECT id FROM enum_rows WHERE m::text = h::text ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}})
			s.prepared = prepared
			referenceRunStep(t, p.ctx, conn, backend.recorder, s)
		}
	})
}

func TestReferenceEnumBindsMetadata(t *testing.T) {
	enumReferenceScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn) {
		enumReferenceSetup(t, p, backend, conn)
		for _, tc := range []struct {
			query string
			rows  [][]any
			args  []any
		}{
			{"SELECT m AS v FROM enum_rows ORDER BY id", [][]any{{"happy"}, {"sad"}, {"ok"}, {nil}}, nil},
			{"SELECT m AS v FROM enum_rows WHERE id=99", nil, nil},
			{"SELECT m AS v FROM enum_rows WHERE id=4", [][]any{{nil}}, nil},
			{"SELECT $1::mood AS v", [][]any{{nil}}, []any{nil}},
		} {
			for _, prepared := range []bool{false, true} {
				s := referenceSelect(tc.query, []string{"v"}, []string{""}, tc.rows, tc.args...)
				s.prepared = prepared
				s.checkMetadata = enumReferenceMetadata(backend.pg, tc.query != "SELECT $1::mood AS v")
				enumReferenceRunStep(t, p.ctx, conn, backend.recorder, s)
			}
		}
		for _, query := range []string{"SELECT $1::mood AS v", "SELECT id FROM enum_rows WHERE m=$1 ORDER BY id", "SELECT id FROM enum_rows WHERE $1=m ORDER BY id"} {
			func() {
				stmt, err := conn.PrepareContext(p.ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: query}
				for i, arg := range []any{"ok", "sad", nil, []byte("happy")} {
					var s referenceStep
					if query == "SELECT $1::mood AS v" {
						values := []any{"ok", "sad", nil, "happy"}
						s = referenceSelect(query, []string{"v"}, []string{""}, [][]any{{values[i]}}, arg)
					} else {
						ids := []int64{3, 2, 0, 1}
						var rows [][]any
						if ids[i] != 0 {
							rows = [][]any{{ids[i]}}
						}
						s = referenceSelect(query, []string{"id"}, []string{"INT4"}, rows, arg)
					}
					enumReferenceRunStep(t, p.ctx, held, backend.recorder, s)
				}
				// Retain the SAME inferred signature for the bad portal input.
				rows, err := stmt.QueryContext(p.ctx, "nope")
				if rows != nil {
					rows.Close()
					t.Fatal("bad held bind exposed cursor")
				}
				enumReferenceAssertDiagnostic(t, err, referenceDiagnostic{
					Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02",
					Message: `invalid input value for enum mood: "nope"`,
					Context: "unnamed portal parameter $1 = '...'",
				})
			}()
		}
		q := "INSERT INTO enum_rows VALUES ($1,$2,$3)"
		stmt, err := conn.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
		for i, value := range []any{"ok", "sad", nil} {
			referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, i+5, value, value))
		}
		enumReferenceRunStep(t, p.ctx, conn, backend.recorder, referenceSelect("SELECT id,m,h FROM enum_rows WHERE id>=5 ORDER BY id", []string{"id", "m", "h"}, []string{"INT4", "", ""}, [][]any{{int64(5), "ok", "ok"}, {int64(6), "sad", "sad"}, {int64(7), nil, nil}}))
		owned := []byte("happy")
		referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, 8, owned, "ok"))
		copy(owned, "xxxxx")
		enumReferenceRunStep(t, p.ctx, conn, backend.recorder, referenceSelect("SELECT m AS v FROM enum_rows WHERE id=8", []string{"v"}, []string{""}, [][]any{{"happy"}}))
		referenceRunStep(t, p.ctx, conn, backend.recorder, referencePrepared(referenceSelect("SELECT $1::mood = $1::mood AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}, "ok")))
	})
}

func TestReferenceEnumSmoke(t *testing.T) {
	enumReferenceScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn) {
		enumReferenceSetup(t, p, backend, conn)
		referenceRunStep(t, p.ctx, conn, backend.recorder, referencePrepared(referenceExec("INSERT INTO enum_rows VALUES (5,$1,$2)", 1, "sad", nil)))
		s := referenceSelect("SELECT m AS v FROM enum_rows ORDER BY m,id", []string{"v"}, []string{""}, [][]any{{"sad"}, {"sad"}, {"ok"}, {"happy"}, {nil}})
		s.checkMetadata = enumReferenceMetadata(backend.pg, true)
		enumReferenceRunStep(t, p.ctx, conn, backend.recorder, s)
		referenceRunStep(t, p.ctx, conn, backend.recorder, referenceSelect("SELECT id FROM enum_rows WHERE m='ok' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}))
		enumReferenceError(t, p, conn, "SELECT 'nope'::mood", "22P02", true)
		enumReferenceError(t, p, conn, "SELECT 'sad'::mood = 'sad'::happiness", "42883", true)
	})
}
