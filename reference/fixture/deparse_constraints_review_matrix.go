package fixture

import (
	"database/sql"
	"testing"
)

func DeparseReviewStreams(t *testing.T, scenario func(*testing.T, *sql.Conn, bool, func(Step) Observed)) {
	t.Helper()
	p := NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			scenario(t, conn, b.Name == "postgres", func(s Step) Observed {
				if s.SelectRows && s.Diagnostic == nil {
					previous := s.CheckMetadata
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						DeparseReviewMetadata(t, b.PG, rows)
						if previous != nil {
							previous(t, rows)
						}
					}
				}
				return RunStep(t, p.Ctx, conn, b.Recorder, s)
			})
		})
	}
}
