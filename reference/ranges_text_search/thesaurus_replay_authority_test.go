package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// The host replay namespace and the SQL caller's lookup path are intentionally
// different. Only public connection/control APIs participate in this witness.
func thesaurusReplayAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"thes_replay_denied"})
	fixture.SchemaReferenceCleanup(t, "thes_replay_a")
	fixture.SchemaReferenceCleanup(t, "thes_replay_b")
	var emitted []byte
	thesaurusScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, producer *sql.Conn) {
		run := func(conn *sql.Conn, step fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, step) }
		for _, query := range []string{
			"CREATE ROLE thes_replay_denied",
			"GRANT USAGE ON SCHEMA public TO thes_replay_denied",
			"CREATE SCHEMA thes_replay_a",
			"CREATE SCHEMA thes_replay_b",
			"CREATE TEXT SEARCH DICTIONARY thes_replay_a.zz_sub (TEMPLATE=pg_catalog.simple,STOPWORDS=english)",
			"CREATE TEXT SEARCH DICTIONARY thes_replay_b.zz_sub (TEMPLATE=pg_catalog.simple,STOPWORDS=english,Accept=false)",
			"SET search_path=thes_replay_a,public",
			"CREATE TEXT SEARCH DICTIONARY public.thes_replay_aa (TEMPLATE=pg_catalog.thesaurus,DictFile=thesaurus_sample,Dictionary='zz_sub')",
			"CREATE TEXT SEARCH CONFIGURATION public.thes_replay_cfg (COPY=pg_catalog.simple)",
			"ALTER TEXT SEARCH CONFIGURATION public.thes_replay_cfg ALTER MAPPING FOR asciiword WITH public.thes_replay_aa,pg_catalog.simple",
		} {
			run(producer, fixture.Exec(query, 0))
		}
		const scalarSQL = "SELECT ts_lexize('public.thes_replay_aa',$1::text) AS lexemes"
		const vectorSQL = "SELECT to_tsvector('public.thes_replay_cfg',$1::text) AS normalized"
		const sourceOptions = "dictfile = 'thesaurus_sample', dictionary = 'zz_sub'"
		options := func(conn *sql.Conn, want string) {
			t.Helper()
			run(conn, fixture.Select("SELECT dictinitoption FROM pg_ts_dict WHERE oid='public.thes_replay_aa'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{want}}))
		}
		run(producer, fixture.Select(scalarSQL, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{1}"}}, "one"))
		options(producer, sourceOptions)
		var producerPID int64
		if err := producer.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()::bigint").Scan(&producerPID); err != nil {
			t.Fatal(err)
		}
		// Returning a borrower alone would not retire its physical session.
		_ = producer.Raw(func(any) error { return driver.ErrBadConn })
		producer.Close()
		consumer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer consumer.Close()
		var consumerPID int64
		if err := consumer.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()::bigint").Scan(&consumerPID); err != nil {
			t.Fatal(err)
		}
		if producerPID <= 0 || consumerPID <= 0 || producerPID == consumerPID {
			t.Fatal("producer physical session survived retirement", producerPID, consumerPID)
		}
		run(consumer, fixture.Exec("SET search_path=thes_replay_b,public", 0))
		held, err := consumer.PrepareContext(p.Ctx, scalarSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		rejected := thesaurusDiagnostic("F0000", `thesaurus sample word "one" isn't recognized by subdictionary (rule 1)`)
		checkScalar := func(denied bool) {
			t.Helper()
			step := fixture.Select(scalarSQL, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{1}"}}, "one")
			if denied {
				step = fixture.ErrorDiagnostic(scalarSQL, rejected, "one")
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: consumer, Statement: held, SQL: scalarSQL}, b.Recorder, step)
			run(consumer, step)
			run(consumer, fixture.Prepared(step))
		}
		// Accepted replay provenance A must not redirect initial SQL lookup away
		// from the consumer's distinguishable rejecting B normalizer.
		checkScalar(true)
		run(consumer, fixture.Exec("ALTER TEXT SEARCH DICTIONARY thes_replay_b.zz_sub (Accept=true)", 0))
		checkScalar(false)
		run(consumer, fixture.Exec("ALTER TEXT SEARCH DICTIONARY thes_replay_b.zz_sub (Accept=false)", 0))
		checkScalar(true)
		run(consumer, fixture.Exec("ALTER TEXT SEARCH DICTIONARY thes_replay_b.zz_sub (Accept=true)", 0))
		checkScalar(false)
		var original, recreated int64
		if err := consumer.QueryRowContext(p.Ctx, "SELECT 'thes_replay_a.zz_sub'::regdictionary::oid::bigint").Scan(&original); err != nil {
			t.Fatal(err)
		}
		run(consumer, fixture.Exec("DROP TEXT SEARCH DICTIONARY thes_replay_a.zz_sub", 0))
		// A's current same-name identity is now recognizably different from both
		// its original English-stopword state and the consumer's B normalizer.
		run(consumer, fixture.Exec("CREATE TEXT SEARCH DICTIONARY thes_replay_a.zz_sub (TEMPLATE=pg_catalog.simple)", 0))
		if err := consumer.QueryRowContext(p.Ctx, "SELECT 'thes_replay_a.zz_sub'::regdictionary::oid::bigint").Scan(&recreated); err != nil {
			t.Fatal(err)
		}
		if recreated == original {
			t.Fatal("normalizer recreation reused identity", original)
		}
		run(consumer, fixture.Select("SELECT dictinitoption FROM pg_ts_dict WHERE oid='thes_replay_a.zz_sub'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{nil}}))
		for _, prepared := range []bool{false, true} {
			step := ftsConfigurationVector(vectorSQL, "'cards':3 'invitation':2 'order':1", "booking the tickets")
			step.Prepared = prepared
			run(consumer, step)
		}
		options(consumer, sourceOptions)
		if !b.PG {
			var out bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
				t.Fatal("host Dump after producer retirement", err)
			}
			emitted = bytes.Clone(out.Bytes())
			before := bytes.Index(emitted, []byte(`CREATE TEXT SEARCH DICTIONARY "thes_replay_a"."zz_sub"`))
			after := bytes.Index(emitted, []byte(`CREATE TEXT SEARCH DICTIONARY "public"."thes_replay_aa"`))
			if before < 0 || after < before {
				t.Fatal("current recreated normalizer did not precede its consumer", out.String())
			}
			if !bytes.Contains(emitted, []byte(`dictionary = '"thes_replay_a"."zz_sub"'`)) {
				t.Fatal("host replay followed consumer path instead of accepted provenance", out.String())
			}
		} else if len(emitted) == 0 {
			t.Fatal("missing actual native replay stream")
		}
		options(consumer, sourceOptions)
		// Warm the eventual denied principal in a source physical session. That
		// successful initialization must not become imported destination authority.
		run(consumer, fixture.Exec("GRANT USAGE ON SCHEMA thes_replay_a TO thes_replay_denied", 0))
		warm, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer warm.Close()
		defer warm.Raw(func(any) error { return driver.ErrBadConn })
		fixture.RunStep(t, p.Ctx, warm, b.Recorder, fixture.Exec("SET ROLE thes_replay_denied", 0))
		fixture.RunStep(t, p.Ctx, warm, b.Recorder, fixture.Exec("SET search_path=thes_replay_a,public", 0))
		fixture.RunStep(t, p.Ctx, warm, b.Recorder, fixture.Select(scalarSQL, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{1}"}}, "one"))
		var sourceWarmPID int64
		if err := warm.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()::bigint").Scan(&sourceWarmPID); err != nil {
			t.Fatal(err)
		}
		run(consumer, fixture.Exec("REVOKE USAGE ON SCHEMA thes_replay_a FROM thes_replay_denied", 0))
		checkRestored := func(pair *fixture.Pair, backend fixture.Backend, conn *sql.Conn) {
			t.Helper()
			for _, prepared := range []bool{false, true} {
				step := ftsConfigurationVector(vectorSQL, "'booking':1 'the':2 'tickets':3", "booking the tickets")
				step.Prepared = prepared
				fixture.RunStep(t, pair.Ctx, conn, backend.Recorder, step)
			}
			fixture.RunStep(t, pair.Ctx, conn, backend.Recorder, fixture.Select("SELECT dictinitoption FROM pg_ts_dict WHERE oid='public.thes_replay_aa'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"dictfile = 'thesaurus_sample', dictionary = '\"thes_replay_a\".\"zz_sub\"'"}}))
			unauthorized, err := backend.DB.Conn(pair.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer unauthorized.Close()
			defer unauthorized.Raw(func(any) error { return driver.ErrBadConn })
			var activePID, unauthorizedPID int64
			if err := conn.QueryRowContext(pair.Ctx, "SELECT pg_backend_pid()::bigint").Scan(&activePID); err != nil {
				t.Fatal(err)
			}
			if err := unauthorized.QueryRowContext(pair.Ctx, "SELECT pg_backend_pid()::bigint").Scan(&unauthorizedPID); err != nil {
				t.Fatal(err)
			}
			if unauthorizedPID <= 0 || unauthorizedPID == activePID || pair == p && unauthorizedPID == sourceWarmPID {
				t.Fatal("unauthorized import probe borrowed initialized physical session", activePID, sourceWarmPID, unauthorizedPID)
			}
			fixture.RunStep(t, pair.Ctx, unauthorized, backend.Recorder, fixture.Exec("SET ROLE thes_replay_denied", 0))
			for _, query := range []string{scalarSQL, vectorSQL} {
				step := fixture.ErrorDiagnostic(query, thesaurusDiagnostic("42501", "permission denied for schema thes_replay_a"), "one")
				fixture.RunStep(t, pair.Ctx, unauthorized, backend.Recorder, step)
				fixture.RunStep(t, pair.Ctx, unauthorized, backend.Recorder, fixture.Prepared(step))
			}
		}
		if b.PG {
			for _, query := range []string{"DROP TEXT SEARCH CONFIGURATION public.thes_replay_cfg", "DROP TEXT SEARCH DICTIONARY public.thes_replay_aa", "DROP SCHEMA thes_replay_a CASCADE", "DROP SCHEMA thes_replay_b CASCADE"} {
				run(consumer, fixture.Exec(query, 0))
			}
			if _, err := consumer.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual emitted PostgreSQL replay", err)
			}
			checkRestored(p, b, consumer)
			return
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal("same-driver replay authority", err)
		}
		checkRestored(p, b, consumer)
		fresh := fixture.NewFixture(t, false, true)
		fc, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		fb := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
		fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Exec("CREATE ROLE thes_replay_denied", 0))
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal("fresh-driver replay authority", err)
		}
		checkRestored(fresh, fb, fc)
	})
}
