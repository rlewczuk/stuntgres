package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func pgParameterACLSharedPending(t *testing.T, p *fixture.Pair, a, b fixture.Backend, dump []byte) {
	t.Helper()
	for _, reset := range []bool{true, false} {
		c, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		func() {
			defer c.Close()
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			for _, q := range []string{"BEGIN", "GRANT SET ON PARAMETER pacs_pending.value TO pacs_reader"} { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
			if reset {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
			} else {
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil { t.Fatal(err) }
			}
			_, err := c.ExecContext(p.Ctx, "COMMIT")
			fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
			for _, view := range []fixture.Backend{a, b} { fixture.RunStep(t, p.Ctx, view.DB, view.Recorder, fixture.Select("SELECT parname FROM pg_parameter_acl WHERE parname='pacs_pending.value'", []string{"parname"}, []string{"TEXT"}, nil)) }
		}()
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil { t.Fatal(err) }
	}
	// Existing-row work has a descriptor predecessor in addition to the absent
	// first-write incarnation fence. Both must reject stale publication.
	c, err := b.DB.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	defer c.Close()
	defer c.ExecContext(p.Ctx, "ROLLBACK")
	for _, q := range []string{"BEGIN", "REVOKE SET ON PARAMETER lc_messages FROM pacs_reader"} { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil { t.Fatal(err) }
	_, err = c.ExecContext(p.Ctx, "COMMIT")
	fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
}
