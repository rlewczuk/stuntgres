package reference_test

import (
	"bytes"
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
	// Metadata families are independent. Each fixture keeps its complete
	// grant/Dump/Reset/retained-row lifecycle on one unchanged context.
	for _, group := range []struct {
		name  string
		facts []int
	}{
		{"relation", []int{0, 1}},
		{"row_array_types", []int{2, 6}},
		{"discovery", []int{3, 4}},
		{"attributes", []int{5}},
	} {
		t.Run(group.name, func(t *testing.T) {
			pgParameterACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				pgParameterACLControls(t, p, b, group.facts)
			})
		})
	}
}

func pgParameterACLControls(t *testing.T, p *fixture.Pair, b fixture.Backend, facts []int) {
	t.Helper()
	checkFacts := func() {
		t.Helper()
		steps := pgParameterACLFactSteps(b)
		for _, index := range facts {
			s := steps[index]
			fixture.PGDatabaseCheck(t, p, b, b.DB, s)
			fixture.PGDatabaseCheck(t, p, b, b.DB, fixture.Prepared(s))
			held := fixture.ViewHold(t, p, b, s.Query)
			fixture.PGDatabaseCheck(t, p, b, held, s)
			fixture.PGDatabaseCheck(t, p, b, held, s)
		}
	}
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
	checkFacts()
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
	var snapshot bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &snapshot); err != nil {
		t.Fatal("parameter ACL Dump", err)
	}
	pgParameterACLRead(t, p, b, name, acl, oid)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, pgParameterACLSelect(q, false, nil, name))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, pgParameterACLSelect(q, false, nil, name))
	checkFacts()
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
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(snapshot.Bytes())); err != nil {
		t.Fatal(err)
	}
	restoredOID := pgParameterACLOID(t, p, b, name)
	if restoredOID == 0 {
		t.Fatal("missing destination parameter identity")
	}
	pgParameterACLRead(t, p, b, name, acl, restoredOID)
	checkFacts()
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(snapshot.Bytes(), again.Bytes()) {
		t.Fatal("parameter ACL redump is not canonical", err)
	}
}
