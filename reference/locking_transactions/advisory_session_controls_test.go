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
	"stuntgres/reference/fixture"
	"testing"
)

type advisorySessionFailReader struct{ err error }

func (r advisorySessionFailReader) Read([]byte) (int, error) { return 0, r.err }

func TestReferenceAdvisorySessionNativeControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, peer := advisorySessionConn(t, p, b), advisorySessionConn(t, p, b)
			advisorySessionExec(t, p.Ctx, a, "CREATE TABLE advisory_control_data(id integer)", 0)
			advisorySessionExec(t, p.Ctx, a, "INSERT INTO advisory_control_data VALUES(7)", 1)
			for _, k := range advisorySessionKeys {
				for _, name := range []string{"pg_advisory_lock", "pg_advisory_lock_shared", "pg_advisory_lock"} {
					advisorySessionValue(t, p.Ctx, a, k.call(name), "VOID", "")
				}
				advisorySessionValue(t, p.Ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", false)
			}
			if !b.PG {
				// Controls use only the unwrapped native pool; A/B remain actual
				// retained physical sessions, not the control borrower's session.
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(strings.ToLower(dump.String()), "pg_advisory_") {
					t.Fatal("Dump serialized live locks", dump.String())
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				for _, k := range advisorySessionKeys {
					advisorySessionValue(t, p.Ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", false)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT id FROM advisory_control_data", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				for _, k := range advisorySessionKeys {
					advisorySessionValue(t, p.Ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", false)
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
				if err := stuntgres.Restore(p.Ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				advisorySessionValue(t, p.Ctx, target, "SELECT id FROM advisory_control_data", "INT4", int64(7))
				for _, k := range advisorySessionKeys {
					advisorySessionValue(t, p.Ctx, target, k.call("pg_try_advisory_lock"), "BOOL", true)
				}
				advisorySessionClear(t, p.Ctx, target)
				// Retain a materialized public cursor over catalog replacement.
				rows, err := a.QueryContext(p.Ctx, "SELECT id FROM advisory_control_data")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				injected := errors.New("advisory restore input failed")
				if err := stuntgres.Restore(p.Ctx, p.Native, advisorySessionFailReader{injected}); !errors.Is(err, injected) {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
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
					advisorySessionValue(t, p.Ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", false)
				}
				// Restore's scalar allowlist must not evaluate advisory effects.
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE forbidden(v bool); INSERT INTO forbidden VALUES(pg_try_advisory_lock(999));")); err == nil {
					t.Fatal("Restore executed advisory scalar")
				}
				advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(999)", "BOOL", true)
				advisorySessionValue(t, p.Ctx, peer, "SELECT pg_advisory_unlock(999)", "BOOL", true)
				fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT id FROM advisory_control_data", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				w := advisorySessionStart(t, p.Ctx, peer, advisorySessionKeys[0].call("pg_advisory_lock"))
				// No worker-start timing claim: whether already pending or
				// admitted later, failed staging must preserve the real outcome.
				if err := stuntgres.Restore(p.Ctx, p.Native, advisorySessionFailReader{io.ErrUnexpectedEOF}); !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatal(err)
				}
				advisorySessionClear(t, p.Ctx, a)
				if err := w.await(t); err != nil {
					t.Fatal(err)
				}
				advisorySessionValue(t, p.Ctx, a, advisorySessionKeys[0].call("pg_try_advisory_lock"), "BOOL", false)
				advisorySessionClear(t, p.Ctx, peer)
				// Rebuild the counted state for the common independent decrements.
				for _, k := range advisorySessionKeys {
					for _, name := range []string{"pg_advisory_lock", "pg_advisory_lock_shared", "pg_advisory_lock"} {
						advisorySessionValue(t, p.Ctx, a, k.call(name), "VOID", "")
					}
				}
				// Exact opaque DSN isolation belongs only to native host controls.
				other, err := p.NativeConnector.Driver().(driver.DriverContext).OpenConnector("reference/" + t.Name() + "/other")
				if err != nil {
					t.Fatal(err)
				}
				isolated := sql.OpenDB(other)
				defer isolated.Close()
				isolated.SetMaxOpenConns(1)
				isolated.SetMaxIdleConns(1)
				for _, k := range advisorySessionKeys {
					advisorySessionValue(t, p.Ctx, isolated, k.call("pg_try_advisory_lock"), "BOOL", true)
				}
				advisorySessionClear(t, p.Ctx, isolated)
			}
			for _, k := range advisorySessionKeys {
				advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_unlock_shared"), "BOOL", true)
				advisorySessionValue(t, p.Ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", false)
				advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_unlock"), "BOOL", true)
				advisorySessionValue(t, p.Ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", false)
				advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_unlock"), "BOOL", true)
				advisorySessionValue(t, p.Ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", true)
			}
			advisorySessionClear(t, p.Ctx, peer)
		})
	}
}
