package reference_test

import "testing"

func TestReferenceGeneratedStoredIdentityGuard(t *testing.T) {
	for _, notNull := range []bool{false, true} {
		t.Run(map[bool]string{false: "nullable", true: "not-null"}[notNull], func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, run func(referenceStep)) {
				suffix := ""
				message := `column "b" of relation "g" must be declared NOT NULL before identity can be added`
				if notNull {
					suffix = " NOT NULL"
					message = `column "b" of relation "g" already has a default value`
				}
				run(referenceExec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED`+suffix+`)`, 0))
				run(referenceExec(`INSERT INTO g(a) VALUES(2)`, 1))
				run(referenceImplicitError(`ALTER TABLE g ALTER b ADD GENERATED ALWAYS AS IDENTITY`, referenceDiagnostic{Code: "55000", Message: message}))
				run(referenceSelect(`SELECT * FROM g`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
				run(referenceSelect(`SELECT relname FROM pg_class WHERE relname='g_b_seq'`, []string{"relname"}, []string{"NAME"}, nil))
				run(referenceExec(`INSERT INTO g(a) VALUES(4)`, 1))
				run(referenceSelect(`SELECT * FROM g WHERE a=4`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(5)}}))
			})
		})
	}
}
