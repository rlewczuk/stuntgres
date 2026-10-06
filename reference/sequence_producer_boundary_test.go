//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReferenceSequenceProducerBoundary(t *testing.T) {
	p := newReferencePair(t)
	// Separate backend subtests keep the release witness reachable if native is red.
	for _, backend := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
	}{{"Native", p.memory, p.memoryRecord}, {"Postgres", p.postgres, p.postgresRecord}} {
		t.Run(backend.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
			defer cancel()
			conn := func() *sql.Conn {
				c, err := backend.db.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { c.Close() })
				return c
			}
			c, b := conn(), conn()
			aSchema := fmt.Sprintf("producer_boundary_a_%d", time.Now().UnixNano())
			bSchema := aSchema + "_b"
			t.Cleanup(func() {
				cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
				defer stop()
				_, _ = c.ExecContext(cleanup, "ROLLBACK")
				for _, schema := range []string{aSchema, bSchema} {
					_, _ = c.ExecContext(cleanup, "DROP SCHEMA "+schema+" CASCADE")
				}
			})
			run := func(step referenceStep) { referenceRunStep(t, ctx, c, backend.record, step) }
			exec := func(q string) { run(referenceStep{query: q}) }
			rows := func(q string, columns, types []string, want [][]any) {
				run(referenceStep{query: q, selectRows: true, columns: columns, types: types, rows: want})
			}
			state := func(name string, value int64, called bool) {
				rows("SELECT last_value,is_called FROM "+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{value, called}})
			}
			exec("CREATE SCHEMA " + aSchema)
			exec("CREATE SCHEMA " + bSchema)
			exec("SET search_path TO " + aSchema)
			for _, q := range []string{"CREATE SEQUENCE s START 31 CACHE 1", "CREATE SEQUENCE nested START 47 CACHE 1", "CREATE TABLE empty_src(id INT)", "CREATE TABLE src(id INT)", "CREATE TABLE dst(v BIGINT)"} {
				exec(q)
			}
			run(referenceStep{query: "INSERT INTO src VALUES(1)", affected: 1})
			bad := controlsReferenceDiagnostic("22P02", `invalid input syntax for type bigint: "bad"`)
			missingInput := controlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
			missingInput.Context = "unnamed portal parameter $1 = '...'"
			for _, prepared := range []bool{false, true} {
				for _, source := range []string{"empty_src", "src WHERE id=99", "src"} {
					for _, tc := range []struct {
						q    string
						arg  any
						diag referenceDiagnostic
					}{
						{"INSERT INTO dst SELECT setval('s',$1::text::bigint) FROM " + source, "bad", bad},
						{"INSERT INTO dst SELECT setval($1,nextval('nested')) FROM " + source, "missing", missingInput},
					} {
						run(referenceStep{query: tc.q, args: []any{tc.arg}, prepared: prepared, state: tc.diag.Code, diagnostic: &tc.diag, errorStage: "execute"})
						rows("SELECT v FROM dst", []string{"v"}, []string{"INT8"}, nil)
						state("s", 31, false)
						state("nested", 47, false)
						for _, name := range []string{"s", "nested"} {
							want := controlsReferenceDiagnostic("55000", `currval of sequence "`+name+`" is not yet defined in this session`)
							run(referenceStep{query: "SELECT currval('" + name + "')", selectRows: true, state: "55000", diagnostic: &want, errorStage: "execute"})
						}
						want := controlsReferenceDiagnostic("55000", "lastval is not yet defined in this session")
						run(referenceStep{query: "SELECT lastval()", selectRows: true, state: "55000", diagnostic: &want, errorStage: "execute"})
					}
				}
			}
			rows("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(31)}})
			rows("SELECT nextval('nested')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(47)}})
			for _, prepared := range []bool{false, true} {
				exec("BEGIN")
				run(referenceStep{query: "INSERT INTO dst SELECT nextval('s') FROM src WHERE id=99", prepared: prepared})
				// Source-table AS is deliberately retained; B does not touch src/dst.
				peer, stop := context.WithTimeout(ctx, 2*time.Second)
				result, err := b.ExecContext(peer, "ALTER SEQUENCE "+aSchema+".s RESTART WITH 80")
				stop()
				if err != nil {
					t.Fatal("undemanded INSERT blocked peer RESTART", err)
				}
				if n, err := result.RowsAffected(); err != nil || n != 0 {
					t.Fatal(n, err)
				}
				exec("COMMIT")
				rows("SELECT v FROM dst", []string{"v"}, []string{"INT8"}, nil)
				state("s", 80, false)
				rows("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(80)}})
			}
			// Retain the real INSERT statement, not a fresh prepared execution per step.
			for _, q := range []string{"CREATE SEQUENCE " + bSchema + ".s START 10 CACHE 1", "CREATE SEQUENCE " + bSchema + ".t START 20 CACHE 1", "CREATE TABLE " + bSchema + ".dst(id BIGINT,n BIGINT,m BIGINT,day DATE,payload BYTEA,label TEXT)", "SET search_path TO " + aSchema + "," + bSchema} {
				exec(q)
			}
			// Remove the input fixture's s so the initial capture is genuinely b.s.
			exec("DROP SEQUENCE " + aSchema + ".s")
			q := "INSERT INTO " + bSchema + ".dst SELECT $1::text::bigint,nextval('s'),nextval('t'),DATE '2024-02-29',$3,$2::text"
			s, err := c.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			exec("CREATE SEQUENCE " + aSchema + ".s START 100 CACHE 1")
			exec("ALTER TABLE " + bSchema + ".t RENAME TO identity")
			missing := func() {
				_, err := s.ExecContext(ctx, "7", "first", []byte{0, 1})
				want := controlsReferenceDiagnostic("42P01", `relation "t" does not exist`)
				want.Position = strconv.Itoa(strings.Index(q, "'t'") + 1)
				referenceAssertDiagnostic(t, err, &want)
			}
			missing()
			rows("SELECT id FROM "+bSchema+".dst", []string{"id"}, []string{"INT8"}, nil)
			state(aSchema+".s", 100, false)
			state(bSchema+".s", 10, false)
			state(bSchema+".identity", 20, false)
			exec("CREATE SEQUENCE " + aSchema + ".t START 200 CACHE 1")
			insert := func(id, label string) {
				r, err := s.ExecContext(ctx, id, label, []byte{0, 1})
				if err != nil {
					t.Fatal(err)
				}
				if n, err := r.RowsAffected(); err != nil || n != 1 {
					t.Fatal(n, err)
				}
			}
			insert("7", "first")
			exec("SET search_path TO " + bSchema)
			missing()
			state(aSchema+".s", 100, true)
			state(aSchema+".t", 200, true)
			state(bSchema+".s", 10, false)
			exec("SET search_path TO " + aSchema + "," + bSchema)
			insert("8", "second")
			var day any = "2024-02-29"
			if backend.name == "Postgres" {
				day = time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
			}
			rows("SELECT * FROM "+bSchema+".dst ORDER BY id", []string{"id", "n", "m", "day", "payload", "label"}, []string{"INT8", "INT8", "INT8", "DATE", "BYTEA", "TEXT"}, [][]any{{int64(7), int64(100), int64(200), day, "\x00\x01", "first"}, {int64(8), int64(101), int64(201), day, "\x00\x01", "second"}})
			state(bSchema+".s", 10, false)
			state(bSchema+".identity", 20, false)
			state(aSchema+".s", 101, true)
			state(aSchema+".t", 201, true)
		})
	}
}
