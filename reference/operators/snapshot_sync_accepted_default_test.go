//go:build !noservertests

package reference_test

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
	"stuntgres/reference/fixture"
)

// These are real accepted portals: Parse/Describe/Bind all precede replacement.
// SQL keeps its predecessor; genuine PL selects its compatible current body.
func TestReferenceSnapshotSyncAcceptedDefaultDemand(t *testing.T) {
	for _, language := range []string{"standard", "quoted", "pl"} {
		for _, exporting := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/exporting-predecessor=%v", language, exporting), func(t *testing.T) {
				f := fixture.NewServerFixture(t)
				for _, b := range f.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						w := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
						peer := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
						importer := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
						triggerProtocolCode(t, w.query(t, snapshotDefaultSetup), "")
						before := snapshotDefaultDeclaration(language, exporting)
						after := snapshotDefaultDeclaration(language, !exporting)
						triggerProtocolCode(t, w.query(t, "CREATE FUNCTION snapshot_default(a integer) RETURNS text STABLE "+before+"; CREATE TABLE snapshot_default_rows(v text DEFAULT snapshot_default(a=>snapshot_default_arg(1+3)),marker integer)"), "")
						triggerProtocolCode(t, w.query(t, "BEGIN; INSERT INTO snapshot_default_cut VALUES(9,90)"), "")
						defer func() { triggerProtocolCode(t, w.query(t, "ROLLBACK"), "") }()
						query := "INSERT INTO snapshot_default_rows DEFAULT VALUES RETURNING v"
						frames := w.cycle(t,
							&pgproto3.Parse{Name: "snapshot", Query: query},
							&pgproto3.Describe{ObjectType: 'S', Name: "snapshot"},
							&pgproto3.Bind{DestinationPortal: "old", PreparedStatement: "snapshot"},
							&pgproto3.Parse{Name: "empty", Query: "INSERT INTO snapshot_default_rows(marker) SELECT 1 WHERE false"},
							&pgproto3.Bind{DestinationPortal: "empty", PreparedStatement: "empty"})
						triggerProtocolCode(t, frames, "")
						routineDefaultPortalDescription(t, frames, []string{"v"}, []uint32{25})
						snapshotDefaultNoNotice(t, frames)
						triggerProtocolReady(t, frames, 'T')
						triggerProtocolScalar(t, w, "SELECT is_called::text FROM snapshot_default_seq", "false")
						triggerProtocolScalar(t, w, "SELECT count(*) FROM snapshot_default_rows", "0")
						triggerProtocolCode(t, w.query(t, "CREATE OR REPLACE FUNCTION snapshot_default(a integer) RETURNS text STABLE "+after+"; CREATE OR REPLACE FUNCTION snapshot_default_arg(a integer) RETURNS integer IMMUTABLE RETURN a+10"), "")
						frames = w.cycle(t, &pgproto3.Execute{Portal: "empty"})
						triggerProtocolCode(t, frames, "")
						triggerProtocolCompletions(t, frames, "INSERT 0 0")
						snapshotDefaultNoNotice(t, frames)
						triggerProtocolScalar(t, w, "SELECT is_called::text FROM snapshot_default_seq", "false")

						selectedExport := exporting
						if language == "pl" {
							selectedExport = !exporting
						}
						var retained string
						commits, retainedCut := 0, 0
						for _, route := range []string{"old", "fresh", "direct", "reusable"} {
							argument, exports := "14", !exporting
							messages := []pgproto3.FrontendMessage{&pgproto3.Execute{Portal: route}}
							switch route {
							case "old":
								argument, exports = "5", selectedExport
							case "fresh":
								messages = append([]pgproto3.FrontendMessage{&pgproto3.Bind{DestinationPortal: route, PreparedStatement: "snapshot"}}, messages...)
							case "direct":
								messages = []pgproto3.FrontendMessage{&pgproto3.Query{String: query}}
							case "reusable":
								triggerProtocolCode(t, w.query(t, "PREPARE snapshot_reusable AS "+query), "")
								messages = []pgproto3.FrontendMessage{&pgproto3.Query{String: "EXECUTE snapshot_reusable"}}
							}
							cut := commits
							if exports {
								commits++
								frames = snapshotDefaultBlockedExecute(t, f, b, w, peer, messages, commits)
							} else if route == "direct" {
								frames = w.query(t, query)
							} else if route == "reusable" {
								frames = w.query(t, "EXECUTE snapshot_reusable")
							} else {
								frames = w.cycle(t, messages...)
							}
							triggerProtocolCode(t, frames, "")
							triggerProtocolCompletions(t, frames, "INSERT 0 1")
							triggerProtocolReady(t, frames, 'T')
							snapshotDefaultNoNotice(t, frames)
							value := snapshotDefaultScalar(t, frames)
							if !exports {
								if value != "quiet:"+argument {
									t.Fatalf("%s returned %q want quiet:%s", route, value, argument)
								}
								continue
							}
							token := snapshotDefaultToken(t, value, argument)
							triggerProtocolCode(t, importer.query(t, "BEGIN ISOLATION LEVEL REPEATABLE READ; SET TRANSACTION SNAPSHOT '"+token+"'"), "")
							// The peer's commit during the actual callback wait is newer
							// than F. So are its unread/empty-table changes. Private9
							// is never part of an exported committed cut.
							triggerProtocolValues(t, importer.query(t, "SELECT id,v FROM snapshot_default_cut ORDER BY id"), [][]string{{"1", fmt.Sprint(10+cut)}, {"3", "30"}})
							triggerProtocolScalar(t, importer, "SELECT v FROM snapshot_default_unread", fmt.Sprint(10+cut))
							triggerProtocolScalar(t, importer, "SELECT count(*) FROM snapshot_default_empty", fmt.Sprint(cut))
							triggerProtocolCode(t, importer.query(t, "ROLLBACK"), "")
							retained, retainedCut = token, cut
						}
						triggerProtocolScalar(t, w, "SELECT count(*) FROM snapshot_default_rows", "4")
						if retained != "" {
							triggerProtocolCode(t, importer.query(t, "BEGIN ISOLATION LEVEL REPEATABLE READ; SET TRANSACTION SNAPSHOT '"+retained+"'"), "")
						}
						triggerProtocolCode(t, w.query(t, "ROLLBACK"), "")
						if retained != "" {
							triggerProtocolValues(t, importer.query(t, "SELECT id,v FROM snapshot_default_cut ORDER BY id"), [][]string{{"1", fmt.Sprint(10+retainedCut)}, {"3", "30"}})
							triggerProtocolScalar(t, importer, "SELECT v FROM snapshot_default_unread", fmt.Sprint(10+retainedCut))
							triggerProtocolScalar(t, importer, "SELECT count(*) FROM snapshot_default_empty", fmt.Sprint(retainedCut))
							triggerProtocolCode(t, importer.query(t, "ROLLBACK; BEGIN ISOLATION LEVEL REPEATABLE READ"), "")
							routineDefaultPortalDiagnostic(t, importer.query(t, "SET TRANSACTION SNAPSHOT '"+retained+"'"), fixture.ControlsReferenceDiagnostic("42704", "snapshot \""+retained+"\" does not exist"))
							triggerProtocolCode(t, importer.query(t, "ROLLBACK"), "")
						}
					})
				}
			})
		}
	}
	t.Run("nested-siblings-hidden-body", snapshotDefaultMixedDemand)
}

