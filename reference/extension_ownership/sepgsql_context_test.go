package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Each route uses the fixture's complete row/type/diagnostic assertions. Held
// statements are reused for different parameter frames, not freshly prepared.
func sepgsqlContextRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, steps ...fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		for _, step := range steps {
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		}
	}
	statements := map[string]*sql.Stmt{}
	defer func() {
		for _, statement := range statements {
			statement.Close()
		}
	}()
	for _, step := range steps {
		statement := statements[step.Query]
		if statement == nil {
			var err error
			statement, err = c.PrepareContext(p.Ctx, step.Query)
			if err != nil {
				t.Fatal(err)
			}
			statements[step.Query] = statement
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: statement, SQL: step.Query}, b.Recorder, step)
	}
}

func sepgsqlContextValue(query string, value any, args ...any) fixture.Step {
	return fixture.Select(query, []string{"value"}, []string{"TEXT"}, [][]any{{value}}, args...)
}

func sepgsqlContextInvalid(query, input string, args ...any) fixture.Step {
	return fixture.ImplicitError(query, fixture.Diagnostic{Code: "42602", Message: fmt.Sprintf("SELinux: invalid security label: %q", input)}, args...)
}

func sepgsqlContextOrdinaryFunction(run sepgsqlRun) {
	run(fixture.Exec("CREATE FUNCTION context_identity(value text) RETURNS text LANGUAGE sql VOLATILE STRICT AS 'SELECT value'", 0))
	run(fixture.Exec("CREATE TABLE context_inputs(v text)", 0))
	run(fixture.Exec("INSERT INTO context_inputs VALUES(NULL)", 1))
}

