package reference_test

import (
	"database/sql"
	"slices"
	"strings"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const (
	sepgsqlInitCall   = "test_u:object_r:initialization_call_t:s0"
	sepgsqlInitDenied = "test_u:object_r:initialization_denied_t:s0"
	sepgsqlInitStrict = "test_u:object_r:initialization_strict_t:s0"
	sepgsqlInitInner  = "test_u:object_r:initialization_inner_t:s0"
)

type sepgsqlInitializationAudit struct {
	mu     sync.Mutex
	events []stuntgres.SepgsqlAuditEvent
}

func (a *sepgsqlInitializationAudit) record(event stuntgres.SepgsqlAuditEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, event)
}

func (a *sepgsqlInitializationAudit) reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = nil
}

func (a *sepgsqlInitializationAudit) snapshot() []stuntgres.SepgsqlAuditEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.events)
}

func sepgsqlInitializationConfig(a *sepgsqlInitializationAudit) stuntgres.SepgsqlSimulationConfig {
	config := sepgsqlConfig()
	config.AuditAllowed, config.Audit = true, a.record
	config.Policy.Contexts = append(config.Policy.Contexts, sepgsqlInitCall, sepgsqlInitDenied, sepgsqlInitStrict, sepgsqlInitInner)
	config.Policy.Rules = append(config.Policy.Rules,
		stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlInitCall, Class: "db_procedure", Allow: []string{"execute"}},
		stuntgres.SepgsqlRule{Source: sepgsqlT, Target: sepgsqlInitCall, Class: "db_procedure", Deny: []string{"execute"}},
		stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlInitStrict, Class: "db_procedure", Allow: []string{"execute"}},
		stuntgres.SepgsqlRule{Source: sepgsqlT, Target: "*", Class: "process", Allow: []string{"setcurrent", "dyntransition", "transition"}},
		stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_table", Allow: []string{"insert", "update"}},
		stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_column", Allow: []string{"insert", "update"}},
		stuntgres.SepgsqlRule{Source: sepgsqlT, Target: sepgsqlP, Class: "db_table", Allow: []string{"select", "insert", "update"}},
		stuntgres.SepgsqlRule{Source: sepgsqlT, Target: sepgsqlP, Class: "db_column", Allow: []string{"select", "insert", "update"}},
	)
	// Execute alone permits strict-NULL initialization, but actual non-NULL
	// entry must fail: R has no entrypoint permission for this label.
	config.Policy.TrustedTransitions = append(config.Policy.TrustedTransitions,
		stuntgres.SepgsqlTrustedTransition{Source: sepgsqlR, Procedure: sepgsqlInitStrict, Result: sepgsqlT},
	)
	return config
}

// The ordinary phase is genuine inactive PostgreSQL 18.6 SQL. The active phase
// has independent, REL_18_6 execExpr.c/ExecInitFunc and fmgr.c expectations; it
// does not claim an active SELinux oracle, optimizer parity, or stock AVC counts.
func TestReferenceSepgsqlSimulationInitialization(t *testing.T) {
	t.Run("provider_independent", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, backend := range p.Backends() {
			t.Run(backend.Name, func(t *testing.T) {
				c, err := backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				sepgsqlInitializationOrdinary(t, p, backend, c)
				sepgsqlInitializationAggregateBodies(t, p, backend, c, nil)
			})
		}
	})
	t.Run("native_simulation", func(t *testing.T) {
		var audit sepgsqlInitializationAudit
		config := sepgsqlInitializationConfig(&audit)
		p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
			return d.RegisterSepgsqlSimulation(config)
		})
		for _, backend := range p.Backends() {
			// Ordinary PostgreSQL is deliberately inactive, not a simulated
			// active provider with replacement SQL functions or fabricated grants.
			if backend.PG {
				continue
			}
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			sepgsqlInitializationActive(t, p, backend, c, &audit)
		}
	})
}

func sepgsqlInitializationLabel(run sepgsqlRun, signature, label string) {
	run(fixture.Exec("SECURITY LABEL ON FUNCTION "+signature+" IS '"+label+"'", 0))
}

