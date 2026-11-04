package reference_test

import (
	"database/sql"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

const sepgsqlA = "test_u:test_r:admin_t:s0"
const sepgsqlR = "test_u:test_r:reader_t:s0"
const sepgsqlT = "test_u:test_r:trusted_t:s0"
const sepgsqlP = "test_u:object_r:public_t:s0"
const sepgsqlS = "test_u:object_r:secret_t:s0"
const sepgsqlU = "test_u:object_r:unlabeled_t:s0"

func sepgsqlConfig() stuntgres.SepgsqlSimulationConfig {
	return stuntgres.SepgsqlSimulationConfig{InstallSQL: true, Policy: stuntgres.SepgsqlPolicy{
		ID: "stuntgres-sepgsql-tests", Version: "1", Contexts: []string{sepgsqlA, sepgsqlR, sepgsqlT, sepgsqlP, sepgsqlS, sepgsqlU}, InitialContext: sepgsqlA, UnlabeledContext: sepgsqlU,
		Rules: []stuntgres.SepgsqlRule{
			{Source: sepgsqlA, Target: "*", Class: "*", Allow: []string{"*"}},
			{Source: sepgsqlR, Target: sepgsqlP, Class: "db_database", Allow: []string{"access"}},
			{Source: sepgsqlR, Target: sepgsqlP, Class: "db_schema", Allow: []string{"search"}},
			{Source: sepgsqlR, Target: sepgsqlP, Class: "db_table", Allow: []string{"select", "lock"}},
			{Source: sepgsqlR, Target: sepgsqlP, Class: "db_column", Allow: []string{"select"}},
			{Source: sepgsqlR, Target: sepgsqlP, Class: "db_view", Allow: []string{"expand"}},
			{Source: sepgsqlR, Target: sepgsqlP, Class: "db_procedure", Allow: []string{"execute"}},
			{Source: sepgsqlR, Target: sepgsqlS, Class: "db_procedure", Allow: []string{"execute", "entrypoint"}},
			{Source: sepgsqlR, Target: "*", Class: "process", Allow: []string{"setcurrent", "dyntransition", "transition"}},
			{Source: sepgsqlT, Target: sepgsqlP, Class: "db_database", Allow: []string{"access"}},
			{Source: sepgsqlT, Target: sepgsqlP, Class: "db_schema", Allow: []string{"search"}},
			{Source: sepgsqlT, Target: "*", Class: "db_procedure", Allow: []string{"execute", "entrypoint"}},
			{Source: sepgsqlT, Target: sepgsqlS, Class: "db_table", Allow: []string{"select"}},
			{Source: sepgsqlT, Target: sepgsqlS, Class: "db_column", Allow: []string{"select"}},
		},
		TrustedTransitions: []stuntgres.SepgsqlTrustedTransition{{Source: sepgsqlR, Procedure: sepgsqlS, Result: sepgsqlT}},
		DefaultSpec:        "db_database * " + sepgsqlP + "\ndb_schema * " + sepgsqlP + "\ndb_table * " + sepgsqlP + "\ndb_column * " + sepgsqlP + "\ndb_sequence * " + sepgsqlP + "\ndb_view * " + sepgsqlP + "\ndb_procedure * " + sepgsqlP + "\n",
		NamedSpecs:         map[string]string{"public": "db_table * " + sepgsqlP + "\ndb_column * " + sepgsqlP + "\n"},
		Translations:       []stuntgres.SepgsqlTranslation{{Raw: sepgsqlP, Translated: "Public display label"}},
	}}
}

type sepgsqlRun func(fixture.Step)

// Both modes exercise the same meaningful ordinary/inactive PostgreSQL phase.
// The separate native_simulation subtest is an explicitly source-grounded native
// contract. It never installs fake oracle functions or claims live SELinux parity.
func sepgsqlCase(t *testing.T, ordinary func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, sepgsqlRun), configure func(*stuntgres.SepgsqlSimulationConfig), active func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, sepgsqlRun)) {
	t.Helper()
	t.Run("provider_independent", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, backend := range p.Backends() {
			t.Run(backend.Name, func(t *testing.T) {
				c, err := backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				run := func(step fixture.Step) {
					defer func() {
						if t.Failed() {
							t.Logf("sepgsql statement: %s", step.Query)
						}
					}()
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, step)
				}
				ordinary(t, p, backend, c, run)
			})
		}
	})
	t.Run("native_simulation", func(t *testing.T) {
		config := sepgsqlConfig()
		if configure != nil {
			configure(&config)
		}
		p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error { return d.RegisterSepgsqlSimulation(config) })
		for _, backend := range p.Backends() {
			if backend.PG {
				continue
			}
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(step fixture.Step) {
				defer func() {
					if t.Failed() {
						t.Logf("sepgsql statement: %s", step.Query)
					}
				}()
				fixture.RunStep(t, p.Ctx, c, backend.Recorder, step)
			}
			active(t, p, backend, c, run)
		}
	})
}
func sepgsqlText(query, column, value string) fixture.Step {
	return fixture.Select(query, []string{column}, []string{"TEXT"}, [][]any{{value}})
}
func sepgsqlBool(query, column string, value bool) fixture.Step {
	return fixture.Select(query, []string{column}, []string{"BOOL"}, [][]any{{value}})
}
func sepgsqlCount(query string, value int64) fixture.Step {
	return fixture.Select(query, []string{"count"}, []string{"INT8"}, [][]any{{value}})
}
func sepgsqlSet(context string) fixture.Step {
	return sepgsqlBool("SELECT sepgsql_setcon('"+context+"') AS changed", "changed", true)
}
func sepgsqlDenied(query string) fixture.Step {
	return fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "SELinux: security policy violation"})
}
func sepgsqlTable(run sepgsqlRun) {
	run(fixture.Exec("CREATE TABLE sepgsql_items(id integer,v text)", 0))
	run(fixture.Exec("INSERT INTO sepgsql_items VALUES(1,'one'),(2,'two')", 2))
}
func sepgsqlOrdinaryTable(t *testing.T, p *fixture.Pair, c *sql.Conn, run sepgsqlRun) {
	sepgsqlTable(run)
	run(fixture.Select("SELECT id,v FROM sepgsql_items ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}}))
}
func sepgsqlHostError(t *testing.T, err error) {
	t.Helper()
	var state interface{ SQLState() string }
	if err == nil || errors.As(err, &state) {
		t.Fatal("expected ordinary initialization API error", err)
	}
}
