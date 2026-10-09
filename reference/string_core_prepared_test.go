package reference_test

import "testing"

func TestReferenceStringCorePreparedOwnershipAndReanalysis(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			stringCoreControlExec(t, db, "CREATE TABLE string_core_owned(id INT,v TEXT)")
			input := []byte(" é ")
			stringCoreControlExec(t, db, "INSERT INTO string_core_owned VALUES(1,normalize(btrim($1)))", input)
			input[1] = 'X'
			s, err := db.Prepare("SELECT btrim(v)||'!' AS v FROM string_core_owned ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			check := func() {
				t.Helper()
				rows, e := s.Query()
				stringCoreRetainedRows(t, rows, e, []string{"v"}, []string{"TEXT"}, [][]any{{"é!"}}, false)
			}
			check()
			stringCoreControlExec(t, db, "ALTER TABLE string_core_owned ALTER COLUMN v TYPE VARCHAR(20)")
			check()
			stringCoreControlExec(t, db, "ALTER TABLE string_core_owned ALTER COLUMN v TYPE CHAR(4)")
			check()
		})
	}
}
