package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExtensionDropDependencies(t *testing.T) {
	extensionDropCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		var notices []fixture.Diagnostic
		_, owner, recorder := fixture.NoticeReferencePool(t, p, pg, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, owner, recorder, s) }
		for _, q := range []string{"CREATE SCHEMA ext_addon", "CREATE SCHEMA ext_locked", "CREATE TABLE ext_locked.ext_drop_keep(v int)", "CREATE EXTENSION unaccent SCHEMA ext_addon", "CREATE EXTENSION fuzzystrmatch", "CREATE VIEW ext_drop_remaining AS SELECT soundex('Anne') AS v"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO ext_locked.ext_drop_keep VALUES(77)", 1))
		id := extensionTempCapture(t, p, owner)
		extensionTempConsumers(t, p, owner, recorder, "ext_addon")
		detail := "default value for column txt of table ext_temp_consumer depends on function ext_addon.unaccent(text)\n" +
			"constraint ext_temp_check on table ext_temp_consumer depends on function ext_addon.unaccent(text)\n" +
			"view ext_temp_view depends on function ext_addon.unaccent(text)\n" +
			"view ext_temp_chain depends on view ext_temp_view"
		for _, suffix := range []string{"", " RESTRICT"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("DROP EXTENSION unaccent"+suffix, extensionDropRestrict(detail))
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				extensionTempPresent(t, p, c, r, id)
				extensionTempConsumerState(t, p, c, r, true)
				extensionDropNotices(t, notices, nil)
			}
		}
		run(fixture.Exec("DROP EXTENSION unaccent CASCADE", 0))
		extensionDropNotices(t, notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to 4 other objects", Detail: "drop cascades to default value for column txt of table ext_temp_consumer\ndrop cascades to constraint ext_temp_check on table ext_temp_consumer\ndrop cascades to view ext_temp_view\ndrop cascades to view ext_temp_chain"}})
		extensionTempGone(t, p, c, r, id)
		extensionTempConsumerState(t, p, c, r, false)
		extensionTempMissingViews(t, p, c, r)
		run(fixture.Exec("INSERT INTO ext_temp_consumer(id) VALUES(2)", 1))
		run(fixture.Exec("INSERT INTO ext_temp_consumer(id,txt) VALUES(3,'Hôtel')", 1))
		run(fixture.Select("SELECT id,txt FROM ext_temp_consumer ORDER BY id", []string{"id", "txt"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "Hotel"}, {int64(2), nil}, {int64(3), "Hôtel"}}))
		run(fixture.Select("SELECT v FROM ext_drop_remaining", []string{"v"}, []string{"TEXT"}, [][]any{{"A500"}}))
		run(fixture.Select("SELECT v FROM ext_locked.ext_drop_keep", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
		run(fixture.Exec("CREATE TABLE ext_addon.ext_drop_schema_survived(v int)", 0))
	})
}

