package fixture

import (
	"database/sql"
	"testing"
)

func AlterIdentityScenario(t *testing.T, body func(*testing.T, *Pair, Backend, *sql.Conn)) {
	t.Helper()
	p := NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b, IdentityConnection(t, p.Ctx, b.DB)) })
	}
}

func AlterIdentityMissing(t *testing.T, p *Pair, b Backend, c Executor, name string) {
	t.Helper()
	s := ImplicitError(`SELECT * FROM `+name, Diagnostic{Code: "42P01", Message: `relation "` + name + `" does not exist`, Position: "15"})
	s.SelectRows = true
	RunStep(t, p.Ctx, c, b.Recorder, s)
}