func sepgsqlContextOrdinaryFunctions(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
	sepgsqlContextOrdinaryFunction(run)
	sepgsqlContextRoutes(t, p, b, c,
		sepgsqlContextValue("SELECT context_identity($1::text) AS value", "text", "text"),
		sepgsqlContextValue("SELECT context_identity($1::text) AS value", nil, nil),
		sepgsqlContextValue("SELECT context_identity(v) AS value FROM context_inputs", nil),
		sepgsqlContextValue("SELECT context_identity('7'::text) AS value", "7"))
	run(fixture.Select("SELECT proargtypes::text AS arguments,prorettype='text'::regtype AS text_result,proisstrict,proretset,provolatile::text AS volatility,proparallel::text AS parallel FROM pg_proc WHERE oid='context_identity(text)'::regprocedure ORDER BY proname",
		[]string{"arguments", "text_result", "proisstrict", "proretset", "volatility", "parallel"}, []string{"TEXT", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT"}, [][]any{{"25", true, true, false, "v", "u"}}))
}

func sepgsqlContextCatalog(run sepgsqlRun, renamed bool) {
	prefix := "sepgsql_"
	if renamed {
		prefix = "context_"
	}
	rows := [][]any{}
	for _, member := range []struct {
		name, args, result string
		strict             bool
	}{{"getcon", "", "text", false}, {"mcstrans_in", "25", "text", true}, {"mcstrans_out", "25", "text", true}, {"setcon", "25", "bool", false}} {
		rows = append(rows, []any{prefix + member.name, member.args, member.result, member.strict, false, "v", "u", "f", "c", "$libdir/sepgsql", "sepgsql_" + member.name})
	}
	query := "SELECT p.proname::text AS routine,p.proargtypes::text AS arguments,t.typname::text AS result,p.proisstrict,p.proretset,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prokind::text AS kind,l.lanname::text AS language,p.probin,p.prosrc FROM pg_proc p JOIN pg_type t ON t.oid=p.prorettype JOIN pg_language l ON l.oid=p.prolang WHERE p.pronamespace='pg_catalog'::regnamespace AND p.proname IN ('" + prefix + "getcon','" + prefix + "setcon','" + prefix + "mcstrans_in','" + prefix + "mcstrans_out') ORDER BY p.proname"
	run(fixture.Select(query, []string{"routine", "arguments", "result", "proisstrict", "proretset", "volatility", "parallel", "kind", "language", "probin", "prosrc"}, []string{"TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, rows))
}

func sepgsqlContextConversions(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	steps := []fixture.Step{
		sepgsqlContextValue("SELECT sepgsql_mcstrans_out('"+sepgsqlP+"') AS value", "Public display label"),
		sepgsqlContextValue("SELECT sepgsql_mcstrans_in('Public display label') AS value", sepgsqlP),
		sepgsqlContextValue("SELECT sepgsql_mcstrans_in(sepgsql_mcstrans_out($1::text)) AS value", sepgsqlP, sepgsqlP),
		sepgsqlContextValue("SELECT sepgsql_mcstrans_out(sepgsql_mcstrans_in($1::text)) AS value", "Public display label", "Public display label"),
	}
	for _, name := range []string{"in", "out"} {
		query := "SELECT sepgsql_mcstrans_" + name + "($1::text) AS value"
		for _, value := range []any{nil, "unmapped", "", " "} {
			steps = append(steps, sepgsqlContextValue(query, value, value))
		}
		steps = append(steps, sepgsqlContextValue("SELECT sepgsql_mcstrans_"+name+"(NULL::text) AS value", nil))
		steps = append(steps, sepgsqlContextValue("SELECT sepgsql_mcstrans_"+name+"(7::text) AS value", "7"))
	}
	sepgsqlContextRoutes(t, p, b, c, steps...)
}

func TestReferenceSepgsqlSimulationContextFunctions(t *testing.T) {
	sepgsqlCase(t, sepgsqlContextOrdinaryFunctions, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		sepgsqlContextCatalog(run, false)
		sepgsqlContextRoutes(t, p, b, c, sepgsqlContextValue("SELECT sepgsql_getcon() AS value", sepgsqlA))
		steps := []fixture.Step{
			fixture.Select("SELECT sepgsql_setcon($1::text) AS changed", []string{"changed"}, []string{"BOOL"}, [][]any{{true}}, sepgsqlR),
			sepgsqlContextValue("SELECT sepgsql_getcon() AS value", sepgsqlR),
		}
		for _, input := range []string{"unknown", "", " "} {
			steps = append(steps, sepgsqlContextInvalid("SELECT sepgsql_setcon($1::text)", input, input), sepgsqlContextValue("SELECT sepgsql_getcon() AS value", sepgsqlR))
		}
		steps = append(steps, fixture.Select("SELECT sepgsql_setcon($1::text) AS changed", []string{"changed"}, []string{"BOOL"}, [][]any{{true}}, nil), sepgsqlContextValue("SELECT sepgsql_getcon() AS value", sepgsqlA))
		sepgsqlContextRoutes(t, p, b, c, steps...)
		for _, cast := range []string{"varchar", "character(" + fmt.Sprint(len(sepgsqlR)) + ")"} {
			sepgsqlContextRoutes(t, p, b, c, sepgsqlBool("SELECT sepgsql_setcon('"+sepgsqlR+"'::"+cast+") AS changed", "changed", true))
		}
		sepgsqlContextConversions(t, p, b, c)
		for _, prepared := range []bool{false, true} {
			for _, query := range []string{"SELECT sepgsql_getcon(1)", "SELECT sepgsql_setcon()", "SELECT sepgsql_setcon(7)", "SELECT sepgsql_mcstrans_in(7)", "SELECT sepgsql_mcstrans_out()"} {
				name := query[len("SELECT "):]
				// Normal resolver diagnostics, not simulated provider errors.
				signature := name
				if name == "sepgsql_getcon(1)" {
					signature = "sepgsql_getcon(integer)"
				} else if name == "sepgsql_setcon(7)" {
					signature = "sepgsql_setcon(integer)"
				} else if name == "sepgsql_mcstrans_in(7)" {
					signature = "sepgsql_mcstrans_in(integer)"
				}
				step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42883", Message: "function " + signature + " does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})
				step.Prepared = prepared
				run(step)
			}
		}
		held, err := c.PrepareContext(p.Ctx, "SELECT sepgsql_setcon($1::text)")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		var changed bool
		if err = c.QueryRowContext(ctx, "SELECT sepgsql_setcon(NULL)").Scan(&changed); !errors.Is(err, context.Canceled) {
			t.Fatal("pre-canceled direct SQL", err)
		}
		if err = held.QueryRowContext(ctx, sepgsqlA).Scan(&changed); !errors.Is(err, context.Canceled) {
			t.Fatal("pre-canceled held SQL", err)
		}
		run(sepgsqlContextValue("SELECT sepgsql_getcon() AS value", sepgsqlR))
	})
}

// Remove only this scenario's broad R process grant; ordinary admission stays
// unchanged. Every omission below therefore represents a substantive default deny.
func sepgsqlContextProcess(config *stuntgres.SepgsqlSimulationConfig, setcurrent bool, targets ...string) {
	rules := config.Policy.Rules[:0]
	for _, rule := range config.Policy.Rules {
		if rule.Source != sepgsqlR || rule.Class != "process" {
			rules = append(rules, rule)
		}
	}
	config.Policy.Rules = rules
	if setcurrent {
		config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlR, Class: "process", Allow: []string{"setcurrent"}})
	}
	for _, target := range targets {
		config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlR, Target: target, Class: "process", Allow: []string{"dyntransition"}})
	}
}