func sepgsqlInitializationActive(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, audit *sepgsqlInitializationAudit) {
	run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
	admin := func() { run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true)) }
	for _, name := range []string{"init_echo", "init_denied", "init_strict"} {
		run(fixture.Exec("CREATE FUNCTION "+name+"(text) RETURNS text AS '$libdir/sepgsql','sepgsql_mcstrans_out' LANGUAGE C VOLATILE STRICT", 0))
	}
	sepgsqlInitializationLabel(run, "init_echo(text)", sepgsqlInitCall)
	sepgsqlInitializationLabel(run, "init_denied(text)", sepgsqlInitDenied)
	sepgsqlInitializationLabel(run, "init_strict(text)", sepgsqlInitStrict)
	run(fixture.Exec("CREATE TABLE init_inputs(id integer,v text,n numeric)", 0))
	run(fixture.Exec("INSERT INTO init_inputs VALUES(1,'alpha',3.14),(2,'beta',NULL),(3,'gamma',5.6)", 3))

	run(fixture.Exec("CREATE TABLE init_null_input(v text)", 0))
	run(fixture.Exec("INSERT INTO init_null_input VALUES(NULL)", 1))
	for _, prepared := range []bool{false, true} {
		run(sepgsqlSet(sepgsqlR))
		audit.reset()
		step := sepgsqlText("SELECT init_echo(CASE WHEN sepgsql_setcon('"+sepgsqlT+"') THEN 'argument' ELSE 'unreachable' END) AS value", "value", "argument")
		step.Prepared = prepared
		step.CheckMetadata = sepgsqlInitializationTextMetadata()
		run(step)
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlT))
		var initialized bool
		for _, event := range audit.snapshot() {
			if event.Class == "db_procedure" && event.Target == sepgsqlInitCall && slices.Contains(event.Required, "execute") {
				if event.Subject != sepgsqlR || len(event.Denied) != 0 {
					t.Fatalf("postargument execute replay: %#v", event)
				}
				initialized = true
			}
		}
		if !initialized {
			t.Fatal("missing original-subject initialization")
		}
		admin()
		run(sepgsqlSet(sepgsqlR))
		audit.reset()
		denied := sepgsqlDenied("SELECT init_denied(CASE WHEN sepgsql_setcon('" + sepgsqlT + "') THEN 'argument' ELSE 'unreachable' END)")
		denied.Prepared, denied.SelectRows = prepared, true
		run(denied)
		// SQL effects alone could be rolled back after a late denial. The
		// actual process audit proves the argument was never evaluated.
		for _, event := range audit.snapshot() {
			if event.Class == "process" {
				t.Fatalf("argument ran before original execute denial: %#v", event)
			}
		}
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		admin()

		run(sepgsqlSet(sepgsqlR))
		audit.reset()
		null := fixture.Select("SELECT init_strict(v) AS value FROM init_null_input", []string{"value"}, []string{"TEXT"}, [][]any{{nil}})
		null.Prepared = prepared
		run(null)
		var execute bool
		for _, event := range audit.snapshot() {
			if event.Target != sepgsqlInitStrict || event.Class != "db_procedure" {
				continue
			}
			if slices.Contains(event.Required, "entrypoint") {
				t.Fatalf("strict NULL entered FHET: %#v", event)
			}
			if slices.Contains(event.Required, "execute") {
				execute = true
			}
		}
		if !execute {
			t.Fatal("strict NULL lost retained execute initialization")
		}
		denied = sepgsqlDenied("SELECT init_strict($1::text) AS value")
		denied.Prepared, denied.SelectRows, denied.Args = prepared, true, []any{"nonnull"}
		run(denied)
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		admin()
	}

	const heldQuery = "SELECT init_echo($1::text) AS value"
	held := fixture.IdentityHeld(t, p.Ctx, c, heldQuery)
	for _, value := range []string{"first", "second"} {
		run(sepgsqlSet(sepgsqlR))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(heldQuery, []string{"value"}, []string{"TEXT"}, [][]any{{value}}, value))
		admin()
	}
	// Same prepared program, different genuine execution and subject: no
	// prior R receipt may authorize this T execution.
	run(sepgsqlSet(sepgsqlT))
	denied := sepgsqlDenied(heldQuery)
	denied.SelectRows, denied.Args = true, []any{"third"}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
	admin()
	run(sepgsqlSet(sepgsqlR))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(heldQuery, []string{"value"}, []string{"TEXT"}, [][]any{{"fresh"}}, "fresh"))
	admin()

	// A single retained callsite must keep its owner across actual row pulls.
	run(sepgsqlSet(sepgsqlR))
	for _, prepared := range []bool{false, true} {
		step := fixture.Select("SELECT id,init_echo(v) AS value FROM init_inputs ORDER BY id", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "alpha"}, {int64(2), "beta"}, {int64(3), "gamma"}})
		step.Prepared = prepared
		run(step)
	}
	admin()
	run(fixture.Exec("CREATE EXTENSION hstore", 0))
	sepgsqlInitializationLabel(run, "each(hstore)", sepgsqlS)
	run(sepgsqlSet(sepgsqlR))
	vpc := fixture.Select(`SELECT key,value,sepgsql_getcon() AS subject FROM each('a=>one,b=>NULL'::hstore)`, []string{"key", "value", "subject"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"a", "one", sepgsqlR}, {"b", nil, sepgsqlR}})
	vpc.Bag = true
	for _, prepared := range []bool{false, true} {
		audit.reset()
		vpc.Prepared = prepared
		run(vpc)
		initializations := 0
		for _, event := range audit.snapshot() {
			if event.Subject == sepgsqlR && event.Class == "db_procedure" && strings.HasSuffix(event.Name, ".each") && slices.Contains(event.Required, "execute") {
				initializations++
			}
		}
		// This is an explicit native owner invariant, not stock AVC multiplicity.
		if initializations != 1 {
			t.Fatalf("VPC initialization repeated across pulls: %d", initializations)
		}
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
	}
	admin()

	sepgsqlInitializationBodies(t, p, b, c)
	sepgsqlInitializationAggregateBodies(t, p, b, c, audit)
	sepgsqlInitializationAttachments(t, p, b, c, audit)
	sepgsqlInitializationConditional(t, p, b, c)
}

