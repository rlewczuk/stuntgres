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

func TestReferencePGStatSubscriptionControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"stat_sub_control_reader", "stat_sub_control_denied"})
	statSubscriptionScenario(t, []string{"stat_sub_control", "stat_sub_control_renamed"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		statSubscriptionCreate(t, p, b, c, "stat_sub_control")
		id := statSubscriptionID(t, p, c, "stat_sub_control")
		statSubscriptionExec(t, p, b, c, "CREATE TABLE stat_subscription_sentinel(v integer)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO stat_subscription_sentinel VALUES(37)", 1))
		query := statSubscriptionSQL + " WHERE subname=$1"
		held := statSubscriptionHeld(t, p, c, query)
		defer held.Statement.Close()
		want := [][]any{statSubscriptionRow(id, "stat_sub_control")}
		statSubscriptionCheck(t, p, b, c, query, want, "stat_sub_control")
		statSubscriptionCheck(t, p, b, held, query, want, "stat_sub_control")
		statSubscriptionSentinel(t, p, b, c)
		if b.PG {
			return // Reset/Dump/Restore are public native controls, not SQL emulation.
		}
		statSubscriptionExec(t, p, b, c, "CREATE ROLE stat_sub_control_reader LOGIN PASSWORD 'role-core-fixture'")
		statSubscriptionExec(t, p, b, c, "CREATE ROLE stat_sub_control_denied")
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			for _, q := range []string{"RESET ROLE", "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_subscription(oid) TO PUBLIC", "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_subscription(oid) FROM stat_sub_control_reader"} {
				if _, err := c.ExecContext(ctx, q); err != nil {
					t.Error("backing ACL cleanup", err)
				}
			}
		}()
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		readerPool, _, readerRecorder := fixture.DatabaseScenarioPool(t, p, false, database, "stat_sub_control_reader")
		defer readerPool.Close()
		reader, err := readerPool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		readerBackend := fixture.Backend{Name: b.Name, DB: readerPool, Recorder: readerRecorder}
		readerHeld := statSubscriptionHeld(t, p, reader, query)
		defer readerHeld.Statement.Close()
		statSubscriptionExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_subscription(oid) FROM PUBLIC")
		statSubscriptionExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_subscription(oid) TO stat_sub_control_reader")
		backingDenied := func() {
			t.Helper()
			statSubscriptionExec(t, p, b, c, "SET ROLE stat_sub_control_denied")
			step := fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function pg_stat_get_subscription"), "stat_sub_control")
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			statSubscriptionExec(t, p, b, c, "RESET ROLE")
		}
		backingDenied()
		statSubscriptionCheck(t, p, readerBackend, readerHeld, query, want, "stat_sub_control")

		defer c.ExecContext(context.Background(), "DROP DATABASE IF EXISTS stat_sub_control_peer")
		statSubscriptionExec(t, p, b, c, "CREATE DATABASE stat_sub_control_peer")
		pool, _, recorder := fixture.DatabaseScenarioPool(t, p, false, "stat_sub_control_peer", "")
		defer pool.Close()
		peer, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		pb := fixture.Backend{Name: b.Name, DB: pool, Recorder: recorder}
		defer peer.ExecContext(context.Background(), "DROP SUBSCRIPTION IF EXISTS stat_sub_control")
		statSubscriptionCreate(t, p, pb, peer, "stat_sub_control")
		peerID := statSubscriptionID(t, p, peer, "stat_sub_control")
		peerWant := statSubscriptionRow(peerID, "stat_sub_control")
		if peerID == id {
			t.Fatal("shared identity collision")
		}
		want = append(want, peerWant)
		statSubscriptionCheck(t, p, b, held, query, want, "stat_sub_control")
		old, err := c.QueryContext(p.Ctx, statSubscriptionSQL+" WHERE subid=$1::oid", id)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		statSubscriptionMetadata(t, old, false)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(dump.String(), "pg_stat_subscription") || strings.Contains(dump.String(), "received_lsn") {
			t.Fatal("Dump serialized monitoring state", dump.String())
		}
		failure := errors.New("subscription monitoring control I/O")
		if err := stuntgres.Dump(p.Ctx, p.Native, fixture.CatalogControlFailingWriter{Err: failure}); !errors.Is(err, failure) {
			t.Fatal("Dump ordinary I/O error", err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: failure}); !errors.Is(err, failure) {
			t.Fatal("Restore ordinary I/O error", err)
		}
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled Restore", err)
		}
		if err := stuntgres.Reset(canceled, p.Native); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled Reset", err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"CREATE TABLE broken(")); err == nil {
			t.Fatal("malformed Restore published partial catalog")
		}
		statSubscriptionCheck(t, p, b, held, query, want, "stat_sub_control")
		statSubscriptionSentinel(t, p, b, c)
		statSubscriptionExec(t, p, b, c, "ALTER SUBSCRIPTION stat_sub_control RENAME TO stat_sub_control_renamed")
		statSubscriptionCheck(t, p, b, held, query, [][]any{peerWant}, "stat_sub_control")
		statSubscriptionCheck(t, p, b, held, query, [][]any{statSubscriptionRow(id, "stat_sub_control_renamed")}, "stat_sub_control_renamed")
		statSubscriptionExec(t, p, b, c, "DROP SUBSCRIPTION stat_sub_control_renamed")
		statSubscriptionCheck(t, p, b, held, query, nil, "stat_sub_control_renamed")
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		statSubscriptionCheck(t, p, b, held, query, [][]any{peerWant}, "stat_sub_control")
		statSubscriptionCheck(t, p, pb, peer, statSubscriptionSQL, [][]any{peerWant})
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		fresh := statSubscriptionID(t, p, c, "stat_sub_control")
		if fresh == id || fresh == peerID {
			t.Fatal("Restore reused old subscription identity", fresh, id, peerID)
		}
		want = [][]any{statSubscriptionRow(fresh, "stat_sub_control"), peerWant}
		statSubscriptionCheck(t, p, b, held, query, want, "stat_sub_control")
		statSubscriptionCheck(t, p, pb, peer, statSubscriptionSQL, want)
		statSubscriptionSentinel(t, p, b, c)
		// Restored symbolic (oid) ACLs retain the real reader role binding.
		backingDenied()
		statSubscriptionCheck(t, p, readerBackend, readerHeld, query, want, "stat_sub_control")
		statSubscriptionExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_subscription(oid) FROM stat_sub_control_reader")
		denied := fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function pg_stat_get_subscription"), "stat_sub_control")
		denied.SelectRows, denied.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, readerHeld, readerRecorder, denied)
		statSubscriptionExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_subscription(oid) TO stat_sub_control_reader")
		statSubscriptionCheck(t, p, readerBackend, readerHeld, query, want, "stat_sub_control")
		// The real pre-rename Rows and metadata survive drop and two generations.
		statSubscriptionRead(t, old, false, [][]any{statSubscriptionRow(id, "stat_sub_control")})
		statSubscriptionExec(t, p, b, c, "DROP SUBSCRIPTION stat_sub_control")
		statSubscriptionCheck(t, p, b, held, query, [][]any{peerWant}, "stat_sub_control")
	})
}
