package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantSequenceColumns(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader,og_object_other", 0))
		run(fixture.Exec("CREATE SEQUENCE og_seq_columns START 101", 0))
		run(fixture.Exec("CREATE SEQUENCE og_seq_columns_late START 201", 0))
		owner, _ := fixture.RoleGrantEnvironment(t, p, c)
		last := fixture.Select("SELECT last_value FROM og_seq_columns", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(101)}})
		log := fixture.Select("SELECT log_cnt FROM og_seq_columns", []string{"log_cnt"}, []string{"INT8"}, [][]any{{int64(0)}})
		called := fixture.Select("SELECT is_called FROM og_seq_columns", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}})
		star := fixture.Select("SELECT * FROM og_seq_columns", []string{"last_value", "log_cnt", "is_called"}, []string{"INT8", "INT8", "BOOL"}, [][]any{{int64(101), int64(0), false}})
		one := fixture.Select("SELECT 1 AS v FROM og_seq_columns", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}})
		count := fixture.Select("SELECT count(*) FROM og_seq_columns", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}})
		late := fixture.Select("SELECT last_value FROM og_seq_columns_late", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(201)}})
		// These are actual retained statements, not per-phase Prepare calls. Warm
		// them as the reader once the respective column rights become available.
		held := make(map[string]fixture.HeldStatement)
		for _, s := range []fixture.Step{last, log, called, star, one, count, late} {
			stmt, err := c.PrepareContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(s.Query, err)
			}
			defer stmt.Close()
			held[s.Query] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: s.Query}
		}
		read := func(s fixture.Step, allowed bool, name string) {
			if !allowed {
				s = fixture.ImplicitError(s.Query, fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence " + name})
				s.SelectRows, s.ErrorStage = true, "execute"
			}
			run(s)
			fixture.RunStep(t, p.Ctx, held[s.Query], b.Recorder, s)
		}
		reader := func(body func()) { fixture.RoleGrantActor(run, "og_object_reader", body) }
		columns := func(lastAllowed, siblingsAllowed bool) {
			reader(func() {
				read(last, lastAllowed, "og_seq_columns")
				read(log, siblingsAllowed, "og_seq_columns")
				read(called, siblingsAllowed, "og_seq_columns")
				read(star, lastAllowed && siblingsAllowed, "og_seq_columns")
				// No selected user column requires SELECT on any live column,
				// not unconditional admission and not SELECT on every column.
				read(one, lastAllowed || siblingsAllowed, "og_seq_columns")
				read(count, lastAllowed || siblingsAllowed, "og_seq_columns")
			})
		}
		generatorsDenied := func() {
			reader(func() {
				for _, q := range []string{
					"SELECT nextval('og_seq_columns')",
					"SELECT setval('og_seq_columns',999)",
					"SELECT currval('og_seq_columns')",
				} {
					fixture.RoleGrantPermissionDenied(run, q, "sequence", "og_seq_columns")
				}
			})
			// No generator has been allowed to run: initial log_cnt is exactly
			// zero in the released sequence row, not a learned physical counter.
			run(star)
		}
		columns(false, false)
		run(fixture.Exec("GRANT SELECT(last_value) ON TABLE og_seq_columns TO og_object_reader", 0))
		columns(true, false)
		generatorsDenied()
		run(fixture.Exec("GRANT SELECT(log_cnt,is_called) ON og_seq_columns TO og_object_reader", 0))
		columns(true, true)
		generatorsDenied()
		// Whole-object SELECT independently covers every selected column, but
		// revoking it also clears the reader's earlier column grants/options.
		run(fixture.Exec("GRANT SELECT ON SEQUENCE og_seq_columns TO og_object_reader", 0))
		columns(true, true)
		run(fixture.Exec("REVOKE SELECT ON TABLE og_seq_columns FROM og_object_reader", 0))
		columns(false, false)
		run(fixture.Exec("GRANT SELECT(last_value) ON og_seq_columns TO og_object_reader WITH GRANT OPTION", 0))
		columns(true, false)
		run(fixture.Exec("REVOKE ALL ON SEQUENCE og_seq_columns FROM og_object_reader", 0))
		columns(false, false)

		// A late column or relation error must publish no earlier ACL leaf,
		// including against a retained handle that was previously authorized.
		fixture.RoleGrantError(run, "GRANT SELECT(last_value,missing) ON og_seq_columns TO og_object_reader", "42703", `column "missing" of relation "og_seq_columns" does not exist`)
		columns(false, false)
		fixture.RoleGrantError(run, "GRANT SELECT(last_value) ON og_seq_columns,og_seq_columns_late,og_seq_columns_missing TO og_object_reader", "42P01", `relation "og_seq_columns_missing" does not exist`)
		columns(false, false)
		reader(func() { read(late, false, "og_seq_columns_late") })

		// A genuine column grant option has same-column dependencies. RESTRICT
		// must leave both the reader and its delegated consumer authorized.
		run(fixture.Exec("GRANT SELECT(last_value) ON TABLE og_seq_columns TO og_object_reader WITH GRANT OPTION", 0))
		columns(true, false)
		reader(func() { run(fixture.Exec("GRANT SELECT(last_value) ON og_seq_columns TO og_object_other", 0)) })
		other := func(allowed bool) {
			fixture.RoleGrantActor(run, "og_object_other", func() { read(last, allowed, "og_seq_columns") })
		}
		other(true)
		restrict := fixture.ImplicitError("REVOKE GRANT OPTION FOR SELECT(last_value) ON og_seq_columns FROM og_object_reader RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."})
		restrict.ErrorStage = "execute"
		run(restrict)
		columns(true, false)
		other(true)
		// Preserve a distinct owner-origin grant when cascading reader-origin
		// delegation; removing that independent origin then removes all use.
		run(fixture.Exec("GRANT SELECT(last_value) ON og_seq_columns TO og_object_other", 0))
		run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT(last_value) ON og_seq_columns FROM og_object_reader CASCADE", 0))
		columns(true, false)
		other(true)
		run(fixture.Exec("REVOKE SELECT(last_value) ON og_seq_columns FROM og_object_other", 0))
		other(false)
		run(fixture.Exec("REVOKE SELECT ON SEQUENCE og_seq_columns FROM og_object_reader", 0))
		columns(false, false)
		// Whole-object revocation must remove grant options as well as use.
		reader(func() {
			fixture.RoleGrantError(run, "GRANT SELECT(last_value) ON og_seq_columns TO og_object_other", "42501", `permission denied for column "last_value" of relation "og_seq_columns"`)
		})
		other(false)

		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		warning := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "0LP01", Message: `sequence "og_seq_columns" only supports SELECT column privileges`}
		columnACL := func() {
			// Existing public raw-ACL getter, with the actual attribute number:
			// unsupported INSERT/UPDATE/REFERENCES must never be stored.
			run(fixture.Select("SELECT unnest(pg_get_acl(1259,'og_seq_columns'::regclass,1)) AS acl", []string{"acl"}, []string{"ACLITEM"}, [][]any{{"og_object_reader=r/" + owner}}))
		}
		for _, request := range []string{
			"SELECT(last_value),UPDATE(last_value)",
			"SELECT(last_value),INSERT(last_value)",
			"SELECT(last_value),REFERENCES(last_value)",
		} {
			fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "GRANT "+request+" ON TABLE og_seq_columns TO og_object_reader", warning)
			columnACL()
			columns(true, false)
			generatorsDenied()
			fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "REVOKE "+request+" ON TABLE og_seq_columns FROM og_object_reader", warning)
			columns(false, false)
		}
		for _, privilege := range []string{"UPDATE", "INSERT", "REFERENCES"} {
			fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "GRANT "+privilege+"(last_value) ON og_seq_columns TO og_object_reader", warning)
			columns(false, false)
			generatorsDenied()
			fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "REVOKE "+privilege+"(last_value) ON og_seq_columns FROM og_object_reader", warning)
			columns(false, false)
		}
		// Mask warnings follow syntactic privilege items: two unsupported items
		// produce two warnings, not one merged warning or three column warnings.
		fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "GRANT SELECT(last_value),UPDATE(last_value,log_cnt),REFERENCES(last_value) ON TABLE og_seq_columns TO og_object_reader", warning, warning)
		columnACL()
		columns(true, false)
		generatorsDenied()
		run(fixture.Exec("REVOKE SELECT ON SEQUENCE og_seq_columns FROM og_object_reader", 0))
		columns(false, false)

		// Whole-object authority diagnostics precede explicit column masking.
		// USAGE provides some whole-object rights, but no SELECT grant option.
		// Do not call generators while USAGE is installed: this phase must not
		// change the independently specified pristine sequence row.
		run(fixture.Exec("GRANT USAGE ON SEQUENCE og_seq_columns TO og_object_reader", 0))
		nrun := func(s fixture.Step) {
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, nc, recorder, s)
		}
		nrun(fixture.Exec("SET SESSION AUTHORIZATION og_object_reader", 0))
		fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "GRANT SELECT,UPDATE(last_value) ON TABLE og_seq_columns TO og_object_other",
			fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01007", Message: `no privileges were granted for "og_seq_columns"`},
			warning)
		nrun(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		other(false)
		columns(false, false)
		run(fixture.Exec("REVOKE USAGE ON SEQUENCE og_seq_columns FROM og_object_reader", 0))
		generatorsDenied()
		other(false)
		// Explicit SEQUENCE rejects column privileges at execution, even though
		// TABLE and implicit relation syntax accept this genuine sequence.
		fixture.RoleGrantError(run, "GRANT SELECT(last_value) ON SEQUENCE og_seq_columns TO og_object_reader", "0LP01", "column privileges are only valid for relations")
		columns(false, false)
		// ALL on a column is masked to SELECT with the same exact warning.
		fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "GRANT ALL(last_value) ON TABLE og_seq_columns TO og_object_reader", warning)
		columnACL()
		columns(true, false)
		fixture.RoleGrantError(run, "REVOKE SELECT(last_value) ON SEQUENCE og_seq_columns FROM og_object_reader", "0LP01", "column privileges are only valid for relations")
		columns(true, false)
		generatorsDenied()
		fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "REVOKE ALL(last_value) ON TABLE og_seq_columns FROM og_object_reader", warning)
		columns(false, false)
		// Keep whole-object mask diagnostics distinct from column diagnostics.
		fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "GRANT SELECT,INSERT ON TABLE og_seq_columns TO og_object_reader",
			fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "0LP01", Message: `sequence "og_seq_columns" only supports USAGE, SELECT, and UPDATE privileges`})
		columns(true, true)
		run(fixture.Exec("REVOKE ALL ON SEQUENCE og_seq_columns FROM og_object_reader", 0))
		columns(false, false)
		run(fixture.Exec("GRANT SELECT(last_value) ON TABLE og_seq_columns TO og_object_reader", 0))
		columns(true, false)
		generatorsDenied()
	})
}
