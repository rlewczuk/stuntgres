package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

type lifecycleReadFailure struct{}

func (lifecycleReadFailure) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestReferenceAggregateLifecyclePersistence(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"al_owner"})
	fixture.SchemaReferenceCleanup(t, "al_dst")
	var emitted string
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		lifecycleExec(t, p, b, c, "CREATE ROLE al_owner")
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		if b.PG {
			if emitted == "" {
				t.Fatal("missing actual native dump")
			}
			// The fixture recreates PostgreSQL's public schema without its
			// normal PUBLIC USAGE grant. The aggregate owner's support routine
			// remains in public; provide that destination-environment privilege
			// before replaying the actual, unchanged native bytes.
			lifecycleExec(t, p, b, c, "GRANT USAGE ON SCHEMA public TO al_owner")
			if _, err := c.ExecContext(p.Ctx, emitted); err != nil {
				t.Fatal(err)
			}
			lifecycleExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		} else {
			lifecycleSetup(t, p, b, c)
			lifecycleExec(t, p, b, c, "CREATE SCHEMA al_dst AUTHORIZATION al_owner", "CREATE VIEW al_view AS SELECT al_sum(x) AS v FROM al_input", "ALTER AGGREGATE al_sum(integer) RENAME TO al_renamed", "ALTER AGGREGATE al_renamed(integer) SET SCHEMA al_dst", "ALTER AGGREGATE al_dst.al_renamed(integer) OWNER TO al_owner")
		}
		check := func() {
			lifecycleValues(t, p, b, c, "al_dst.al_renamed")
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT v FROM al_view", int64(6)))
			id := lifecycleOID(t, p, c, "al_dst.al_renamed(integer)")
			lifecycleFacts(t, p, b, c, "al_dst.al_renamed", id)
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT proowner='al_owner'::regrole AS owned,pronamespace='al_dst'::regnamespace AS located FROM pg_proc WHERE oid=$1::oid", []string{"owned", "located"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, id))
			createAggregateRun(t, p, b, c, lifecycleRestrict("DROP AGGREGATE al_dst.al_renamed(integer)", "view al_view depends on function al_dst.al_renamed(integer)"))
		}
		check()
		if b.PG {
			return
		}
		retained, err := c.QueryContext(p.Ctx, "SELECT v FROM al_view")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		var dump bytes.Buffer
		if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = dump.String()
		if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check()
		}
		lifecycleExec(t, p, b, c, "CREATE TABLE al_sentinel(v integer)")
		createAggregateRun(t, p, b, c, fixture.Exec("INSERT INTO al_sentinel VALUES(91)", 1))
		for _, reader := range []io.Reader{strings.NewReader(emitted + "\nCREATE AGGREGATE al_bad(integer) (SFUNC=al_absent,STYPE=int4);"), io.MultiReader(strings.NewReader(emitted), lifecycleReadFailure{})} {
			if err = stuntgres.Restore(p.Ctx, p.Native, reader); err == nil {
				t.Fatal("failed replacement published")
			}
			check()
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT v FROM al_sentinel", int64(91)))
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err = stuntgres.Restore(ctx, p.Native, strings.NewReader(emitted)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		check()
		fixture.ScalarRows(t, false, retained, []string{"v"}, []string{"INT4"}, [][]any{{int64(6)}}, false)
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("aggregate-lifecycle-fresh/" + t.Name())
		if err != nil {
			t.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		fixture.RunStep(t, p.Ctx, fresh, b.Recorder, fixture.Exec("CREATE ROLE al_owner", 0))
		if err = stuntgres.Restore(p.Ctx, fresh, strings.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		for _, s := range []fixture.Step{createAggregateValue("SELECT v FROM al_view", int64(6)), fixture.Select("SELECT al_sum(x::bigint) AS v FROM al_input", []string{"v"}, []string{"INT8"}, [][]any{{int64(106)}}), lifecycleRestrict("DROP AGGREGATE al_dst.al_renamed(integer)", "view al_view depends on function al_dst.al_renamed(integer)")} {
			fixture.RunStep(t, p.Ctx, fresh, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fresh, b.Recorder, fixture.Prepared(s))
		}
		fixture.RunStep(t, p.Ctx, fresh, b.Recorder, fixture.Exec("DROP AGGREGATE al_dst.al_renamed(integer) CASCADE", 0))
	})
}
