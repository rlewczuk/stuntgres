package fixture

import (
	"database/sql"
	"fmt"
	"testing"
)

func TableLikeScenario(t *testing.T, body func(*testing.T, *Pair, Backend, *sql.Conn, bool, func(Step))) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					run := func(s Step) {
						s.Prepared = prepared
						// Explicit generated/ALWAYS-identity writes are rejected during analysis,
						// as in the existing generated and identity diagnostic scenarios.
						if prepared && s.State == "428C9" {
							s.ErrorStage = "prepare"
						}
						RunStep(t, p.Ctx, c, b.Recorder, s)
					}
					body(t, p, b, c, prepared, run)
				})
			}
		})
	}
}

func TableLikeError(q string, d Diagnostic) Step {
	s := ImplicitError(q, d)
	s.ErrorStage = "execute"
	return s
}

func TableLikeAbsent(run func(Step), name string, prepared bool) {
	s := ImplicitError("SELECT * FROM "+name, Diagnostic{Code: "42P01", Message: `relation "` + name + `" does not exist`, Position: "15"})
	s.SelectRows = true
	s.ErrorStage = "execute"
	if prepared {
		s.ErrorStage = "prepare"
	}
	run(s)
}