func TestReferenceExtensionDropLists(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ext_drop_list_reader", "ext_drop_list_owner"})
	extensionDropCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		var notices []fixture.Diagnostic
		_, owner, recorder := fixture.NoticeReferencePool(t, p, pg, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, owner, recorder, s) }
		install := func() {
			run(fixture.Exec("CREATE EXTENSION unaccent", 0))
			run(fixture.Exec("CREATE EXTENSION fuzzystrmatch", 0))
		}
		count := func(n int64) {
			run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname IN ('unaccent','fuzzystrmatch')", []string{"count"}, []string{"INT8"}, [][]any{{n}}))
		}
		install()
		id := extensionTempCapture(t, p, owner)
		run(extensionError("DROP EXTENSION unaccent,ext_drop_missing CASCADE", "42704", `extension "ext_drop_missing" does not exist`))
		extensionTempPresent(t, p, c, r, id)
		count(2)
		run(fixture.Exec("CREATE USER ext_drop_list_reader", 0))
		var database string
		if err := owner.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		grant := "GRANT CREATE ON DATABASE " + fixture.ExtensionTempIdentifier(database) + " TO ext_drop_list_reader"
		run(fixture.Exec(grant, 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO ext_drop_list_reader", 0))
		defer owner.ExecContext(p.Ctx, "REVOKE CREATE ON DATABASE "+fixture.ExtensionTempIdentifier(database)+" FROM ext_drop_list_reader")
		run(fixture.Exec("DROP EXTENSION unaccent", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ext_drop_list_reader", 0))
		defer owner.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run(fixture.Exec("CREATE EXTENSION unaccent", 0))
		run(extensionError("DROP EXTENSION unaccent,fuzzystrmatch CASCADE", "42501", "must be owner of extension fuzzystrmatch"))
		count(2)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("CREATE VIEW ext_drop_shared AS SELECT unaccent(soundex('Anne')) AS v", 0))
		multi := fixture.Diagnostic{Code: "2BP01", Message: "cannot drop desired object(s) because other objects depend on them", Detail: "view ext_drop_shared depends on function unaccent(text)", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
		run(fixture.ImplicitError("DROP EXTENSION unaccent,fuzzystrmatch RESTRICT", multi))
		count(2)
		extensionDropNotices(t, notices, nil)
		run(fixture.Exec("DROP EXTENSION unaccent,fuzzystrmatch CASCADE", 0))
		count(0)
		extensionDropNotices(t, notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to view ext_drop_shared"}})
		notices = nil
		install()
		run(fixture.Exec("DROP EXTENSION unaccent,unaccent,fuzzystrmatch", 0))
		count(0)
		extensionDropNotices(t, notices, nil)
		install()
		run(fixture.Exec("DROP EXTENSION IF EXISTS ext_drop_missing,unaccent,unaccent,fuzzystrmatch", 0))
		count(0)
		extensionDropNotices(t, notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `extension "ext_drop_missing" does not exist, skipping`}})
		notices = nil
		install()
		run(fixture.Exec("CREATE USER ext_drop_list_owner", 0))
		// A missing notice is delivered even if a later installed target fails ownership.
		run(fixture.Exec("SET SESSION AUTHORIZATION ext_drop_list_owner", 0))
		run(extensionError("DROP EXTENSION IF EXISTS ext_drop_missing,unaccent CASCADE", "42501", "must be owner of extension unaccent"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		count(2)
		extensionDropNotices(t, notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `extension "ext_drop_missing" does not exist, skipping`}})
	})
}

