package reference_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func originStateSubscriptionIdentity(t *testing.T, p *fixture.Pair, e fixture.Executor, name string) (int64, string) {
	t.Helper()
	oid := originStateNumber(t, p, e, "SELECT oid::bigint FROM pg_subscription WHERE subname=$1 AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())", name)
	canonical := fmt.Sprint("pg_", oid)
	return originStateID(t, p, e, canonical), canonical
}
func TestReferenceReplicationOriginStateCatalogControls(t *testing.T) {
	databases := []string{"state_control_peer"}
	fixture.DatabaseReferenceCleanup(t, &databases)
	p := subscriptionScenario(t, "state_control_sub", "state_control_missing", "state_control_pending")
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			originStateCleanup(t, b.DB, "state_control", "state_control_zero", "state_control_stale")
			id := originStateCreate(t, p, b, b.DB, "state_control")
			originStateCreate(t, p, b, b.DB, "state_control_zero")
			originStateRun(t, p, b, b.DB, fixture.Exec("CREATE TABLE state_control_sentinel(n integer)", 0))
			originStateRun(t, p, b, b.DB, fixture.Exec("INSERT INTO state_control_sentinel VALUES(41)", 1))
			originCreate(t, p, b, b.DB, "state_control_sub", false)
			_, canonical := originIdentity(t, p, b.DB, "state_control_sub")
			originCreate(t, p, b, b.DB, "state_control_missing", false)
			_, missing := originIdentity(t, p, b.DB, "state_control_missing")
			originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_drop($1::text)", 1, missing))
			for _, name := range []string{"state_control", canonical} {
				originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_advance($1::text,'80000000/4')", 1, name))
			}
			originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_advance('state_control_zero','0/0')", 1))
			originStateProgress(t, p, b, b.DB, "state_control", "80000000/4")
			originStateProgress(t, p, b, b.DB, canonical, "80000000/4")
			originStateRun(t, p, b, b.DB, originStateScalar("SELECT pg_replication_origin_oid($1::text) AS v", "OID", nil, missing))
			if b.PG {
				return
			}
			settings, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer settings.Close()
			originStateRun(t, p, b, settings, fixture.Exec("SET application_name='state_origin_control'", 0))
			var first, second bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &first); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Dump(p.Ctx, p.Native, &second); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(first.Bytes(), second.Bytes()) {
				t.Fatal("nondeterministic logical origin dump")
			}
			for _, statement := range []string{
				"SELECT pg_catalog.pg_replication_origin_create(E'state_control');\n",
				"SELECT pg_catalog.pg_replication_origin_advance(E'state_control', '80000000/4'::pg_catalog.pg_lsn);\n",
				"SELECT pg_catalog.pg_replication_origin_create(E'state_control_zero');\n",
				"SELECT pg_catalog.pg_replication_origin_advance(E'state_control_zero', '0/0'::pg_catalog.pg_lsn);\n",
				"SELECT pg_catalog.pg_replication_origin_drop('pg_' || oid::pg_catalog.text) FROM pg_catalog.pg_subscription WHERE subname = E'state_control_missing';\n",
				"SELECT pg_catalog.pg_replication_origin_advance('pg_' || oid::pg_catalog.text, '80000000/4'::pg_catalog.pg_lsn) FROM pg_catalog.pg_subscription WHERE subname = E'state_control_sub';\n",
			} {
				if strings.Count(first.String(), statement) != 1 {
					t.Fatalf("logical origin dump must contain exactly one %q", statement)
				}
			}
			heldSQL := "SELECT pg_replication_origin_progress('state_control',false) AS v"
			stmt, err := b.DB.PrepareContext(p.Ctx, heldSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: heldSQL}
			old, err := b.DB.QueryContext(p.Ctx, "SELECT roident,roname FROM pg_replication_origin WHERE roname='state_control'")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			before := originBag(t, p, b.DB)
			failure := errors.New("state origin I/O failure")
			if err := stuntgres.Dump(p.Ctx, p.Native, fixture.CatalogControlFailingWriter{Err: failure}); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: failure}); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			for _, suffix := range []string{"SELECT 1;", "SELECT pg_catalog.pg_replication_origin_advance('state_control','bad'::pg_catalog.pg_lsn);", "SELECT pg_catalog.pg_replication_origin_drop('pg_' || oid::pg_catalog.text) FROM pg_catalog.pg_subscription WHERE subname = 'state_absent_subscription';"} {
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(first.String()+suffix)); err == nil {
					t.Fatal("late invalid restore published")
				}
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Reset(canceled, p.Native); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(first.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, originBag(t, p, b.DB)) {
				t.Fatal("failed control changed identities")
			}
			originStateProgress(t, p, b, b.DB, "state_control", "80000000/4")
			originStateRun(t, p, b, b.DB, originStateScalar("SELECT n AS v FROM state_control_sentinel", "INT4", int64(41)))
			// A reader admitted before a standalone identity or relevant progress write
			// must never overwrite that newer destination state.
			for _, change := range []string{"identity", "progress", "subscription_progress", "clear_rollback", "cancel"} {
				err := subscriptionRestoreDuringRead(t, p, first.Bytes(), func(cancel context.CancelFunc) {
					switch change {
					case "identity":
						originStateCreate(t, p, b, b.DB, "state_control_stale")
					case "progress":
						originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_advance('state_control','0/6')", 1))
					case "subscription_progress":
						originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_advance($1::text,'0/7')", 1, canonical))
					case "clear_rollback":
						actor, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer actor.Close()
						originStateRun(t, p, b, actor, fixture.Exec("BEGIN", 0))
						originStateRun(t, p, b, actor, fixture.Exec("SELECT pg_replication_origin_drop('state_control')", 1))
						originStateRun(t, p, b, actor, fixture.Exec("ROLLBACK", 0))
					case "cancel":
						cancel()
					}
				})
				if change == "cancel" {
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				} else if err == nil {
					t.Fatal("stale replacement overwrote newer state", change)
				}
				if originStateID(t, p, b.DB, "state_control") != id {
					t.Fatal("failed replacement reminted identity")
				}
			}
			originStateProgress(t, p, b, b.DB, "state_control", nil)
			originStateProgress(t, p, b, b.DB, canonical, "0/7")
			originStateRun(t, p, b, b.DB, fixture.Exec("CREATE DATABASE state_control_peer", 0))
			peer, _, recorder := fixture.DatabaseScenarioPool(t, p, false, "state_control_peer", "")
			pb := b
			pb.DB = peer
			pb.Recorder = recorder
			originCreate(t, p, pb, peer, "state_control_sub", false)
			peerID, peerName := originStateSubscriptionIdentity(t, p, peer, "state_control_sub")
			originStateRun(t, p, pb, peer, fixture.Exec("SELECT pg_replication_origin_advance($1::text,'80000000/8')", 1, peerName))
			pending, err := peer.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer pending.Rollback()
			originCreate(t, p, pb, pending, "state_control_pending", false)
			pendingID, pendingName := originIdentity(t, p, pending, "state_control_pending")
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			originStateRun(t, p, b, b.DB, originStateScalar("SELECT pg_replication_origin_oid('state_control') AS v", "OID", nil))
			if actual, name := originStateSubscriptionIdentity(t, p, peer, "state_control_sub"); actual != peerID || name != peerName {
				t.Fatal("peer identity changed", actual, name)
			}
			originStateProgress(t, p, pb, peer, peerName, "80000000/8")
			originCheck(t, p, pb, pending, "state_control_pending", pendingID, pendingName)
			if err := subscriptionRestoreDuringRead(t, p, first.Bytes(), func(context.CancelFunc) {
				originStateRun(t, p, pb, peer, fixture.Exec("SELECT pg_replication_origin_advance($1::text,'0/9')", 1, peerName))
			}); err != nil {
				t.Fatal(err)
			}
			fresh := originStateID(t, p, b.DB, "state_control")
			if fresh == peerID || fresh == pendingID {
				t.Fatal("restore collided with peer or pending registration")
			}
			originStateRun(t, p, b, held, originStateScalar(heldSQL, "PG_LSN", "80000000/4"))
			originStateProgress(t, p, pb, peer, peerName, "0/9")
			if actual, name := originStateSubscriptionIdentity(t, p, peer, "state_control_sub"); actual != peerID || name != peerName {
				t.Fatal("restore changed peer identity", actual, name)
			}
			originStateRun(t, p, b, settings, originStateScalar("SELECT current_setting('application_name') AS v", "TEXT", "state_origin_control"))
			_, reminted := originStateSubscriptionIdentity(t, p, b.DB, "state_control_sub")
			if reminted == canonical {
				t.Fatal("subscription canonical name not reminted")
			}
			originStateProgress(t, p, b, b.DB, reminted, "80000000/4")
			var absent int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_replication_origin o JOIN pg_subscription s ON o.roname='pg_'||s.oid::text WHERE s.subname='state_control_missing'").Scan(&absent); err != nil || absent != 0 {
				t.Fatal(absent, err)
			}
			if err := pending.Commit(); err != nil {
				t.Fatal(err)
			}
			originCheck(t, p, pb, peer, "state_control_pending", pendingID, pendingName)
			var oldID int64
			var oldName string
			if !old.Next() {
				t.Fatal(old.Err())
			}
			if err := old.Scan(&oldID, &oldName); err != nil || oldID != id || oldName != "state_control" {
				t.Fatal(oldID, oldName, err)
			}
			old.Close()
			originStateRun(t, p, pb, peer, fixture.Exec("DROP SUBSCRIPTION state_control_pending", 0))
			originStateRun(t, p, pb, peer, fixture.Exec("DROP SUBSCRIPTION state_control_sub", 0))
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(first.Bytes(), redump.Bytes()) {
				t.Fatal("logical remint changed portable dump")
			}
		})
	}
}
func TestReferenceReplicationOriginStateFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"state_smoke_denied"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			originStateCleanup(t, b.DB, "state_smoke", "state_smoke_control")
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(context.Background(), "ROLLBACK")
			defer c.ExecContext(context.Background(), "RESET ROLE")
			run := func(s fixture.Step) { originStateRun(t, p, b, c, s) }
			id := originStateCreate(t, p, b, c, "state_smoke")
			originStateProgress(t, p, b, c, "state_smoke", nil)
			q := "SELECT pg_replication_origin_progress($1::text,$2::boolean) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			run(fixture.Exec("SELECT pg_replication_origin_advance('state_smoke','80000000/0')", 1))
			originStateRun(t, p, b, held, originStateScalar(q, "PG_LSN", "80000000/0", "state_smoke", false))
			run(originStateDuplicate("SELECT pg_replication_origin_create('state_smoke')", "state_smoke"))
			if originStateID(t, p, c, "state_smoke") != id {
				t.Fatal("duplicate changed identity")
			}
			originStateProgress(t, p, b, c, "state_smoke", "80000000/0")
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SELECT pg_replication_origin_advance('state_smoke','0/2')", 1))
			run(fixture.Exec("ROLLBACK", 0))
			originStateProgress(t, p, b, c, "state_smoke", "0/2")
			run(fixture.Exec("CREATE ROLE state_smoke_denied", 0))
			run(fixture.Exec("SET ROLE state_smoke_denied", 0))
			run(originStateError("SELECT pg_replication_origin_oid('state_smoke')", "42501", "permission denied for function pg_replication_origin_oid", ""))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("DROP ROLE state_smoke_denied", 0))
			s := originStateScalar(q, "PG_LSN", "0/2", "state_smoke", true)
			if !b.PG {
				s = originStateError(q, "0A000", originStateFlushBoundary, "", "state_smoke", true)
			}
			originStateRun(t, p, b, held, s)
			originStateRun(t, p, b, held, originStateScalar(q, "PG_LSN", nil, nil, true))
			run(originStateScalar("SELECT pg_replication_origin_drop('state_smoke') AS v", "VOID", ""))
			run(originStateScalar("SELECT pg_replication_origin_oid('state_smoke') AS v", "OID", nil))
			createSQL := "SELECT pg_replication_origin_create($1::text)"
			createStatement, err := c.PrepareContext(p.Ctx, createSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer createStatement.Close()
			originStateCreate(t, p, b, fixture.HeldStatement{Executor: c, Statement: createStatement, SQL: createSQL}, "state_smoke_control")
			run(fixture.Exec("SELECT pg_replication_origin_advance('state_smoke_control','80000000/1')", 1))
			if !b.PG {
				old, err := c.QueryContext(p.Ctx, "SELECT pg_replication_origin_progress('state_smoke_control',false) AS v")
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				columns, err := old.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"SELECT 1;")); err == nil {
					t.Fatal("late restore error published")
				}
				originStateProgress(t, p, b, c, "state_smoke_control", "80000000/1")
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				originStateRun(t, p, b, held, originStateScalar(q, "PG_LSN", "80000000/1", "state_smoke_control", false))
				var redump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
					t.Fatal("smoke redump differs")
				}
				if !old.Next() {
					t.Fatal(old.Err())
				}
				var value string
				if err := old.Scan(&value); err != nil || value != "80000000/1" || columns[0].DatabaseTypeName() != "PG_LSN" {
					t.Fatal(value, err)
				}
			}
		})
	}
}
