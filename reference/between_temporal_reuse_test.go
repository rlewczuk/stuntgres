package reference_test

import "testing"

func TestReferenceBetweenTemporalLiteralReuse(t *testing.T) {
	p := betweenReferenceSetup(t)
	for _, typ := range []string{"TIME(3)", "TIMESTAMP(3)"} {
		input, lower, upper := "12:00:00.123", "11:00:00.123", "13:00:00.123"
		if typ == "TIMESTAMP(3)" {
			input = "2000-01-02 12:00:00.123"
			lower = "2000-01-01 12:00:00.123"
			upper = "2000-01-03 12:00:00.123"
		}
		for _, form := range betweenReferenceForms {
			q := "SELECT '" + input + "' " + form + " " + typ + " '" + lower + "' AND " + typ + " '" + upper + "' FROM t WHERE id=1"
			for _, backend := range p.backends() {
				stmt, err := backend.db.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := stmt.Close(); err != nil {
						t.Error(err)
					}
				}()
				for range 3 {
					scalarRunStep(t, p.ctx, referenceHeldStatement{backend.db, stmt, q}, backend.recorder, referenceSelect(q, []string{"?column?"}, []string{"BOOL"}, [][]any{{form == "BETWEEN" || form == "BETWEEN SYMMETRIC"}}))
				}
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}
