package reference_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSequenceProducerRetainedGroup(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
			defer cancel()
			c := fixture.SequenceCoreConn(t, ctx, backend.DB)
			aSchema := fmt.Sprintf("producer_boundary_a_%d", time.Now().UnixNano())
			bSchema := aSchema + "_b"
			t.Cleanup(func() {
				cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
				defer stop()
				_, _ = c.ExecContext(cleanup, "ROLLBACK")
				_, _ = c.ExecContext(cleanup, "SET search_path TO public")
				for _, schema := range []string{aSchema, bSchema} {
					_, _ = c.ExecContext(cleanup, "DROP SCHEMA "+schema+" CASCADE")
				}
			})
			run := func(step fixture.Step) { fixture.RunStep(t, ctx, c, backend.Recorder, step) }
			exec := func(q string) { run(fixture.Step{Query: q}) }
			rows := func(q string, columns, types []string, want [][]any) {
				run(fixture.Step{Query: q, SelectRows: true, Columns: columns, Types: types, Rows: want})
			}
			state := func(name string, value int64, called bool) {
				rows("SELECT last_value,is_called FROM "+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{value, called}})
			}
			exec("CREATE SCHEMA " + aSchema)
			exec("CREATE SCHEMA " + bSchema)
			// Retain the real INSERT statement, not a fresh prepared execution per step.
			for _, q := range []string{"CREATE SEQUENCE " + bSchema + ".s START 10 CACHE 1", "CREATE SEQUENCE " + bSchema + ".t START 20 CACHE 1", "CREATE TABLE " + bSchema + ".dst(id BIGINT,n BIGINT,m BIGINT,day DATE,payload BYTEA,label TEXT)", "SET search_path TO " + aSchema + "," + bSchema} {
				exec(q)
			}
			q := "INSERT INTO " + bSchema + ".dst SELECT $1::text::bigint,nextval('s'),nextval('t'),DATE '2024-02-29',$3,$2::text"
			s, err := c.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			exec("CREATE SEQUENCE " + aSchema + ".s START 100 CACHE 1")
			exec("ALTER TABLE " + bSchema + ".t RENAME TO identity")
			missing := func() {
				want := fixture.ControlsReferenceDiagnostic("42P01", `relation "t" does not exist`)
				want.Position = strconv.Itoa(strings.Index(q, "'t'") + 1)
				fixture.RunStep(t, ctx, fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, backend.Recorder, fixture.Step{Query: q, Args: []any{"7", "first", []byte{0, 1}}, State: want.Code, Diagnostic: &want, ErrorStage: "execute"})
			}
			missing()
			rows("SELECT id FROM "+bSchema+".dst", []string{"id"}, []string{"INT8"}, nil)
			state(aSchema+".s", 100, false)
			state(bSchema+".s", 10, false)
			state(bSchema+".identity", 20, false)
			exec("CREATE SEQUENCE " + aSchema + ".t START 200 CACHE 1")
			insert := func(id, label string) {
				fixture.RunStep(t, ctx, fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, backend.Recorder, fixture.Step{Query: q, Args: []any{id, label, []byte{0, 1}}, Affected: 1})
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
			if backend.Name == "postgres" {
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
