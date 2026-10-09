//go:build reference

package reference_test

import "testing"

func TestReferenceViewOracle(t *testing.T) {
	p := newReferencePair(t)
	setup := referenceExec("CREATE TABLE t(id INT,txt TEXT)", 0)
	p.step(t, setup)
	for _, tc := range []struct {
		query      string
		diagnostic referenceDiagnostic
	}{
		{"CREATE VIEW bad AS SELECT $1 FROM t", referenceDiagnostic{Code: "42P02", Message: "there is no parameter $1", Position: "27"}},
		{"CREATE VIEW bad WITH (security_barrier=wat) AS SELECT id FROM t", referenceDiagnostic{Code: "22023", Message: `invalid value for boolean option "security_barrier": wat`}},
		{"CREATE VIEW bad WITH (unknown_option=true) AS SELECT id FROM t", referenceDiagnostic{Code: "22023", Message: `unrecognized parameter "unknown_option"`}},
		{"CREATE VIEW bad WITH (security_invoker=true, security_invoker=false) AS SELECT id FROM t", referenceDiagnostic{Code: "22023", Message: `parameter "security_invoker" specified more than once`}},
		{"CREATE VIEW bad(x,y,z) AS SELECT id,txt FROM t", referenceDiagnostic{Code: "42601", Message: "CREATE VIEW specifies more column names than columns"}},
		{"CREATE VIEW bad AS SELECT id,id FROM t", referenceDiagnostic{Code: "42701", Message: `column "id" specified more than once`}},
		{`CREATE VIEW "ébad" AS SELECT missing FROM t`, referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "30"}},
		{"CREATE VIEW bad AS SELECT id FROM absent", referenceDiagnostic{Code: "42P01", Message: `relation "absent" does not exist`, Position: "35"}},
		{"CREATE VIEW bad AS SELECT id FROM t WHERE id", referenceDiagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "43"}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			step := referenceImplicitError(tc.query, tc.diagnostic)
			t.Run("postgres_first", func(t *testing.T) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, step) })
			t.Run("paired", func(t *testing.T) { p.step(t, step) })
		})
	}
}
