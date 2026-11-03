package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Native replacement has no PostgreSQL SQL equivalent. Both callers keep this
// phase inside their otherwise shared dual-mode public scenario.
func slruNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held fixture.HeldStatement) {
	t.Helper()
	statsExec(t, p, b, c, "SET application_name='slru controls'", 0)
	var pid int64
	var principal, mode string
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid(),current_user,current_setting('stats_fetch_consistency')").Scan(&pid, &principal, &mode); err != nil {
		t.Fatal(err)
	}
	before := slruGet(t, p, b, held)
	slruGetterRawACL(t, p, b, c, nil)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "pg_stat_slru") || strings.Contains(dump.String(), "stats_reset") || strings.Contains(dump.String(), "pg_stat_get_slru") {
		t.Fatal("Dump serialized monitoring history or implicit getter ACL repair", dump.String())
	}
	slruSame(t, before, slruGet(t, p, b, held))
	slruGetterRawACL(t, p, b, c, nil)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("not valid SQL")); err == nil {
		t.Fatal("invalid Restore succeeded")
	}
	slruSame(t, before, slruGet(t, p, b, held))
	slruGetterRawACL(t, p, b, c, nil)
	slruSentinel(t, p, b, c)
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	for _, control := range []func() error{
		func() error { return stuntgres.Reset(ctx, p.Native) },
		func() error { return stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())) },
	} {
		if err := control(); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled native control", err)
		}
		slruSame(t, before, slruGet(t, p, b, held))
		slruGetterRawACL(t, p, b, c, nil)
		slruSentinel(t, p, b, c)
	}
	// Leave actual database/sql Rows unconsumed across each accepted replacement.
	// The held statement must instead execute against the newly installed owner.
	for _, restore := range []bool{false, true} {
		old := slruGet(t, p, b, held)
		retained, err := held.QueryContext(p.Ctx, slruSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		lower := slruDistinctClock(t, p, c, old.rows["other"].reset)
		if restore {
			err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes()))
		} else {
			err = stuntgres.Reset(p.Ctx, p.Native)
		}
		if err != nil {
			t.Fatal(err)
		}
		upper := slruClock(t, p, c)
		slruSame(t, old, slruRead(t, retained, false))
		resetReplicationVoid(t, p, b, c, "SELECT pg_stat_clear_snapshot()", "pg_stat_clear_snapshot")
		fresh := slruGet(t, p, b, held)
		slruEvent(t, old, fresh, lower, upper)
		for name, row := range fresh.rows {
			if row.technical != [7]int64{} || !row.reset.After(old.rows[name].reset) || row.reset.Before(lower) || row.reset.After(upper) {
				t.Fatal("replacement must reset every category at a genuine bounded event", name, row, lower, upper)
			}
		}
		slruSame(t, fresh, slruGet(t, p, b, c))
		slruGetterRawACL(t, p, b, c, nil)
		var gotPID int64
		var gotPrincipal, gotMode, application string
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid(),current_user,current_setting('stats_fetch_consistency'),current_setting('application_name')").Scan(&gotPID, &gotPrincipal, &gotMode, &application); err != nil {
			t.Fatal(err)
		}
		if gotPID != pid || gotPrincipal != principal || gotMode != mode || application != "slru controls" {
			t.Fatal("native control replaced live session authority/settings", gotPID, gotPrincipal, gotMode, application)
		}
		if restore {
			slruSentinel(t, p, b, c)
		} else {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_class WHERE relname='slru_sentinel' AND relnamespace='public'::regnamespace", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
	}
	slruNativeGetterACLControls(t, p, b, c, held, dump.Bytes())
}

func slruGetterRawACL(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, want any) {
	t.Helper()
	// A fixed expected raw value distinguishes NULL, explicit empty and an
	// explicit effective default independently of privilege checks/dump text.
	step := fixture.Select("SELECT proacl::text AS acl FROM pg_catalog.pg_proc WHERE oid=2306", []string{"acl"}, []string{"TEXT"}, [][]any{{want}})
	fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
}

func slruGetterRoleAccess(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held fixture.HeldStatement, role string, allowed bool) {
	t.Helper()
	before := slruGet(t, p, b, c)
	statsExec(t, p, b, c, "SET ROLE "+role, 0)
	defer c.ExecContext(context.Background(), "RESET ROLE")
	if allowed {
		slruSame(t, before, slruGet(t, p, b, c))
		slruSame(t, before, slruGet(t, p, b, held))
	} else {
		denied := fixture.ErrorDiagnostic(slruSQL, activityDiagnostic("42501", "permission denied for function pg_stat_get_slru", 0))
		denied.SelectRows, denied.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, denied)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
	}
	statsExec(t, p, b, c, "RESET ROLE", 0)
	slruSame(t, before, slruGet(t, p, b, held))
}

func slruNativeGetterACLControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held fixture.HeldStatement, implicit []byte) {
	t.Helper()
	const identity = "pg_catalog.pg_stat_get_slru()"
	const locator = ` WHERE oid=E'"pg_catalog"."pg_stat_get_slru"()'::pg_catalog.regprocedure;`
	// User roles must be recreated and their grantor/grantee references remapped
	// by real Restore, rather than relying only on fixed predefined principals.
	statsExec(t, p, b, c, "CREATE ROLE slru_control_reader", 0)
	statsExec(t, p, b, c, "CREATE ROLE slru_control_denied", 0)
	defer c.ExecContext(context.Background(), "RESET ROLE")
	for _, state := range []struct {
		name   string
		setup  []string
		raw    string
		reader bool
		public bool
	}{
		{
			name: "restricted",
			setup: []string{
				"REVOKE EXECUTE ON FUNCTION " + identity + " FROM PUBLIC",
				"GRANT EXECUTE ON FUNCTION " + identity + " TO slru_control_reader WITH GRANT OPTION",
			},
			raw:    "{postgres=X/postgres,slru_control_reader=X*/postgres}",
			reader: true,
		},
		{
			name: "explicit_empty",
			setup: []string{"REVOKE ALL ON FUNCTION " + identity + " FROM postgres,slru_control_reader"},
			raw:   "{}",
		},
		{
			name: "explicit_default",
			setup:  []string{"GRANT EXECUTE ON FUNCTION " + identity + " TO postgres,PUBLIC"},
			raw:    "{postgres=X/postgres,=X/postgres}",
			reader: true,
			public: true,
		},
	} {
		t.Run("getter_acl_"+state.name, func(t *testing.T) {
			for _, query := range state.setup {
				statsExec(t, p, b, c, query, 0)
			}
			slruGetterRawACL(t, p, b, c, state.raw)
			var saved bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
				t.Fatal(err)
			}
			repairs := 0
			for line := range strings.SplitSeq(saved.String(), "\n") {
				if strings.HasPrefix(line, "UPDATE pg_catalog.pg_proc SET proacl=") && strings.HasSuffix(line, locator) {
					repairs++
				}
			}
			if repairs != 1 {
				t.Fatal("explicit getter ACL needs exactly one symbolic repair", saved.String())
			}
			// Establish the opposite PUBLIC boundary before replacement. Keeping
			// the source ACL or silently falling back to seed rights cannot pass.
			change := "GRANT EXECUTE ON FUNCTION " + identity + " TO PUBLIC"
			if state.public {
				change = "REVOKE EXECUTE ON FUNCTION " + identity + " FROM PUBLIC"
			}
			statsExec(t, p, b, c, change, 0)
			slruGetterRoleAccess(t, p, b, c, held, "slru_control_denied", !state.public)
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
			resetReplicationVoid(t, p, b, c, "SELECT pg_stat_clear_snapshot()", "pg_stat_clear_snapshot")
			slruGetterRawACL(t, p, b, c, state.raw)
			slruGetterRoleAccess(t, p, b, c, held, "slru_control_reader", state.reader)
			slruGetterRoleAccess(t, p, b, c, held, "slru_control_denied", state.public)
			slruSentinel(t, p, b, c)
		})
	}
	// Round-trip the original implicit ACL after explicit-empty/default state,
	// not just a fresh engine's seed. The old held SELECT must regain PUBLIC
	// access while the raw ACL becomes NULL, rather than an explicit default.
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(implicit)); err != nil {
		t.Fatal(err)
	}
	resetReplicationVoid(t, p, b, c, "SELECT pg_stat_clear_snapshot()", "pg_stat_clear_snapshot")
	slruGetterRawACL(t, p, b, c, nil)
	slruGetterRoleAccess(t, p, b, c, held, "pg_signal_backend", true)
	slruSentinel(t, p, b, c)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(redump.String(), "pg_stat_get_slru") {
		t.Fatal("restored implicit getter ACL needs no repair", redump.String())
	}
}

func TestReferencePGStatSLRUControls(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				if b.PG { return } // Native controls are intentionally not PostgreSQL SQL.
				statsExec(t, p, b, c, "SET stats_fetch_consistency="+mode, 0)
				statsExec(t, p, b, c, "CREATE TABLE slru_sentinel(v integer)", 0)
				statsExec(t, p, b, c, "INSERT INTO slru_sentinel VALUES(37)", 1)
				held := slruHeld(t, p, c, slruSQL); defer held.Statement.Close()
				slruNativeControls(t, p, b, c, held)
			})
		})
	}
}