const snapshotDefaultSetup = "CREATE TABLE snapshot_default_cut(id integer PRIMARY KEY,v integer); INSERT INTO snapshot_default_cut VALUES(1,10),(3,30); CREATE TABLE snapshot_default_unread(v integer); INSERT INTO snapshot_default_unread VALUES(10); CREATE TABLE snapshot_default_empty(v integer); CREATE SEQUENCE snapshot_default_seq; CREATE FUNCTION snapshot_default_arg(a integer) RETURNS integer IMMUTABLE RETURN a+1; CREATE FUNCTION snapshot_default_gate() RETURNS boolean STABLE LANGUAGE plpgsql AS $$BEGIN PERFORM pg_advisory_lock(4294967321::bigint); PERFORM pg_advisory_unlock(4294967321::bigint); PERFORM nextval('snapshot_default_seq'); RETURN true; END$$; CREATE FUNCTION snapshot_default_export(a integer) RETURNS text STABLE LANGUAGE plpgsql AS $$BEGIN RETURN CASE WHEN snapshot_default_gate() THEN pg_export_snapshot()||':'||a::text ELSE 'unreachable' END; END$$"

func snapshotDefaultDeclaration(language string, exporting bool) string {
	body := "'quiet:'||a::text"
	if exporting {
		body = "CASE WHEN snapshot_default_gate() THEN pg_export_snapshot()||':'||a::text ELSE 'unreachable' END"
		if language != "pl" {
			// REL_18_6 clauses.c rejects direct VOLATILE expressions in a
			// STABLE SQL inline body. This unchanged STABLE PL callback makes
			// the simple SQL wrapper inlineable without inventing an SQL
			// executor frame or changing the callback's borrowed outer cut.
			body = "snapshot_default_export(a)"
		}
	}
	switch language {
	case "quoted":
		return "LANGUAGE SQL AS $$SELECT " + body + "$$"
	case "pl":
		return "LANGUAGE plpgsql AS $$BEGIN RETURN " + body + "; END$$"
	default:
		return "RETURN " + body
	}
}

