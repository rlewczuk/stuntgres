package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSmokeSequenceProducerBoundarySmoke(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "a", "b")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
			defer cancel()
			c := fixture.SequenceCoreConn(t, ctx, backend.DB)
			b := fixture.SequenceCoreConn(t, ctx, backend.DB)
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run := func(step fixture.Step) { fixture.RunStep(t, ctx, c, backend.Recorder, step) }
			exec := func(q string, n int64) { run(fixture.Exec(q, n)) }
			rows := func(q string, names, types []string, want [][]any) { run(fixture.Select(q, names, types, want)) }
			state := func(name string, v int64, called bool) {
				rows("SELECT last_value,is_called FROM "+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{v, called}})
			}
			for _, q := range []string{"CREATE SEQUENCE s START 31 CACHE 1", "CREATE SEQUENCE nested START 47 CACHE 1", "CREATE TABLE empty_src(id INT)", "CREATE TABLE src(id INT)", "CREATE TABLE dst(v BIGINT)"} {
				exec(q, 0)
			}
			exec("INSERT INTO src VALUES(1)", 1)
			bad := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type bigint: "bad"`)
			missingInput := fixture.ControlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
			missingInput.Context = "unnamed portal parameter $1 = '...'"
			for _, prepared := range []bool{false, true} {
				for _, source := range []string{"empty_src", "src WHERE id=99", "src"} {
					for _, tc := range []struct {
						q    string
						arg  any
						want fixture.Diagnostic
					}{
						{"INSERT INTO dst SELECT setval('s',$1::text::bigint) FROM " + source, "bad", bad},
						{"INSERT INTO dst SELECT setval($1,nextval('nested')) FROM " + source, "missing", missingInput},
					} {
						step := fixture.ErrorDiagnostic(tc.q, tc.want, tc.arg)
						step.Prepared, step.ErrorStage = prepared, "execute"
						run(step)
						rows("SELECT v FROM dst", []string{"v"}, []string{"INT8"}, nil)
						state("s", 31, false)
						state("nested", 47, false)
						for _, name := range []string{"s", "nested"} {
							want := fixture.ControlsReferenceDiagnostic("55000", `currval of sequence "`+name+`" is not yet defined in this session`)
							step := fixture.ErrorDiagnostic("SELECT currval('"+name+"')", want)
							step.SelectRows, step.ErrorStage = true, "execute"
							run(step)
						}
						want := fixture.ControlsReferenceDiagnostic("55000", "lastval is not yet defined in this session")
						step = fixture.ErrorDiagnostic("SELECT lastval()", want)
						step.SelectRows, step.ErrorStage = true, "execute"
						run(step)
					}
				}
			}
			run(fixture.SequenceInt64Step(fixture.Select("SELECT nextval('nested')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(47)}})))
			for _, prepared := range []bool{false, true} {
				exec("BEGIN", 0)
				step := fixture.Exec("INSERT INTO dst SELECT nextval('s') FROM src WHERE id=99", 0)
				step.Prepared = prepared
				run(step)
				// The source-table AS lock remains with A; the peer touches only s.
				peer, stop := context.WithTimeout(ctx, 2*time.Second)
				defer stop()
				fixture.RunStep(t, peer, b, backend.Recorder, fixture.Exec("ALTER SEQUENCE s RESTART WITH 80", 0))
				stop()
				exec("COMMIT", 0)
				rows("SELECT v FROM dst", []string{"v"}, []string{"INT8"}, nil)
				state("s", 80, false)
				run(fixture.SequenceInt64Step(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(80)}})))
			}
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10 CACHE 1", "CREATE SEQUENCE b.t START 20 CACHE 1", "CREATE TABLE b.dst(id BIGINT,n BIGINT,m BIGINT,day DATE,payload BYTEA,label TEXT)", "SET search_path TO a,b"} {
				exec(q, 0)
			}
			q := "INSERT INTO b.dst SELECT $1::text::bigint,nextval('s'),nextval('t'),DATE '2024-02-29',$3,$2::text"
			s, err := c.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			held := fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
			exec("CREATE SEQUENCE a.s START 100 CACHE 1", 0)
			exec("ALTER TABLE b.t RENAME TO identity", 0)
			missing := func() {
				want := fixture.ControlsReferenceDiagnostic("42P01", `relation "t" does not exist`)
				want.Position = strconv.Itoa(strings.Index(q, "'t'") + 1)
				step := fixture.ErrorDiagnostic(q, want, "7", "first", []byte{0, 1})
				step.ErrorStage = "execute"
				fixture.RunStep(t, ctx, held, backend.Recorder, step)
			}
			missing()
			rows("SELECT id FROM b.dst", []string{"id"}, []string{"INT8"}, nil)
			state("a.s", 100, false)
			state("b.s", 10, false)
			state("b.identity", 20, false)
			exec("CREATE SEQUENCE a.t START 200 CACHE 1", 0)
			insert := func(id, label string) {
				fixture.RunStep(t, ctx, held, backend.Recorder, fixture.Exec(q, 1, id, label, []byte{0, 1}))
			}
			insert("7", "first")
			exec("SET search_path TO b", 0)
			missing()
			state("a.s", 100, true)
			state("a.t", 200, true)
			state("b.s", 10, false)
			exec("SET search_path TO a,b", 0)
			insert("8", "second")
			var day any = "2024-02-29"
			if backend.PG {
				day = time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
			}
			step := fixture.Select("SELECT * FROM b.dst ORDER BY id", []string{"id", "n", "m", "day", "payload", "label"}, []string{"INT8", "INT8", "INT8", "DATE", "BYTEA", "TEXT"}, [][]any{{int64(7), int64(100), int64(200), day, "\x00\x01", "first"}, {int64(8), int64(101), int64(201), day, "\x00\x01", "second"}})
			var retained [2][]byte
			step.CheckScan = func(t *testing.T, rows *sql.Rows, i int) {
				var id, n, m int64
				var date any
				var payload []byte
				var label string
				if err := rows.Scan(&id, &n, &m, &date, &payload, &label); err != nil {
					t.Fatal(err)
				}
				if id != int64(7+i) || n != int64(100+i) || m != int64(200+i) || !smokeSequenceProducerDateEqual(date, day) || !bytes.Equal(payload, []byte{0, 1}) || label != []string{"first", "second"}[i] {
					t.Fatal(id, n, m, date, payload, label)
				}
				retained[i] = payload
			}
			run(step)
			if !bytes.Equal(retained[0], []byte{0, 1}) || !bytes.Equal(retained[1], []byte{0, 1}) {
				t.Fatal("BYTEA scans changed after cursor advance/close", retained)
			}
			retained[0][0] = 9
			if retained[1][0] != 0 {
				t.Fatal("BYTEA scans share storage")
			}
			state("a.s", 101, true)
			state("a.t", 201, true)
			state("b.s", 10, false)
			state("b.identity", 20, false)
		})
	}
}
