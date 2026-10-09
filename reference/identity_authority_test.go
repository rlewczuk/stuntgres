//go:build reference

package reference_test

import "testing"

func TestReferenceIdentityInsertOnlyAuthority(t *testing.T) {
	p := newReferencePair(t)
	c, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, q := range []string{`CREATE TABLE secured(id INT GENERATED ALWAYS AS IDENTITY)`, `CREATE ROLE identity_writer`, `GRANT USAGE ON SCHEMA public TO identity_writer`, `GRANT INSERT ON secured TO identity_writer`, `SET ROLE identity_writer`} {
		sequenceProbeExec(t, p.ctx, c, q)
	}
	identityRefConnExec(t, p.ctx, c, `INSERT INTO secured DEFAULT VALUES`, 1)
	_, err = c.ExecContext(p.ctx, `SELECT nextval('secured_id_seq')`)
	want := controlsReferenceDiagnostic("42501", "permission denied for sequence secured_id_seq")
	referenceAssertDiagnostic(t, err, &want)
	sequenceProbeExec(t, p.ctx, c, `RESET ROLE`)
	sequenceProbeValue(t, p.ctx, c, `SELECT id FROM secured`, 1)
}
