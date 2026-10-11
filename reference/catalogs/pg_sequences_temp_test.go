package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGSequencesTemp(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			s := fixture.NewTempReferenceSession(t, p, b.DB, b.Recorder, false)
			owner := pgSequencesOwner(t, p, s.C)
			s.Exec(s.C, "CREATE SEQUENCE public.pgsv_temp", 0)
			s.Exec(s.C, "CREATE TEMP SEQUENCE pgsv_temp", 0)
			var schema string
			if err := s.C.QueryRowContext(p.Ctx, "SELECT schemaname FROM pg_sequences WHERE sequencename='pgsv_temp' AND schemaname<>'public'").Scan(&schema); err != nil {
				t.Fatal(err)
			}
			pgSequencesScalar(t, p, b, s.C, "SELECT nextval('pgsv_temp')", "nextval", 1)
			persistent := pgSequencesDefault(owner, "pgsv_temp", nil)
			temp := pgSequencesDefault(owner, "pgsv_temp", int64(1))
			temp[0] = schema
			query := "SELECT * FROM pg_sequences WHERE sequencename='pgsv_temp'"
			fixture.PgDescriptionExecutorRows(t, p, b, s.C, pgSequencesSelect(b, query, [][]any{persistent, temp}))
			pgSequencesCheck(t, p, b, s.Peer, "pgsv_temp", persistent)
			s.Retire()
			pgSequencesCheck(t, p, b, s.Peer, "pgsv_temp", persistent)
		})
	}
}
