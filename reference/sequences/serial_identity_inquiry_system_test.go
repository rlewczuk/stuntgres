package reference_test

import (
	"github.com/lib/pq"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSerialIdentitySystemColumns(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			serialIdentityRun(t, p, b, c,
				fixture.Exec(`CREATE TABLE si_system(id SERIAL)`, 0),
				fixture.Exec(`CREATE VIEW si_system_view AS SELECT id FROM si_system`, 0))
			held := serialIdentityHeld(t, p, b, c)
			for _, column := range []string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"} {
				t.Run(column, func(t *testing.T) {
					t.Run("table", func(t *testing.T) {
						serialIdentityCheck(t, p, b, c, held, "si_system", column, nil)
					})
					want := fixture.ControlsReferenceDiagnostic("42703", `column "`+column+`" of relation "si_system_view" does not exist`)
					t.Run("view/direct", func(t *testing.T) {
						s := fixture.ErrorDiagnostic(`SELECT pg_get_serial_sequence('si_system_view',`+pq.QuoteLiteral(column)+`)`, want)
						s.SelectRows, s.ErrorStage = true, "execute"
						serialIdentityRun(t, p, b, c, s)
					})
					t.Run("view/held-execute", func(t *testing.T) {
						s := fixture.ErrorDiagnostic(serialIdentityQuery, want, "si_system_view", column)
						s.SelectRows, s.ErrorStage = true, "execute"
						serialIdentityRun(t, p, b, held, s)
					})
					t.Run("held-recovery", func(t *testing.T) {
						serialIdentityCheck(t, p, b, c, held, "si_system", "id", "public.si_system_id_seq")
						serialIdentityCheck(t, p, b, c, held, "si_system_view", "id", nil)
					})
				})
			}
			serialIdentityRun(t, p, b, c,
				fixture.Select(`SELECT last_value,is_called FROM si_system_id_seq`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
		})
	}
}