func TestReferenceExtensionDropDomainChecks(t *testing.T) {
	extensionDropCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		var notices []fixture.Diagnostic
		_, owner, recorder := fixture.NoticeReferencePool(t, p, pg, &notices)
		defer owner.ExecContext(p.Ctx, "ROLLBACK")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, owner, recorder, s) }
		for _, q := range []string{
			"CREATE EXTENSION unaccent",
			"CREATE DOMAIN ext_drop_checked AS text CONSTRAINT ext_drop_domain_check CHECK (unaccent(VALUE)=VALUE)",
			"CREATE TABLE ext_drop_domain_values(id int,v ext_drop_checked)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO ext_drop_domain_values VALUES(1,'Hotel'),(2,NULL)", 2))
		id := extensionTempCapture(t, p, owner)
		var domain, check, table, procedure int64
		for _, capture := range []struct {
			query string
			id    *int64
		}{
			{"SELECT oid FROM pg_type WHERE typname='ext_drop_checked' AND typnamespace='public'::regnamespace", &domain},
			{"SELECT oid FROM pg_constraint WHERE conname='ext_drop_domain_check' AND contypid='ext_drop_checked'::regtype", &check},
			{"SELECT 'ext_drop_domain_values'::regclass::oid", &table},
			{"SELECT 'unaccent(text)'::regprocedure::oid", &procedure},
		} {
			if err := owner.QueryRowContext(p.Ctx, capture.query).Scan(capture.id); err != nil {
				t.Fatal(capture.query, err)
			}
			if *capture.id <= 0 {
				t.Fatal("missing authentic catalog identity", capture.query)
			}
		}
		state := func(present bool) {
			t.Helper()
			run(fixture.Select("SELECT oid::bigint AS oid FROM pg_type WHERE typname='ext_drop_checked' AND typnamespace='public'::regnamespace", []string{"oid"}, []string{"INT8"}, [][]any{{domain}}))
			run(fixture.Select("SELECT oid::bigint AS oid FROM pg_class WHERE relname='ext_drop_domain_values' AND relnamespace='public'::regnamespace", []string{"oid"}, []string{"INT8"}, [][]any{{table}}))
			var checks [][]any
			var count int64
			if present {
				checks = [][]any{{check, domain}}
				count = 1
			}
			run(fixture.Select("SELECT oid::bigint AS oid,contypid::bigint AS contypid FROM pg_constraint WHERE conname='ext_drop_domain_check' AND contypid=$1", []string{"oid", "contypid"}, []string{"INT8", "INT8"}, checks, domain))
			run(fixture.Select("SELECT count(*) FROM pg_depend WHERE classid=2606 AND objid=$1 AND refclassid=1255 AND refobjid=$2 AND deptype='n'", []string{"count"}, []string{"INT8"}, [][]any{{count}}, check, procedure))
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT id,v::text AS v FROM ext_drop_domain_values ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "Hotel"}, {int64(2), nil}})
				s.Prepared = prepared
				run(s)
			}
		}
		// Independently specified fields confirmed by the PostgreSQL18.6 oracle.
		restrict := extensionDropRestrict("constraint ext_drop_domain_check depends on function unaccent(text)")
		violation := fixture.Diagnostic{Code: "23514", Message: `value for domain ext_drop_checked violates check constraint "ext_drop_domain_check"`, Schema: "public", DataType: "ext_drop_checked", Constraint: "ext_drop_domain_check"}
		enforced := func() {
			t.Helper()
			for _, prepared := range []bool{false, true} {
				for _, value := range []any{"Hotel", nil} {
					s := fixture.Select("SELECT ($1::text::ext_drop_checked)::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{value}}, value)
					s.Prepared = prepared
					run(s)
				}
				s := fixture.ImplicitError("SELECT ($1::text::ext_drop_checked)::text AS v", violation)
				s.Args, s.Prepared, s.SelectRows, s.ErrorStage = []any{"Hôtel"}, prepared, true, "execute"
				run(s)
			}
		}
		q := "SELECT unaccent($1::text)"
		stmt, err := owner.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: owner, Statement: stmt, SQL: q}
		call := func() {
			t.Helper()
			for _, value := range []any{"Hôtel", nil} {
				var want any = "Hotel"
				if value == nil {
					want = nil
				}
				s := fixture.Select(q, []string{"unaccent"}, []string{"TEXT"}, [][]any{{want}}, value)
				run(s)
				run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, recorder, s)
			}
		}
		state(true)
		enforced()
		call()
		for _, suffix := range []string{"", " RESTRICT"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("DROP EXTENSION unaccent"+suffix, restrict)
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				extensionTempPresent(t, p, owner, recorder, id)
				state(true)
				extensionDropNotices(t, notices, nil)
			}
		}
		cascadeNotice := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to constraint ext_drop_domain_check"}}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Prepared(fixture.Exec("DROP EXTENSION unaccent CASCADE", 0)))
		extensionTempGone(t, p, owner, recorder, id)
		state(false)
		extensionTempPresent(t, p, c, r, id)
		extensionDropNotices(t, notices, cascadeNotice)
		run(fixture.Exec("ROLLBACK", 0))
		extensionTempPresent(t, p, owner, recorder, id)
		state(true)
		enforced()
		call()
		notices = nil
		run(fixture.Exec("DROP EXTENSION unaccent CASCADE", 0))
		extensionTempGone(t, p, owner, recorder, id)
		state(false)
		extensionDropNotices(t, notices, cascadeNotice)
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT ($1::text::ext_drop_checked)::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"Hôtel"}}, "Hôtel")
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Prepared(fixture.Exec("INSERT INTO ext_drop_domain_values VALUES(3,'Hôtel')", 1)))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT id,v::text AS v FROM ext_drop_domain_values ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "Hotel"}, {int64(2), nil}, {int64(3), "Hôtel"}})
			s.Prepared = prepared
			run(s)
		}
	})
}