func snapshotDefaultNoNotice(t *testing.T, frames [][]byte) {
	t.Helper()
	for _, frame := range frames {
		if frame[0] == 'N' {
			t.Fatalf("unexpected snapshot DEFAULT notice: %q", frame)
		}
	}
}

func snapshotDefaultScalar(t *testing.T, frames [][]byte) string {
	t.Helper()
	var values []string
	for _, frame := range frames {
		if frame[0] != 'D' {
			continue
		}
		var row pgproto3.DataRow
		if err := row.Decode(frame[5:]); err != nil {
			t.Fatal(err)
		}
		if len(row.Values) != 1 || row.Values[0] == nil {
			t.Fatal("expected one nonNULL text value")
		}
		values = append(values, string(row.Values[0]))
	}
	if len(values) != 1 {
		t.Fatalf("expected one row, got %v", values)
	}
	return values[0]
}

func snapshotDefaultToken(t *testing.T, value, argument string) string {
	t.Helper()
	token, suffix, ok := strings.Cut(value, ":")
	if !ok || suffix != argument || token == "" || strings.IndexFunc(token, func(r rune) bool { return !(r >= '0' && r <= '9' || r >= 'A' && r <= 'F' || r == '-') }) >= 0 {
		t.Fatalf("expected export token and original prepared argument %s, got %q", argument, value)
	}
	return token
}

// Send Execute without receiving it yet. The real server owns the blocked
// request; a different connection observes its exact advisory-lock admission.
func snapshotDefaultBlockedExecute(t *testing.T, f *fixture.ServerFixture, b fixture.Backend, w, peer *triggerProtocolWire, messages []pgproto3.FrontendMessage, step int) [][]byte {
	t.Helper()
	triggerProtocolCode(t, peer.query(t, "SELECT pg_advisory_lock(4294967321::bigint)"), "")
	locked, joined := true, false
	type result struct {
		frames [][]byte
		err error
	}
	completed := make(chan result, 1)
	defer func() {
		if locked {
			triggerProtocolCode(t, peer.query(t, "SELECT pg_advisory_unlock(4294967321::bigint)"), "")
		}
		if !joined {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			_ = w.conn.CancelRequest(ctx)
			select {
			case <-completed:
			case <-ctx.Done():
				_ = w.conn.Close(ctx)
				<-completed
			}
		}
	}()
	for _, message := range messages {
		w.conn.Frontend().Send(message)
	}
	if _, simple := messages[0].(*pgproto3.Query); !simple {
		w.conn.Frontend().Send(&pgproto3.Sync{})
	}
	if err := w.conn.Frontend().Flush(); err != nil {
		joined = true
		t.Fatal(err)
	}
	go func() {
		var out result
		for {
			message, err := w.conn.ReceiveMessage(w.ctx)
			if err != nil {
				out.err = err
				break
			}
			frame, err := message.Encode(nil)
			if err != nil {
				out.err = err
				break
			}
			out.frames = append(out.frames, frame)
			if _, ready := message.(*pgproto3.ReadyForQuery); ready {
				break
			}
		}
		completed <- out
	}()
	ctx, cancel := context.WithTimeout(f.Ctx, 5*time.Second)
	defer cancel()
	observer := f.RawPool(t, b)
	for {
		select {
		case out := <-completed:
			joined = true
			triggerProtocolCode(t, out.frames, "")
			t.Fatalf("selected DEFAULT completed before advisory admission: %v", out.err)
		default:
		}
		var pending bool
		err := observer.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND pid=$1 AND classid=1 AND objid=25 AND objsubid=1 AND mode='ExclusiveLock' AND NOT granted AND $2=ANY(pg_blocking_pids(pid)))", w.conn.PID(), peer.conn.PID()).Scan(&pending)
		if err != nil {
			t.Fatal("observing selected DEFAULT callback admission", err)
		}
		if pending {
			break
		}
		runtime.Gosched()
	}
	triggerProtocolCode(t, peer.query(t, fmt.Sprintf("UPDATE snapshot_default_cut SET v=%d WHERE id=1; UPDATE snapshot_default_unread SET v=%d; INSERT INTO snapshot_default_empty VALUES(%d)", 10+step, 10+step, step)), "")
	triggerProtocolCode(t, peer.query(t, "SELECT pg_advisory_unlock(4294967321::bigint)"), "")
	locked = false
	select {
	case out := <-completed:
		joined = true
		if out.err != nil {
			t.Fatal(out.err)
		}
		return out.frames
	case <-f.Ctx.Done():
		t.Fatal(f.Ctx.Err())
		return nil
	}
}
