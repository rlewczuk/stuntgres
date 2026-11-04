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

func TestReferenceSepgsqlSimulationCodecs(t *testing.T) {
	var mu sync.Mutex
	var events []stuntgres.SepgsqlAuditEvent
	install := func(run sepgsqlRun) {
		run(fixture.Exec("CREATE EXTENSION hstore", 0))
		run(fixture.Exec("CREATE EXTENSION ltree", 0))
		run(fixture.Exec("CREATE DOMAIN sepgsql_codec_domain AS hstore", 0))
		run(fixture.Exec("CREATE TYPE sepgsql_codec_record AS (v hstore)", 0))
	}
	scalar := func(query string) fixture.Step { return sepgsqlText(query, "value", `"k"=>"v"`) }
	noArrayLeaf := func(run sepgsqlRun) {
		for _, step := range []fixture.Step{
			sepgsqlBool("SELECT array_to_string(NULL::ltree[],';') IS NULL AS skipped", "skipped", true),
			sepgsqlText("SELECT array_to_string(ARRAY[NULL::ltree],';') AS value", "value", ""),
		} {
			run(step)
			run(fixture.Prepared(step))
		}
	}
	positive := func(run sepgsqlRun) {
		run(sepgsqlText("SELECT ('k=>v'::hstore)->'k' AS value", "value", "v"))
		run(scalar("SELECT ('k=>v'::hstore)::text AS value"))
		run(fixture.Prepared(scalar("SELECT ('k=>v'::hstore)::text AS value")))
		run(sepgsqlText("SELECT array_to_string(ARRAY['a'::ltree],';') AS value", "value", "a"))
		run(fixture.Prepared(sepgsqlText("SELECT array_to_string(ARRAY['a'::ltree],';') AS value", "value", "a")))
		run(scalar("SELECT format('%s','k=>v'::hstore) AS value"))
		run(sepgsqlBool("SELECT (NULL::hstore)::text IS NULL AS skipped", "skipped", true))
		run(scalar("SELECT ('k=>v'::sepgsql_codec_domain)::text AS value"))
		run(sepgsqlText(`SELECT ('{a}'::ltree[])[1]::text AS value`, "value", "a"))
		run(fixture.Prepared(sepgsqlText(`SELECT ('{a}'::ltree[])[1]::text AS value`, "value", "a")))
		run(scalar(`SELECT v::text AS value FROM json_populate_record(NULL::sepgsql_codec_record,'{"v":"k=>v"}')`))
		parameter := fixture.Select("SELECT ($1::hstore)::text AS value", []string{"value"}, []string{"TEXT"}, [][]any{{`"k"=>"v"`}}, "k=>v")
		run(parameter)
		run(fixture.Prepared(parameter))
		noArrayLeaf(run)
	}
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		install(run)
		positive(run)
		run(sepgsqlBool("SELECT pg_input_is_valid('k=>v','hstore') AS valid", "valid", true))
	}, func(config *stuntgres.SepgsqlSimulationConfig) {
		config.AuditAllowed = true
		config.Audit = func(event stuntgres.SepgsqlAuditEvent) { mu.Lock(); events = append(events, event); mu.Unlock() }
		// Direct text input/output are fmgr callbacks, not ordinary SQL EXECUTE.
		// They retain meaningful trusted entry checks even when execute is denied.
		config.Policy.Rules = append(config.Policy.Rules,
			stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlS, Class: "db_procedure", Deny: []string{"execute"}},
			stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlU, Class: "db_procedure", Allow: []string{"execute"}})
		config.Policy.TrustedTransitions = append(config.Policy.TrustedTransitions, stuntgres.SepgsqlTrustedTransition{Source: sepgsqlR, Procedure: sepgsqlU, Result: sepgsqlT})
	}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		install(run)
		label := func(signature, context string) {
			run(fixture.Exec("SECURITY LABEL ON FUNCTION "+signature+" IS '"+context+"'", 0))
		}
		label("hstore_in(cstring)", sepgsqlS)
		label("hstore_out(hstore)", sepgsqlS)
		label("ltree_in(cstring)", sepgsqlS)
		label("ltree_out(ltree)", sepgsqlS)
		run(sepgsqlSet(sepgsqlR))
		positive(run)
		run(sepgsqlBool("SELECT pg_input_is_valid('k=>v','hstore') AS valid", "valid", true))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		mu.Lock()
		observed := slices.Clone(events)
		mu.Unlock()
		for _, name := range []string{"hstore_in", "hstore_out", "ltree_in", "ltree_out"} {
			entered := false
			for _, event := range observed {
				if event.Subject == sepgsqlR && event.Target == sepgsqlS && event.Class == "db_procedure" && strings.Contains(event.Name, name) && slices.Contains(event.Required, "entrypoint") && len(event.Denied) == 0 {
					entered = true
				}
			}
			if !entered {
				t.Fatalf("actual %s callback was not entered: %#v", name, observed)
			}
		}
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(fixture.Exec("ALTER FUNCTION hstore_in(cstring) RENAME TO sepgsql_renamed_hstore_input", 0))
		run(fixture.Exec("ALTER FUNCTION hstore_out(hstore) RENAME TO sepgsql_renamed_hstore_output", 0))
		run(fixture.Exec("ALTER FUNCTION ltree_in(cstring) RENAME TO sepgsql_renamed_ltree_input", 0))
		run(fixture.Exec("ALTER FUNCTION ltree_out(ltree) RENAME TO sepgsql_renamed_ltree_output", 0))
		run(sepgsqlSet(sepgsqlR))
		positive(run)
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		label("sepgsql_renamed_hstore_input(cstring)", sepgsqlU)
		label("sepgsql_renamed_ltree_input(cstring)", sepgsqlU)
		run(sepgsqlSet(sepgsqlR))
		// REL_18_6 parse_coerce.c installs the parser errposition callback at
		// the original UNKNOWN literal before calling stringTypeDatum, also
		// for domain base input and recursive array element input. The callback
		// in parse_node.c attaches that position to every non-cancel error.
		for _, query := range []string{
			"SELECT 'broken'::hstore",
			"SELECT 'broken'::sepgsql_codec_domain",
		} {
			step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "SELinux: security policy violation", Position: "8"})
			run(step)
			run(fixture.Prepared(step))
		}
		run(sepgsqlDenied("SELECT pg_input_is_valid('broken','hstore')"))
		nestedInput := fixture.ImplicitError(`SELECT ('{a..b}'::ltree[])[1]::text`, fixture.Diagnostic{Code: "42501", Message: "SELinux: security policy violation", Position: "9"})
		run(nestedInput)
		run(fixture.Prepared(nestedInput))
		run(sepgsqlDenied(`SELECT v::text FROM json_populate_record(NULL::sepgsql_codec_record,'{"v":"broken"}')`))
		run(sepgsqlBool("SELECT (NULL::hstore)::text IS NULL AS skipped", "skipped", true))
		noArrayLeaf(run)
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		label("sepgsql_renamed_hstore_input(cstring)", sepgsqlS)
		label("sepgsql_renamed_ltree_input(cstring)", sepgsqlS)
		label("sepgsql_renamed_hstore_output(hstore)", sepgsqlU)
		label("sepgsql_renamed_ltree_output(ltree)", sepgsqlU)
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlDenied("SELECT ('k=>v'::hstore)::text"))
		run(sepgsqlDenied("SELECT array_to_string(ARRAY['a'::ltree],';')"))
		run(fixture.Prepared(sepgsqlDenied("SELECT array_to_string(ARRAY['a'::ltree],';')")))
		run(sepgsqlDenied("SELECT format('%s','k=>v'::hstore)"))
		run(sepgsqlBool("SELECT (NULL::hstore)::text IS NULL AS skipped", "skipped", true))
		noArrayLeaf(run)
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
	})
}
