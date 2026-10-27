package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringCorePreparedOwnershipAndReanalysis(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			for _, q := range []string{"SELECT pg_typeof($1||$2)::text AS v", "SELECT pg_typeof($1||42)::text AS v", "SELECT pg_typeof(42||$1)::text AS v"} {
				args := []any{"x"}
				if q == "SELECT pg_typeof($1||$2)::text AS v" {
					args = append(args, "y")
				}
				fixture.RunStep(t, p.Ctx, db, backend.Recorder, fixture.Prepared(fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"text"}}, args...)))
			}
			fixture.StringCoreControlExec(t, db, "CREATE TABLE string_core_owned(id INT,v TEXT)")
			input := []byte(" é ")
			fixture.StringCoreControlExec(t, db, "INSERT INTO string_core_owned VALUES(1,normalize(btrim($1)))", input)
			input[1] = 'X'
			s, err := db.Prepare("SELECT btrim(v)||'!' AS v FROM string_core_owned ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			check := func() {
				t.Helper()
				rows, e := s.Query()
				fixture.StringCoreRetainedRows(t, rows, e, []string{"v"}, []string{"TEXT"}, [][]any{{"é!"}}, false)
			}
			check()
			fixture.StringCoreControlExec(t, db, "ALTER TABLE string_core_owned ALTER COLUMN v TYPE VARCHAR(20)")
			check()
			fixture.StringCoreControlExec(t, db, "ALTER TABLE string_core_owned ALTER COLUMN v TYPE CHAR(4)")
			check()
		})
	}
}