func sepgsqlContextOrdinaryPermissions(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
	sepgsqlContextOrdinaryFunction(run)
	run(fixture.Exec("CREATE ROLE context_caller", 0))
	run(fixture.Exec("GRANT USAGE ON SCHEMA public TO context_caller", 0))
	run(fixture.Exec("GRANT SELECT ON context_inputs TO context_caller", 0))
	run(fixture.Exec("REVOKE EXECUTE ON FUNCTION context_identity(text) FROM PUBLIC", 0))
	run(fixture.Exec("SET plan_cache_mode=force_generic_plan", 0))
	run(fixture.Exec("SET ROLE context_caller", 0))
	for _, prepared := range []bool{false, true} {
		step := fixture.ImplicitError("SELECT context_identity(v) FROM context_inputs", fixture.Diagnostic{Code: "42501", Message: "permission denied for function context_identity"})
		step.Prepared = prepared
		run(step)
	}
	query := "SELECT context_identity($1::text)"
	statement, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	for _, value := range []any{nil, "text"} {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}, b.Recorder, fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function context_identity"}, value))
	}
	run(fixture.Exec("RESET ROLE", 0))
	run(sepgsqlContextValue("SELECT context_identity(v) AS value FROM context_inputs", nil))
	run(fixture.Exec("DROP OWNED BY context_caller", 0))
	run(fixture.Exec("DROP ROLE context_caller", 0))
}

func TestReferenceSepgsqlSimulationContextPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"context_caller", "context_acl"})
	for _, scenario := range []struct {
		name       string
		setcurrent bool
		targets    []string
		requests   []any
		permissive bool
	}{
		{"setcurrent", false, []string{sepgsqlA, sepgsqlR, sepgsqlT}, []any{sepgsqlT, sepgsqlR, nil}, false},
		{"dyntransition_target", true, []string{sepgsqlA, sepgsqlR}, []any{sepgsqlT}, false},
		{"dyntransition_same", true, []string{sepgsqlA}, []any{sepgsqlR}, false},
		{"dyntransition_reset", true, []string{sepgsqlR}, []any{nil}, false},
		{"permissive", true, []string{sepgsqlA, sepgsqlR}, []any{sepgsqlT}, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			sepgsqlCase(t, sepgsqlContextOrdinaryPermissions, func(config *stuntgres.SepgsqlSimulationConfig) {
				sepgsqlContextProcess(config, scenario.setcurrent, scenario.targets...)
				config.Permissive = scenario.permissive
			}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
				run(sepgsqlSet(sepgsqlR))
				run(sepgsqlBool("SELECT current_setting('is_superuser')::boolean AS value", "value", true))
				for _, request := range scenario.requests {
					query := "SELECT sepgsql_setcon($1::text)"
					if scenario.permissive {
						run(fixture.Select(query, []string{"sepgsql_setcon"}, []string{"BOOL"}, [][]any{{true}}, request))
						for _, input := range []string{"unknown", "", " "} {
							run(sepgsqlContextInvalid(query, input, input))
							run(sepgsqlContextValue("SELECT sepgsql_getcon() AS value", sepgsqlT))
						}
						continue
					}
					sepgsqlContextRoutes(t, p, b, c, fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "SELinux: security policy violation"}, request), sepgsqlContextValue("SELECT sepgsql_getcon() AS value", sepgsqlR))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("SAVEPOINT before_denial", 0))
					run(fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "SELinux: security policy violation"}, request))
					run(fixture.Exec("ROLLBACK TO before_denial", 0))
					run(sepgsqlContextValue("SELECT sepgsql_getcon() AS value", sepgsqlR))
					run(fixture.Exec("ROLLBACK", 0))
				}
			})
		})
	}
	t.Run("strict_admission", func(t *testing.T) {
		sepgsqlCase(t, sepgsqlContextOrdinaryPermissions, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			run(fixture.Exec("CREATE ROLE context_acl", 0))
			run(fixture.Exec("CREATE TABLE context_inputs(v text)", 0))
			run(fixture.Exec("INSERT INTO context_inputs VALUES(NULL)", 1))
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO context_acl", 0))
			run(fixture.Exec("GRANT SELECT ON context_inputs TO context_acl", 0))
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION sepgsql_mcstrans_in(text) FROM PUBLIC", 0))
			run(fixture.Exec("SET plan_cache_mode=force_generic_plan", 0))
			run(fixture.Exec("SET ROLE context_acl", 0))
			for _, prepared := range []bool{false, true} {
				step := fixture.ImplicitError("SELECT sepgsql_mcstrans_in(v) FROM context_inputs", fixture.Diagnostic{Code: "42501", Message: "permission denied for function sepgsql_mcstrans_in"})
				step.Prepared = prepared
				run(step)
			}
			query := "SELECT sepgsql_mcstrans_in($1::text)"
			statement, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}, b.Recorder, fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function sepgsql_mcstrans_in"}, nil))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION sepgsql_mcstrans_in(text) TO PUBLIC", 0))
			run(sepgsqlContextValue("SELECT sepgsql_getcon() AS value", sepgsqlA))
			run(fixture.Exec("SECURITY LABEL ON FUNCTION sepgsql_mcstrans_in(text) IS '"+sepgsqlU+"'", 0))
			run(sepgsqlSet(sepgsqlR))
			sepgsqlContextRoutes(t, p, b, c,
				sepgsqlDenied("SELECT sepgsql_mcstrans_in(v) FROM context_inputs"),
				fixture.ImplicitError("SELECT sepgsql_mcstrans_in($1::text)", fixture.Diagnostic{Code: "42501", Message: "SELinux: security policy violation"}, nil),
				sepgsqlContextValue("SELECT sepgsql_getcon() AS value", sepgsqlR))
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			run(fixture.Exec("DROP OWNED BY context_acl", 0))
			run(fixture.Exec("DROP ROLE context_acl", 0))
		})
	})
}

