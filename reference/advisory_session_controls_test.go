package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"testing"
)

type advisorySessionFailReader struct{ err error }

func (r advisorySessionFailReader) Read([]byte) (int, error) { return 0, r.err }

func TestReferenceAdvisorySessionNativeControls(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			a, peer := advisorySessionConn(t, p, b), advisorySessionConn(t, p, b)
			advisorySessionExec(t, p.ctx, a, "CREATE TABLE advisory_control_data(id integer)", 0)
			advisorySessionExec(t, p.ctx, a, "INSERT INTO advisory_control_data VALUES(7)", 1)
			for _, k := range advisorySessionKeys {
				for _, name := range []string{"pg_advisory_lock", "pg_advisory_lock_shared", "pg_advisory_lock"} {
					advisorySessionValue(t, p.ctx, a, k.call(name), "VOID", "")
				}
				advisorySessionValue(t, p.ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", false)
			}
			if !b.pg {
				// Controls use only the unwrapped native pool; A/B remain actual
				// retained physical sessions, not the control borrower's session.
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(strings.ToLower(dump.String()), "pg_advisory_") {
					t.Fatal("Dump serialized live locks", dump.String())
				}
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				for _, k := range advisorySessionKeys {
					advisorySessionValue(t, p.ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", false)
				}
				if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				referenceRunStep(t, p.ctx, a, b.recorder, referenceSelect("SELECT id FROM advisory_control_data", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				for _, k := range advisorySessionKeys {
					advisorySessionValue(t, p.ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", false)
				}
				// Replay the actual emitted SQL in a different Driver: persistent
				// rows restore, ownership does not leak across driver domains.
				connector, err := stuntgres.NewDriver().OpenConnector("advisory/dump-target")
				if err != nil {
					t.Fatal(err)
				}
				target := sql.OpenDB(connector)
				defer target.Close()
				target.SetMaxOpenConns(1)
				target.SetMaxIdleConns(1)
				if err := stuntgres.Restore(p.ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				advisorySessionValue(t, p.ctx, target, "SELECT id FROM advisory_control_data", "INT4", int64(7))
				for _, k := range advisorySessionKeys {
					advisorySessionValue(t, p.ctx, target, k.call("pg_try_advisory_lock"), "BOOL", true)
				}
				advisorySessionClear(t, p.ctx, target)
				// Retain a materialized public cursor over catalog replacement.
				rows, err := a.QueryContext(p.ctx, "SELECT id FROM advisory_control_data")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				injected := errors.New("advisory restore input failed")
				if err := stuntgres.Restore(p.ctx, p.native, advisorySessionFailReader{injected}); !errors.Is(err, injected) {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(p.ctx)
				cancel()
				if err := stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if !rows.Next() {
					t.Fatal(rows.Err())
				}
				var id int64
				if err := rows.Scan(&id); err != nil || id != 7 {
					t.Fatal(id, err)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal(rows.Err())
				}
				rows.Close()
				for _, k := range advisorySessionKeys {
					advisorySessionValue(t, p.ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", false)
				}
				// Restore's scalar allowlist must not evaluate advisory effects.
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE forbidden(v bool); INSERT INTO forbidden VALUES(pg_try_advisory_lock(999));")); err == nil {
					t.Fatal("Restore executed advisory scalar")
				}
				advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock(999)", "BOOL", true)
				advisorySessionValue(t, p.ctx, peer, "SELECT pg_advisory_unlock(999)", "BOOL", true)
				referenceRunStep(t, p.ctx, a, b.recorder, referenceSelect("SELECT id FROM advisory_control_data", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				w := advisorySessionStart(t, p.ctx, peer, advisorySessionKeys[0].call("pg_advisory_lock"))
				// No worker-start timing claim: whether already pending or
				// admitted later, failed staging must preserve the real outcome.
				if err := stuntgres.Restore(p.ctx, p.native, advisorySessionFailReader{io.ErrUnexpectedEOF}); !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatal(err)
				}
				advisorySessionClear(t, p.ctx, a)
				if err := w.await(t); err != nil {
					t.Fatal(err)
				}
				advisorySessionValue(t, p.ctx, a, advisorySessionKeys[0].call("pg_try_advisory_lock"), "BOOL", false)
				advisorySessionClear(t, p.ctx, peer)
				// Rebuild the counted state for the common independent decrements.
				for _, k := range advisorySessionKeys {
					for _, name := range []string{"pg_advisory_lock", "pg_advisory_lock_shared", "pg_advisory_lock"} {
						advisorySessionValue(t, p.ctx, a, k.call(name), "VOID", "")
					}
				}
				// Exact opaque DSN isolation belongs only to native host controls.
				other, err := p.nativeConnector.Driver().(driver.DriverContext).OpenConnector("reference/" + t.Name() + "/other")
				if err != nil {
					t.Fatal(err)
				}
				isolated := sql.OpenDB(other)
				defer isolated.Close()
				isolated.SetMaxOpenConns(1)
				isolated.SetMaxIdleConns(1)
				for _, k := range advisorySessionKeys {
					advisorySessionValue(t, p.ctx, isolated, k.call("pg_try_advisory_lock"), "BOOL", true)
				}
				advisorySessionClear(t, p.ctx, isolated)
			}
			for _, k := range advisorySessionKeys {
				advisorySessionValue(t, p.ctx, a, k.call("pg_advisory_unlock_shared"), "BOOL", true)
				advisorySessionValue(t, p.ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", false)
				advisorySessionValue(t, p.ctx, a, k.call("pg_advisory_unlock"), "BOOL", true)
				advisorySessionValue(t, p.ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", false)
				advisorySessionValue(t, p.ctx, a, k.call("pg_advisory_unlock"), "BOOL", true)
				advisorySessionValue(t, p.ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", true)
			}
			advisorySessionClear(t, p.ctx, peer)
		})
	}
}
