package reference_test

import (
	"database/sql"
	"slices"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func sepgsqlDebugWord(query string) fixture.Step {
	return fixture.Select(query, []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "WORD", "{simple}", "simple", "{word}"}})
}

func TestReferenceSepgsqlSimulationFTS(t *testing.T) {
	var mu sync.Mutex
	var events []stuntgres.SepgsqlAuditEvent
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		run(sepgsqlDebugWord("SELECT * FROM ts_debug('simple'::regconfig,'WORD')"))
		run(fixture.Exec("SET default_text_search_config='simple'", 0))
		run(sepgsqlText("SELECT get_current_ts_config()::text AS configuration", "configuration", "simple"))
		run(sepgsqlDebugWord("SELECT * FROM ts_debug('WORD')"))
		run(fixture.Prepared(sepgsqlDebugWord("SELECT * FROM ts_debug('WORD')")))
	}, func(config *stuntgres.SepgsqlSimulationConfig) {
		config.AuditAllowed = true
		config.Audit = func(event stuntgres.SepgsqlAuditEvent) { mu.Lock(); events = append(events, event); mu.Unlock() }
		// R can enter B=T, but only nested C=A can read the secret query catalogs.
		config.Policy.Rules = slices.DeleteFunc(config.Policy.Rules, func(rule stuntgres.SepgsqlRule) bool {
			return rule.Source == sepgsqlT && (rule.Class == "db_table" || rule.Class == "db_column")
		})
		config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlT, Target: sepgsqlA, Class: "process", Allow: []string{"transition"}})
		config.Policy.TrustedTransitions = append(config.Policy.TrustedTransitions,
			stuntgres.SepgsqlTrustedTransition{Source: sepgsqlT, Procedure: sepgsqlU, Result: sepgsqlA},
			stuntgres.SepgsqlTrustedTransition{Source: sepgsqlA, Procedure: sepgsqlU, Result: sepgsqlR})
	}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		for _, query := range []string{
			"CREATE EXTENSION unaccent",
			"CREATE ROLE sepgsql_fts_actor",
			"REVOKE ALL ON FUNCTION unaccent_init(internal) FROM PUBLIC",
			"REVOKE ALL ON FUNCTION unaccent_lexize(internal,internal,internal,internal) FROM PUBLIC",
			"CREATE TEXT SEARCH CONFIGURATION sepgsql_debug (COPY=pg_catalog.simple)",
			"ALTER TEXT SEARCH CONFIGURATION sepgsql_debug ALTER MAPPING FOR asciiword WITH unaccent,pg_catalog.simple",
			"SET default_text_search_config='sepgsql_debug'",
			"SECURITY LABEL ON TABLE pg_catalog.pg_ts_config IS '" + sepgsqlS + "'",
			"SECURITY LABEL ON TABLE pg_catalog.pg_ts_config_map IS '" + sepgsqlS + "'",
			"SECURITY LABEL ON FUNCTION pg_catalog.ts_debug(text) IS '" + sepgsqlS + "'",
			"SECURITY LABEL ON FUNCTION pg_catalog.ts_debug(regconfig,text) IS '" + sepgsqlU + "'",
			"SECURITY LABEL ON FUNCTION pg_catalog.get_current_ts_config() IS '" + sepgsqlU + "'",
			"SECURITY LABEL ON FUNCTION unaccent_init(internal) IS '" + sepgsqlU + "'",
			"SECURITY LABEL ON FUNCTION unaccent_lexize(internal,internal,internal,internal) IS '" + sepgsqlU + "'",
			"SET ROLE sepgsql_fts_actor",
		} {
			run(fixture.Exec(query, 0))
		}
		run(sepgsqlSet(sepgsqlR))
		step := fixture.Select("SELECT * FROM ts_debug('WORD')", []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}, [][]any{{"asciiword", "Word, all ASCII", "WORD", "{unaccent,simple}", "simple", "{word}"}})
		run(step)
		run(fixture.Prepared(step))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		run(sepgsqlDenied("SELECT * FROM ts_debug('sepgsql_debug'::regconfig,'WORD')"))
		// Assert discriminating phase witnesses, not planner-dependent AVC counts.
		mu.Lock()
		observed := slices.Clone(events)
		mu.Unlock()
		witness := func(subject, target, class, permission string) bool {
			for _, event := range observed {
				if event.Subject == subject && event.Target == target && event.Class == class && len(event.Denied) == 0 && slices.Contains(event.Required, permission) {
					return true
				}
			}
			return false
		}
		for _, want := range [][4]string{{sepgsqlT, sepgsqlU, "db_procedure", "execute"}, {sepgsqlT, sepgsqlA, "process", "transition"}, {sepgsqlA, sepgsqlS, "db_table", "select"}, {sepgsqlA, sepgsqlU, "db_procedure", "entrypoint"}, {sepgsqlA, sepgsqlR, "process", "transition"}} {
			if !witness(want[0], want[1], want[2], want[3]) {
				t.Fatalf("missing actual phase witness %q in %#v", want, observed)
			}
		}
		// Strict NULL produces no body or callbacks and must not disturb R.
		run(fixture.Select("SELECT * FROM ts_debug(NULL::text)", step.Columns, step.Types, nil))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
	})
}

