package reference_test

import (
	"testing"

	"github.com/lib/pq"
)

func TestReferenceSerialIdentitySystemColumns(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			serialIdentityRun(t, p, b, c,
				referenceExec(`CREATE TABLE si_system(id SERIAL)`, 0),
				referenceExec(`CREATE VIEW si_system_view AS SELECT id FROM si_system`, 0))
			held := serialIdentityHeld(t, p, b, c)
			for _, column := range []string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"} {
				t.Run(column, func(t *testing.T) {
					t.Run("table", func(t *testing.T) {
						serialIdentityCheck(t, p, b, c, held, "si_system", column, nil)
					})
					want := controlsReferenceDiagnostic("42703", `column "`+column+`" of relation "si_system_view" does not exist`)
					t.Run("view/direct", func(t *testing.T) {
						s := referenceErrorDiagnostic(`SELECT pg_get_serial_sequence('si_system_view',`+pq.QuoteLiteral(column)+`)`, want)
						s.selectRows, s.errorStage = true, "execute"
						serialIdentityRun(t, p, b, c, s)
					})
					t.Run("view/held-execute", func(t *testing.T) {
						s := referenceErrorDiagnostic(serialIdentityQuery, want, "si_system_view", column)
						s.selectRows, s.errorStage = true, "execute"
						serialIdentityRun(t, p, b, held, s)
					})
					t.Run("held-recovery", func(t *testing.T) {
						serialIdentityCheck(t, p, b, c, held, "si_system", "id", "public.si_system_id_seq")
						serialIdentityCheck(t, p, b, c, held, "si_system_view", "id", nil)
					})
				})
			}
			serialIdentityRun(t, p, b, c,
				referenceSelect(`SELECT last_value,is_called FROM si_system_id_seq`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
		})
	}
}