// The ordinary definitions are supported released SQL, not simulated security.
// Only the native active phase examines trusted-entry/current-subject evidence.
func sepgsqlInitializationAggregateBodies(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, audit *sepgsqlInitializationAudit) {
	run := func(step fixture.Step) {
		defer func() {
			if t.Failed() {
				t.Logf("sepgsql aggregate body statement (prepared=%t): %s", step.Prepared, step.Query)
			}
		}()
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
	}
	for _, step := range []fixture.Step{
		fixture.Exec("CREATE TABLE init_aggregate_body(n numeric,v integer)", 0),
		fixture.Exec("INSERT INTO init_aggregate_body VALUES(1.2,3),(2.3,3)", 2),
		fixture.Exec("CREATE AGGREGATE init_aggregate_round(numeric) (SFUNC=pg_catalog.numeric_add,STYPE=numeric,INITCOND='0',FINALFUNC=pg_catalog.round)", 0),
		fixture.Exec("CREATE AGGREGATE init_aggregate_pad(integer) (SFUNC=pg_catalog.lpad,STYPE=text,INITCOND='x')", 0),
	} {
		run(step)
	}
	for _, prepared := range []bool{false, true} {
		for _, step := range []fixture.Step{
			fixture.Select("SELECT init_aggregate_round(n) AS value FROM init_aggregate_body", []string{"value"}, []string{"NUMERIC"}, [][]any{{"4"}}),
			fixture.Select("SELECT init_aggregate_round(n) AS value FROM init_aggregate_body WHERE false", []string{"value"}, []string{"NUMERIC"}, [][]any{{"0"}}),
			sepgsqlText("SELECT init_aggregate_pad(v) AS value FROM init_aggregate_body", "value", "  x"),
			sepgsqlText("SELECT init_aggregate_pad(v) AS value FROM init_aggregate_body WHERE false", "value", "x"),
			sepgsqlText("SELECT init_aggregate_pad(NULL::integer) AS value FROM init_aggregate_body", "value", "x"),
		} {
			step.Prepared = prepared
			run(step)
		}
	}
	if audit == nil {
		return
	}
	for _, body := range []struct {
		query, outer, inner, name string
		recovery                  fixture.Step
	}{
		{
			"SELECT init_aggregate_round(n) AS value FROM init_aggregate_body",
			"pg_catalog.round(numeric)", "pg_catalog.round(numeric,integer)", "round",
			fixture.Select("SELECT init_aggregate_round(n) AS value FROM init_aggregate_body", []string{"value"}, []string{"NUMERIC"}, [][]any{{"4"}}),
		},
		{
			"SELECT init_aggregate_pad(v) AS value FROM init_aggregate_body",
			"pg_catalog.lpad(text,integer)", "pg_catalog.lpad(text,integer,text)", "lpad",
			sepgsqlText("SELECT init_aggregate_pad(v) AS value FROM init_aggregate_body", "value", "  x"),
		},
	} {
		sepgsqlInitializationLabel(run, body.outer, sepgsqlS)
		sepgsqlInitializationLabel(run, body.inner, sepgsqlInitCall)
		for _, prepared := range []bool{false, true} {
			run(sepgsqlSet(sepgsqlR))
			audit.reset()
			failure := sepgsqlDenied(body.query)
			failure.Prepared, failure.SelectRows = prepared, true
			failure.Diagnostic.Context = "SQL function \"" + body.name + "\" statement 1"
			run(failure)
			entered, denied := false, false
			for _, event := range audit.snapshot() {
				if event.Class != "db_procedure" {
					continue
				}
				if event.Subject == sepgsqlR && event.Target == sepgsqlS && strings.HasSuffix(event.Name, "."+body.name) && slices.Contains(event.Required, "entrypoint") && len(event.Denied) == 0 {
					entered = true
				}
				if event.Subject == sepgsqlT && event.Target == sepgsqlInitCall && strings.HasSuffix(event.Name, "."+body.name) && slices.Contains(event.Denied, "execute") {
					denied = true
				}
			}
			if !entered || !denied {
				t.Fatal("aggregate wrapper did not initialize its child under the genuinely entered subject", entered, denied, audit.snapshot())
			}
			run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			sepgsqlInitializationLabel(run, body.inner, sepgsqlP)
			run(sepgsqlSet(sepgsqlR))
			recovery := body.recovery
			recovery.Prepared = prepared
			run(recovery)
			run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			sepgsqlInitializationLabel(run, body.inner, sepgsqlInitCall)
		}
		sepgsqlInitializationLabel(run, body.outer, sepgsqlP)
		sepgsqlInitializationLabel(run, body.inner, sepgsqlP)
	}
}