func sepgsqlStagedDictionaryInstallation(t *testing.T) {
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		run(fixture.Exec("CREATE EXTENSION unaccent", 0))
		run(sepgsqlText("SELECT ts_lexize('unaccent','café')::text AS lexemes", "lexemes", "{cafe}"))
		run(sepgsqlCount("SELECT count(*) FROM pg_proc WHERE proname IN ('unaccent','unaccent_init','unaccent_lexize')", 4))
		run(fixture.Exec("DROP EXTENSION unaccent", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_proc WHERE proname IN ('unaccent','unaccent_init','unaccent_lexize')", 0))
	}, func(config *stuntgres.SepgsqlSimulationConfig) {
		config.Policy.CreateTransitions = append(config.Policy.CreateTransitions, stuntgres.SepgsqlCreateTransition{Source: sepgsqlA, Parent: sepgsqlP, Class: "db_procedure", Name: "unaccent_init", Result: sepgsqlU})
		config.Policy.TrustedTransitions = append(config.Policy.TrustedTransitions, stuntgres.SepgsqlTrustedTransition{Source: sepgsqlA, Procedure: sepgsqlU, Result: sepgsqlT})
		config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlA, Target: sepgsqlU, Class: "db_procedure", Deny: []string{"entrypoint"}})
	}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		run(sepgsqlDenied("CREATE EXTENSION unaccent"))
		run(sepgsqlCount("SELECT count(*) FROM pg_extension WHERE extname='unaccent'", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_proc WHERE proname IN ('unaccent','unaccent_init','unaccent_lexize')", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_ts_template WHERE tmplname='unaccent'", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_ts_dict WHERE dictname='unaccent'", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE provider='selinux' AND label='"+sepgsqlU+"'", 0))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlA))
		run(fixture.Exec("CREATE TABLE sepgsql_after_initializer_denial(id int)", 0))
	})
}

func TestReferenceSepgsqlSimulationDictionaries(t *testing.T) {
	t.Run("staged_installation", sepgsqlStagedDictionaryInstallation)
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		run(fixture.Exec("CREATE EXTENSION unaccent", 0))
		run(sepgsqlText("SELECT ts_lexize('unaccent','café')::text AS lexemes", "lexemes", "{cafe}"))
		run(fixture.Prepared(sepgsqlText("SELECT ts_lexize('unaccent','café')::text AS lexemes", "lexemes", "{cafe}")))
	}, func(config *stuntgres.SepgsqlSimulationConfig) {
		// A's cold initializer enters T. R has the same transition but no entrypoint
		// permission for U, making a genuinely cold R session deny deterministically.
		config.Policy.TrustedTransitions = append(config.Policy.TrustedTransitions,
			stuntgres.SepgsqlTrustedTransition{Source: sepgsqlA, Procedure: sepgsqlU, Result: sepgsqlT},
			stuntgres.SepgsqlTrustedTransition{Source: sepgsqlR, Procedure: sepgsqlU, Result: sepgsqlT})
	}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		run(fixture.Exec("CREATE EXTENSION unaccent", 0))
		run(fixture.Exec("SECURITY LABEL ON FUNCTION unaccent_init(internal) IS '"+sepgsqlU+"'", 0))
		query := "SELECT ts_lexize('unaccent','café')::text AS lexemes"
		run(sepgsqlText(query, "lexemes", "{cafe}"))
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlText(query, "lexemes", "{cafe}"))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(fixture.Exec("SECURITY LABEL ON FUNCTION unaccent_init(internal) IS '"+sepgsqlS+"'", 0))
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlText(query, "lexemes", "{cafe}"))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(fixture.Exec("SECURITY LABEL ON FUNCTION unaccent_init(internal) IS '"+sepgsqlU+"'", 0))
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlText(query, "lexemes", "{cafe}"))
		peer, err := p.Memory.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		execPeer := func(query string) {
			t.Helper()
			if _, err := peer.ExecContext(p.Ctx, query); err != nil {
				t.Fatal(query, err)
			}
		}
		execPeer("SELECT sepgsql_setcon('" + sepgsqlR + "')")
		_, err = peer.ExecContext(p.Ctx, query)
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "SELinux: security policy violation"})
		var subject string
		if err = peer.QueryRowContext(p.Ctx, "SELECT sepgsql_getcon()").Scan(&subject); err != nil || subject != sepgsqlR {
			t.Fatal("cold denial subject", subject, err)
		}
		// Failure publishes no receipt; only a later real A initialization warms it.
		execPeer("SELECT sepgsql_setcon(NULL)")
		execPeer(query)
		execPeer("SELECT sepgsql_setcon('" + sepgsqlR + "')")
		execPeer(query)
		// Descriptor identity, unlike subject/label association, invalidates the warm
		// receipt. A real helper rename therefore makes this same R session cold.
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(fixture.Exec("ALTER FUNCTION unaccent_init(internal) RENAME TO sepgsql_renamed_init", 0))
		_, err = peer.ExecContext(p.Ctx, query)
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "SELinux: security policy violation"})
		execPeer("SELECT sepgsql_setcon(NULL)")
		execPeer(query)
		execPeer("SELECT sepgsql_setcon('" + sepgsqlR + "')")
		execPeer(query)
	})
}
