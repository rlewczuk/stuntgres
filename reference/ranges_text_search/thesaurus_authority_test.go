package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceThesaurusAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"thes_oracle_creator", "thes_oracle_other"})
	fixture.SchemaReferenceCleanup(t, "thes_oracle_private")
	thesaurusScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		thesaurusSetup(t, p, b, c, "thesaurus_sample")
		run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
		for _, q := range []string{
			"CREATE ROLE thes_oracle_creator", "CREATE ROLE thes_oracle_other",
			"GRANT USAGE,CREATE ON SCHEMA public TO thes_oracle_creator",
			"GRANT USAGE ON SCHEMA public TO thes_oracle_other",
			"CREATE SCHEMA thes_oracle_private",
			"CREATE TEXT SEARCH DICTIONARY thes_oracle_private.sub (TEMPLATE=pg_catalog.simple,STOPWORDS=english)",
			"SET ROLE thes_oracle_creator",
		} {
			run(fixture.Exec(q, 0))
		}
		defer func() { run(fixture.Exec("RESET ROLE", 0)) }()
		denied := thesaurusDiagnostic("42501", "permission denied for schema thes_oracle_private")
		for _, prepared := range []bool{false, true} {
			name := fmt.Sprintf("thes_denied_%t", prepared)
			query := "CREATE TEXT SEARCH DICTIONARY public." + name + " (TEMPLATE=pg_catalog.thesaurus,DictFile=thesaurus_sample,Dictionary='thes_oracle_private.sub')"
			step := fixture.ErrorDiagnostic(query, denied)
			step.Prepared = prepared
			step.ErrorStage = "execute"
			run(step)
			run(fixture.Select("SELECT count(*)::bigint AS n FROM pg_ts_dict WHERE dictname='"+name+"'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			malformed := thesaurusDiagnostic("F0000", "unexpected end of line")
			malformed.Context = "line 1 of configuration file \"stuntgres_thes_bad_end.ths\": \"alpha\n\""
			pgMalformed := malformed
			pgMalformed.Context = "line 1 of configuration file \"/usr/share/postgresql/18/tsearch_data/stuntgres_thes_bad_end.ths\": \"alpha\n\""
			malformedSQL := "CREATE TEXT SEARCH DICTIONARY public." + name + " (TEMPLATE=pg_catalog.thesaurus,DictFile=stuntgres_thes_bad_end,Dictionary='thes_oracle_private.sub')"
			malformedStep := fixture.ErrorDiagnostic(malformedSQL, malformed)
			malformedStep.PostgresDiagnostic = &pgMalformed
			malformedStep.Prepared, malformedStep.ErrorStage = prepared, "execute"
			run(malformedStep)
			run(fixture.Select("SELECT count(*)::bigint AS n FROM pg_ts_dict WHERE dictname='"+name+"'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		// Quoted decoded option names use the qualified-name parser, not dot splitting.
		run(fixture.Prepared(fixture.Exec(`CREATE TEXT SEARCH DICTIONARY public.thes_authorized (TEMPLATE=pg_catalog.thesaurus,DictFile=thesaurus_sample,Dictionary='"public"."thes_sub"')`, 0)))
		run(fixture.Select("SELECT ts_lexize('public.thes_authorized','one') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{1}"}}))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA thes_oracle_private TO thes_oracle_creator", 0))
		run(fixture.Exec("SET ROLE thes_oracle_creator", 0))
		thesaurusInitializedAuthority(t, p, b, c)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("SET ROLE thes_oracle_other", 0))
		owner := fixture.ErrorDiagnostic("ALTER TEXT SEARCH DICTIONARY public.thes_authorized (DictFile=stuntgres_thes_ties)", thesaurusDiagnostic("42501", "must be owner of text search dictionary public.thes_authorized"))
		owner.Prepared = true
		owner.ErrorStage = "execute"
		run(owner)
		destination := fixture.ErrorDiagnostic("CREATE TEXT SEARCH DICTIONARY public.thes_no_create (TEMPLATE=pg_catalog.thesaurus,DictFile=thesaurus_sample,Dictionary='public.thes_sub')", thesaurusDiagnostic("42501", "permission denied for schema public"))
		destination.Prepared = true
		destination.ErrorStage = "execute"
		run(destination)
	})
}

// Initialized permission belongs to the physical session, not a prepared SQL
// handle or a shared compiled-table cache. This uses public connection lifetime
// controls and distinct backend identities in both modes.
func thesaurusInitializedAuthority(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(query string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(query, 0)) }
	for _, query := range []string{
		"CREATE TEXT SEARCH DICTIONARY public.thes_private (TEMPLATE=pg_catalog.thesaurus,DictFile=thesaurus_sample,Dictionary='thes_oracle_private.sub')",
		"CREATE TEXT SEARCH CONFIGURATION public.thes_private_cfg (COPY=pg_catalog.simple)",
		"ALTER TEXT SEARCH CONFIGURATION public.thes_private_cfg ALTER MAPPING FOR asciiword WITH public.thes_private",
	} {
		run(query)
	}
	queries := []string{
		"SELECT ts_lexize('public.thes_private',$1::text) AS lexemes",
		"SELECT to_tsvector('public.thes_private_cfg',$1::text) AS normalized",
	}
	held := make([]*sql.Stmt, len(queries))
	for i, query := range queries {
		var err error
		held[i], err = c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held[i].Close()
	}
	step := func(index int, denied bool) fixture.Step {
		if denied {
			return fixture.ErrorDiagnostic(queries[index], thesaurusDiagnostic("42501", "permission denied for schema thes_oracle_private"), "one")
		}
		if index == 0 {
			return fixture.Select(queries[index], []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{1}"}}, "one")
		}
		return ftsConfigurationVector(queries[index], "'1':1", "one")
	}
	check := func(conn *sql.Conn, useHeld, denied bool) {
		t.Helper()
		for i, query := range queries {
			if useHeld {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held[i], SQL: query}, b.Recorder, step(i, denied))
			}
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, step(i, denied))
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Prepared(step(i, denied)))
		}
	}
	var originalPID int64
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()::bigint").Scan(&originalPID); err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{originalPID: true}
	newPhysical := func(denied bool) {
		t.Helper()
		borrowed, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		_ = borrowed.Raw(func(any) error { return driver.ErrBadConn })
		borrowed.Close()
		fresh, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		defer fresh.Raw(func(any) error { return driver.ErrBadConn })
		var pid int64
		if err := fresh.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()::bigint").Scan(&pid); err != nil {
			t.Fatal(err)
		}
		if pid <= 0 || seen[pid] {
			t.Fatalf("physical session identity reused: %d", pid)
		}
		seen[pid] = true
		fixture.RunStep(t, p.Ctx, fresh, b.Recorder, fixture.Exec("SET ROLE thes_oracle_creator", 0))
		check(fresh, false, denied)
	}
	check(c, true, false)
	newPhysical(false)
	run("RESET ROLE")
	run("REVOKE USAGE ON SCHEMA thes_oracle_private FROM thes_oracle_creator")
	run("SET ROLE thes_oracle_creator")
	check(c, true, false)
	newPhysical(true)
	run("RESET ROLE")
	run("ALTER TEXT SEARCH DICTIONARY thes_oracle_private.sub (Accept=true)")
	run("SET ROLE thes_oracle_creator")
	check(c, true, true)
	newPhysical(true)
	run("RESET ROLE")
	run("GRANT USAGE ON SCHEMA thes_oracle_private TO thes_oracle_creator")
	run("SET ROLE thes_oracle_creator")
	check(c, true, false)
	newPhysical(false)
}
