package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceINETFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE smoke_n(id INTEGER,h INET,n CIDR)", 0))
	for _, row := range [][]any{{1, "10.255.255.255/8", []byte("10")}, {2, "10.0.0.0/16", []byte("192.168/25")}, {3, "::1", []byte("2001:db8::/113")}} {
		p.Step(t, fixture.Prepared(fixture.Exec("INSERT INTO smoke_n VALUES($1,$2,$3)", 1, row...)))
	}
	p.Step(t, fixture.Select("SELECT id,h,n FROM smoke_n ORDER BY h", []string{"id", "h", "n"}, []string{"INT4", "INET", "CIDR"}, [][]any{{int64(1), "10.255.255.255/8", "10.0.0.0/8"}, {int64(2), "10.0.0.0/16", "192.168.0.0/25"}, {int64(3), "::1", "2001:db8::/113"}}))
	p.Step(t, fixture.Select("SELECT ('192.168.0.129/25'::inet)::cidr AS n,('192.168.1.5'::inet)::text AS h", []string{"n", "h"}, []string{"CIDR", "TEXT"}, [][]any{{"192.168.0.128/25", "192.168.1.5/32"}}))
	d := inetDiagnostic("cidr", "192.168.0.1/24", true)
	d.Position = "8"
	p.Step(t, fixture.ErrorDiagnostic("SELECT '192.168.0.1/24'::cidr", d))
	for _, b := range p.Backends() {
		s := fixture.Select("SELECT h,n FROM smoke_n WHERE id=1", []string{"h", "n"}, []string{"INET", "CIDR"}, [][]any{{"10.255.255.255/8", "10.0.0.0/8"}})
		s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
			inetMetadata(t, r, b.PG, []string{"h", "n"}, []string{"INET", "CIDR"}, []bool{true, true})
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}
