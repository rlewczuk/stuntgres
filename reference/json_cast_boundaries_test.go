package reference_test

import "testing"

func TestReferenceJSONCapabilityBoundaries(t *testing.T) {
	cases := []struct{ q, code, message, hint, pos string }{
		{`CREATE TABLE m(v json(2))`, "42601", `syntax error at or near "("`, "", "22"},
		{`SELECT CAST(1 AS json) AS v`, "42846", "cannot cast type integer to json", "", "8"},
		{`SELECT CAST(TRUE AS json) AS v`, "42846", "cannot cast type boolean to json", "", "8"},
		{`SELECT CAST(json 'null' AS integer) AS v`, "42846", "cannot cast type json to integer", "", "8"},
		{`SELECT v FROM u WHERE v=v`, "42883", "operator does not exist: json = json", "No operator matches the given name and argument types. You might need to add explicit type casts.", "24"},
		{`SELECT v FROM u ORDER BY v`, "42883", "could not identify an ordering operator for type json", "Use an explicit ordering operator or modify the query.", "26"},
		{`SELECT v FROM u GROUP BY v`, "42883", "could not identify an equality operator for type json", "", "26"},
		{`SELECT DISTINCT v FROM u`, "42883", "could not identify an equality operator for type json", "", "17"},
		{`CREATE INDEX json_idx ON u(v)`, "42704", `data type json has no default operator class for access method "btree"`, "You must specify an operator class for the index or define a default operator class for the data type.", ""},
	}
	for _, tc := range cases {
		t.Run(tc.q, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("CREATE TABLE u(v JSON)", 0))
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: tc.pos})
					if !backend.pg && tc.q == "SELECT DISTINCT v FROM u" {
						// DISTINCT is outside the native SELECT grammar, independent
						// of JSON's lack of an equality provider.
						s = referenceImplicitError(tc.q, referenceDiagnostic{Code: "42601", Message: `syntax error at or near "DISTINCT"`, Position: "8"})
					}
					s.errorStage = "execute"
					if prepared {
						s = referencePrepared(s)
						s.errorStage = "prepare"
						if tc.code == "42704" {
							s.errorStage = "execute"
						}
					}
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				}
			}
		})
	}
}
