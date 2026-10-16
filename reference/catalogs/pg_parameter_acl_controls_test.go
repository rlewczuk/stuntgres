package reference_test

import (
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type pgParameterACLWriter struct{ calls int }

func (w *pgParameterACLWriter) Write(p []byte) (int, error) {
	w.calls++
	return len(p), nil
}

func TestReferencePGParameterACLControls(t *testing.T) {
	pgParameterACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		const name = "pacl18_control.value"
		defer func() {
			if _, err := b.DB.ExecContext(p.Ctx, "REVOKE ALL ON PARAMETER "+name+" FROM PUBLIC"); err != nil {
				t.Error("control parameter cleanup", err)
			}
		}()
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("GRANT SET ON PARAMETER "+name+" TO PUBLIC", 0))
		oid := pgParameterACLOID(t, p, b, name)
		owner := pgParameterACLOwner(b)
		acl := "{" + owner + "=sA/" + owner + ",=s/" + owner + "}"
		pgParameterACLRead(t, p, b, name, acl, oid)
		pgParameterACLFacts(t, p, b)
		if b.PG {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("REVOKE ALL ON PARAMETER "+name+" FROM PUBLIC", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, pgParameterACLSelect("SELECT * FROM pg_parameter_acl WHERE parname=$1", true, nil, name))
			return
		}
		// Controls apply to this scenario's exact native Driver/DSN. PostgreSQL
		// exercises the independently expected ordinary lifecycle above.
		q := "SELECT * FROM pg_parameter_acl WHERE parname=$1"
		held := fixture.ViewHold(t, p, b, q)
		old, err := b.DB.QueryContext(p.Ctx, q, name)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		saved, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		var writer pgParameterACLWriter
		err = stuntgres.Dump(p.Ctx, p.Native, &writer)
		var state interface{ SQLState() string }
		if err == nil || err.Error() != "stuntgres: dump cannot serialize parameter ACL" || writer.calls != 0 || errors.As(err, &state) {
			t.Fatal("ordinary Dump rejection before writer callback", err, writer.calls)
		}
		pgParameterACLRead(t, p, b, name, acl, oid)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgParameterACLSelect(q, false, nil, name))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, pgParameterACLSelect(q, false, nil, name))
		pgParameterACLFacts(t, p, b)
		var oldOID int64
		var oldName, oldACL string
		if !old.Next() {
			t.Fatal("Reset discarded already captured row", old.Err())
		}
		if err := old.Scan(&oldOID, &oldName, &oldACL); err != nil || oldOID != oid || oldName != name || oldACL != acl {
			t.Fatal("retained Reset values", oldOID, oldName, oldACL, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained EOF", old.Err())
		}
		if err := old.Close(); err != nil {
			t.Fatal(err)
		}
		pgParameterACLMetadata(t, saved, false)
	})
}
