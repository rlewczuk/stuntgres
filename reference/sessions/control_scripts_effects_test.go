package reference_test

import (
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceControlScriptsStageIsolation(t *testing.T) {
	for _, tc := range []struct{ name, definition, expression, stage string }{
		{"direct try", "", "pg_try_advisory_lock(918273::bigint)", "CREATE TABLE cs_effect(v integer CHECK(pg_try_advisory_lock(918273::bigint))); INSERT INTO cs_effect VALUES(7);"},
		{"direct unlock", "", "pg_advisory_unlock(918273::bigint)", "CREATE TABLE cs_effect(v integer CHECK(pg_advisory_unlock(918273::bigint) IS NOT NULL)); INSERT INTO cs_effect VALUES(7);"},
		{"default", "CREATE FUNCTION cs_effect_fn(integer) RETURNS integer LANGUAGE SQL AS $$SELECT pg_advisory_lock(918273::bigint); SELECT $1$$;", "cs_effect_fn(7)", "CREATE TABLE cs_effect(v integer DEFAULT cs_effect_fn(7)); INSERT INTO cs_effect DEFAULT VALUES;"},
		{"check", "CREATE FUNCTION cs_effect_fn(integer) RETURNS integer LANGUAGE SQL AS $$SELECT pg_try_advisory_lock(918273::bigint); SELECT $1$$;", "cs_effect_fn(7)", "CREATE TABLE cs_effect(v integer CHECK(cs_effect_fn(v)>0)); INSERT INTO cs_effect VALUES(7);"},
		{"setting", "CREATE FUNCTION cs_effect_fn() RETURNS integer LANGUAGE SQL AS $$SELECT set_config('application_name','escaped',false); SELECT 7$$;", "cs_effect_fn()", "CREATE TABLE cs_effect(v integer DEFAULT cs_effect_fn()); INSERT INTO cs_effect DEFAULT VALUES;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					defer c.ExecContext(p.Ctx, "SELECT pg_advisory_unlock_all()")
					defer c.ExecContext(p.Ctx, "RESET application_name")
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					if tc.definition != "" {
						run(fixture.Exec(tc.definition, 0))
					}
					want := any(int64(7))
					typ := "INT4"
					if tc.name == "direct try" || tc.name == "direct unlock" {
						want, typ = true, "BOOL"
					}
					for _, prepared := range []bool{false, true} {
						if tc.name == "direct unlock" {
							run(fixture.Select("SELECT pg_try_advisory_lock(918273::bigint) AS value", []string{"value"}, []string{"BOOL"}, [][]any{{true}}))
						}
						s := fixture.Select("SELECT "+tc.expression+" AS value", []string{"value"}, []string{typ}, [][]any{{want}})
						s.Prepared = prepared
						run(s)
					}
					if b.PG {
						return
					}
					// A different physical target session observes lock ownership.
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					defer peer.ExecContext(p.Ctx, "SELECT pg_advisory_unlock_all()")
					lock := func(x fixture.Executor, q string, want bool) {
						t.Helper()
						fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(q+" AS value", []string{"value"}, []string{"BOOL"}, [][]any{{want}}))
					}
					if _, err := c.ExecContext(p.Ctx, "SELECT pg_advisory_unlock_all()"); err != nil {
						t.Fatal(err)
					}
					run(fixture.Exec("SET application_name='control-target'", 0))
					run(fixture.Exec("CREATE TABLE cs_sentinel(v integer)", 0))
					run(fixture.Exec("INSERT INTO cs_sentinel VALUES(77)", 1))
					lock(c, "SELECT pg_try_advisory_lock(918273::bigint)", true)
					assertIsolation := func() {
						t.Helper()
						controlScriptsRoutes(t, p, c, b.Recorder, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"control-target"}}))
						// Stage unlock must not release the target lock; stage lock
						// acquisition must not leave ownership after stage teardown.
						lock(peer, "SELECT pg_try_advisory_lock(918273::bigint)", false)
						lock(c, "SELECT pg_advisory_unlock(918273::bigint)", true)
						lock(peer, "SELECT pg_try_advisory_lock(918273::bigint)", true)
						lock(peer, "SELECT pg_advisory_unlock(918273::bigint)", true)
						lock(c, "SELECT pg_try_advisory_lock(918273::bigint)", true)
					}
					script := "CREATE TABLE must_not_publish(v integer); " + tc.definition + tc.stage
					err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script+"INSERT INTO cs_effect VALUES(1/0);"))
					fixture.AssertControlDiagnostic(t, err, fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
					controlScriptsRoutes(t, p, c, b.Recorder, fixture.Select("SELECT v FROM cs_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
					run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='must_not_publish'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					assertIsolation()
					if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
						t.Fatal(err)
					}
					controlScriptsRoutes(t, p, c, b.Recorder, fixture.Select("SELECT v FROM cs_effect", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
					run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='cs_sentinel'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					assertIsolation()
				})
			}
		})
	}
}

func TestReferenceControlScriptsOwnedEffects(t *testing.T) {
	p := fixture.NewScenario(t)
	definition := "CREATE TABLE cs_log(v integer); CREATE SEQUENCE cs_ticks; CREATE FUNCTION cs_local(integer) RETURNS integer LANGUAGE SQL AS $$INSERT INTO cs_log VALUES($1); SELECT nextval('cs_ticks'); SELECT $1+1$$; CREATE TABLE cs_owned(v integer DEFAULT cs_local(4));"
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			// This SQL witness executes the same stored default as native replay.
			if _, err := b.DB.ExecContext(p.Ctx, definition); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO cs_owned DEFAULT VALUES", 1))
			controlScriptsRoutes(t, p, b.DB, b.Recorder, fixture.Select("SELECT v FROM cs_owned", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}))
			controlScriptsRoutes(t, p, b.DB, b.Recorder, fixture.Select("SELECT v FROM cs_log", []string{"v"}, []string{"INT4"}, [][]any{{int64(4)}}))
			if b.PG {
				return
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(definition+"INSERT INTO cs_owned DEFAULT VALUES; CREATE INDEX cs_owned_idx ON cs_owned((v+1));")); err != nil {
				t.Fatal(err)
			}
			controlScriptsRoutes(t, p, b.DB, b.Recorder, fixture.Select("SELECT v FROM cs_owned", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}))
			controlScriptsRoutes(t, p, b.DB, b.Recorder, fixture.Select("SELECT v FROM cs_log", []string{"v"}, []string{"INT4"}, [][]any{{int64(4)}}))
			controlScriptsRoutes(t, p, b.DB, b.Recorder, fixture.Select("SELECT last_value FROM cs_ticks", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(1)}}))
		})
	}
}
