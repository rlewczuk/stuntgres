package reference_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRLSBaseControlsReplay(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"rls_replay_owner", "rls_replay_other"})
			p := fixture.NewScenario(t)
			for _, role := range []string{"rls_replay_owner", "rls_replay_other"} {
				p.Step(t, fixture.Exec("CREATE ROLE "+role+" LOGIN NOSUPERUSER NOBYPASSRLS", 0))
			}
			c, err := p.Memory.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run := func(step fixture.Step) { step.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, p.MemoryRecord, step) }
			exec := func(q string) { run(fixture.Exec(q, 0)) }
			exec("SET SESSION AUTHORIZATION rls_replay_owner")
			for _, state := range []struct {
				name            string
				enabled, forced bool
			}{{"rls_off", false, false}, {"rls_force_off", false, true}, {"rls_on", true, false}, {"rls_force_on", true, true}} {
				fixture.WithPublicSchemaOwner(t, p, fixture.Backend{Name: "memory", DB: p.Memory, Recorder: p.MemoryRecord}, "rls_replay_owner", func() { exec("CREATE TABLE " + state.name + "(id INT)") })
				run(fixture.Exec("INSERT INTO "+state.name+" VALUES(7)", 1))
				exec("CREATE POLICY retained ON " + state.name + " TO rls_replay_other USING(id>0) WITH CHECK(id>0)")
				if state.enabled {
					exec("ALTER TABLE " + state.name + " ENABLE ROW LEVEL SECURITY")
				}
				if state.forced {
					exec("ALTER TABLE " + state.name + " FORCE ROW LEVEL SECURITY")
				}
			}
			var stream bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &stream); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stream.String(), "CREATE ROLE") {
				t.Fatal("Dump serialized shared roles")
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(stream.Bytes())); err != nil {
				t.Fatal(err)
			}
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(stream.Bytes(), again.Bytes()) {
				t.Fatal("unstable flag/policy/raw-row roundtrip", err)
			}
			if p.Postgres != nil {
				oracle, err := p.Postgres.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer oracle.Close()
				defer oracle.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				// These are the exact emitted bytes, not reconstructed ALTER SQL.
				fixture.WithPublicSchemaOwner(t, p, fixture.Backend{Name: "postgres", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}, "rls_replay_owner", func() {
					if _, err := oracle.ExecContext(p.Ctx, stream.String()); err != nil {
						t.Fatal("pinned replay of native flag stream", err)
					}
				})
			}
			for _, b := range p.Backends() {
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer conn.Close()
					defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
					step := func(v fixture.Step) { v.Prepared = prepared; fixture.RunStep(t, p.Ctx, conn, b.Recorder, v) }
					step(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					for _, name := range []string{"rls_off", "rls_force_off", "rls_on", "rls_force_on"} {
						step(fixture.Select("SELECT id FROM "+name, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					}
					step(fixture.Exec("SET SESSION AUTHORIZATION rls_replay_owner", 0))
					step(fixture.Select("SELECT id FROM rls_force_on", []string{"id"}, []string{"INT4"}, nil))
					step(fixture.Select("SELECT id FROM rls_on", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					step(fixture.Exec("ALTER TABLE rls_force_off ENABLE ROW LEVEL SECURITY", 0))
					step(fixture.Select("SELECT id FROM rls_force_off", []string{"id"}, []string{"INT4"}, nil))
					step(fixture.Exec("ALTER POLICY retained ON rls_force_on RENAME TO survived", 0))
				}()
			}
		})
	}
}