func sepgsqlContextOrdinaryTransactions(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
	run(fixture.Exec("CREATE TABLE context_state(id integer PRIMARY KEY,v text)", 0))
	run(fixture.Exec("INSERT INTO context_state VALUES(1,'A')", 1))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("UPDATE context_state SET v='R' WHERE id=1", 1))
	run(fixture.Exec("SAVEPOINT context_mark", 0))
	run(fixture.Exec("UPDATE context_state SET v='T' WHERE id=1", 1))
	run(fixture.Exec("ROLLBACK TO context_mark", 0))
	run(sepgsqlContextValue("SELECT v AS value FROM context_state", "R"))
	run(fixture.Exec("RELEASE context_mark", 0))
	run(fixture.Exec("ROLLBACK", 0))
	run(sepgsqlContextValue("SELECT v AS value FROM context_state", "A"))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("UPDATE context_state SET v='R' WHERE id=1", 1))
	run(fixture.Exec("COMMIT", 0))
	run(sepgsqlContextValue("SELECT v AS value FROM context_state", "R"))
}

func TestReferenceSepgsqlSimulationContextTransactions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"context_role"})
	sepgsqlCase(t, sepgsqlContextOrdinaryTransactions, func(config *stuntgres.SepgsqlSimulationConfig) {
		// Trusted T must be allowed to change pending state; otherwise a process
		// denial would conceal the trusted-mask behavior this scenario owns.
		config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlT, Target: "*", Class: "process", Allow: []string{"setcurrent", "dyntransition"}})
	}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		check := func(want string) {
			run(sepgsqlContextValue("SELECT sepgsql_getcon() AS value", want))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, sepgsqlContextValue("SELECT sepgsql_getcon() AS value", sepgsqlA))
		}
		run(fixture.Exec("CREATE ROLE context_role", 0))
		run(fixture.Exec("CREATE FUNCTION context_trusted(value text) RETURNS text LANGUAGE sql VOLATILE AS $$SELECT sepgsql_setcon(value); SELECT sepgsql_getcon()$$", 0))
		run(fixture.Exec("SECURITY LABEL ON FUNCTION context_trusted(text) IS '"+sepgsqlS+"'", 0))
		check(sepgsqlA)
		run(fixture.Exec("BEGIN", 0))
		run(sepgsqlSet(sepgsqlR))
		run(fixture.Exec("SAVEPOINT context_mark", 0))
		run(sepgsqlSet(sepgsqlT))
		run(fixture.Exec("ROLLBACK TO context_mark", 0))
		check(sepgsqlR)
		run(fixture.Exec("RELEASE context_mark", 0))
		check(sepgsqlR)
		run(fixture.Exec("SAVEPOINT context_failure", 0))
		run(sepgsqlContextInvalid("SELECT sepgsql_setcon('unknown')", "unknown"))
		run(fixture.Exec("ROLLBACK TO context_failure", 0))
		check(sepgsqlR)
		run(fixture.Exec("ROLLBACK", 0))
		check(sepgsqlA)
		run(fixture.Exec("BEGIN", 0))
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlContextValue("SELECT context_trusted('"+sepgsqlA+"') AS value", sepgsqlT))
		check(sepgsqlA)
		run(fixture.Exec("ROLLBACK", 0))
		check(sepgsqlA)
		run(fixture.Exec("BEGIN", 0))
		run(sepgsqlSet(sepgsqlR))
		run(fixture.Exec("COMMIT", 0))
		check(sepgsqlR)
		run(fixture.Exec("SET ROLE context_role", 0))
		check(sepgsqlR)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("BEGIN", 0))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		check(sepgsqlA)
		run(fixture.Exec("ROLLBACK", 0))
		check(sepgsqlR)
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		check(sepgsqlA)
		run(fixture.Exec("DROP ROLE context_role", 0))
	})
}
