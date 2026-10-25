package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func lifecycleIdentity(t *testing.T, smoke bool) {
	fixture.RoleReferenceCleanup(t, []string{"al_owner"})
	fixture.SchemaReferenceCleanup(t, "al_dst")
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var notices []fixture.Diagnostic
		_, noticeConn, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		c = noticeConn
		b.Recorder = rec
		lifecycleSetup(t, p, b, c)
		lifecycleExec(t, p, b, c, "CREATE ROLE al_owner", "CREATE SCHEMA al_dst AUTHORIZATION al_owner", "CREATE VIEW al_view AS SELECT al_sum(x) AS v FROM al_input")
		id := lifecycleOID(t, p, c, "al_sum(integer)")
		sibling := lifecycleOID(t, p, c, "al_sum(bigint)")
		lifecycleValues(t, p, b, c, "al_sum")
		var retained *sql.Rows
		if !b.PG {
			var err error
			retained, err = c.QueryContext(p.Ctx, "SELECT v FROM al_view")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
		}
		const oldQuery = "SELECT al_sum(x)::integer AS v FROM al_input"
		held, err := c.PrepareContext(p.Ctx, oldQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: oldQuery}, b.Recorder, createAggregateValue(oldQuery, int64(6)))
		currentName := "al_sum"
		for _, change := range []struct{ q, name string }{
			{"ALTER AGGREGATE al_sum(IN ignored int) RENAME TO al_renamed", "al_renamed"},
			{"ALTER AGGREGATE al_renamed(integer) SET SCHEMA al_dst", "al_dst.al_renamed"},
			{"ALTER AGGREGATE al_dst.al_renamed(int4) OWNER TO al_owner", "al_dst.al_renamed"},
		} {
			stmt, err := c.PrepareContext(p.Ctx, change.q)
			if err != nil {
				t.Fatal(err)
			}
			lifecycleFacts(t, p, b, c, currentName, id) // Prepared utility must not publish.
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: change.q}, b.Recorder, fixture.Exec(change.q, 0))
			stmt.Close()
			lifecycleFacts(t, p, b, c, change.name, id)
			lifecycleValues(t, p, b, c, change.name)
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT v FROM al_view", int64(6)))
			if lifecycleOID(t, p, c, "al_sum(bigint)") != sibling {
				t.Fatal("sibling identity changed")
			}
			currentName = change.name
		}
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT p.proowner='al_owner'::regrole AS owner,p.pronamespace='al_dst'::regnamespace AS namespace FROM pg_proc p WHERE p.oid=$1::oid", []string{"owner", "namespace"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, id))
		lifecycleExec(t, p, b, c, "SET search_path=al_dst,public")
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT al_renamed(x) AS v FROM al_input", int64(6)))
		lifecycleExec(t, p, b, c, "SET search_path=public")
		// The remaining bigint overload is not an alias for the removed integer one.
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: oldQuery}, b.Recorder, createAggregateValue(oldQuery, int64(106)))
		lifecycleError(t, p, b, c, "ALTER AGGREGATE al_sum(integer) RENAME TO bad", "42883", "aggregate al_sum(integer) does not exist")
		lifecycleError(t, p, b, c, "ALTER AGGREGATE public.al_renamed(integer) RENAME TO bad", "42883", "aggregate public.al_renamed(integer) does not exist")
		restrict := lifecycleRestrict("DROP AGGREGATE al_dst.al_renamed(integer)", "view al_view depends on function al_dst.al_renamed(integer)")
		createAggregateRun(t, p, b, c, restrict, fixture.Prepared(restrict))
		if smoke && !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			lifecycleValues(t, p, b, c, "al_dst.al_renamed")
			createAggregateRun(t, p, b, c, restrict)
			id = lifecycleOID(t, p, c, "al_dst.al_renamed(integer)")
			lifecycleFacts(t, p, b, c, "al_dst.al_renamed", id)
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT proowner='al_owner'::regrole AS owned,pronamespace='al_dst'::regnamespace AS located FROM pg_proc WHERE oid=$1::oid", []string{"owned", "located"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, id))
		}
		if retained != nil {
			fixture.ScalarRows(t, false, retained, []string{"v"}, []string{"INT4"}, [][]any{{int64(6)}}, false)
		}
		notices = nil
		lifecycleExec(t, p, b, c, "DROP AGGREGATE al_dst.al_renamed(integer) CASCADE")
		cascadeNotice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to view al_view"}
		if len(notices) != 1 || notices[0] != cascadeNotice {
			t.Fatalf("cascade notices=%#v want=%#v", notices, cascadeNotice)
		}
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='al_view' AND relnamespace='public'::regnamespace", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT al_sum(x::bigint) AS v FROM al_input", []string{"v"}, []string{"INT8"}, [][]any{{int64(106)}}))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM al_input", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname='al_add' AND pronamespace='public'::regnamespace", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		notices = nil
		const missing = "DROP AGGREGATE IF EXISTS al_dst.al_renamed(integer)"
		stmt, err := c.PrepareContext(p.Ctx, missing)
		if err != nil {
			t.Fatal(err)
		}
		if len(notices) != 0 {
			t.Fatal("Prepare emitted missing notice", notices)
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: missing}, b.Recorder, fixture.Exec(missing, 0))
		stmt.Close()
		missingNotice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "aggregate al_dst.al_renamed(pg_catalog.int4) does not exist, skipping"}
		if len(notices) != 1 || notices[0] != missingNotice {
			t.Fatalf("missing notices=%#v want=%#v", notices, missingNotice)
		}
	})
}
func TestReferenceAggregateLifecycleIdentity(t *testing.T)     { lifecycleIdentity(t, false) }
func TestReferenceAggregateLifecycleFeatureSmoke(t *testing.T) { lifecycleIdentity(t, true) }
