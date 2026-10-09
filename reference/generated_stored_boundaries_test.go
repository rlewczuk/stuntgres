package reference_test

import "testing"

func TestReferenceGeneratedStoredDefinitionAtomicity(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, run func(referenceStep)) {
		run(referenceExec(`CREATE TABLE sentinel(a INT)`, 0))
		run(referenceExec(`INSERT INTO sentinel VALUES(7)`, 1))
		q := `CREATE SCHEMA generated_atomic CREATE TABLE first(a SERIAL) CREATE TABLE bad(a INT,b INT GENERATED ALWAYS AS(b+1) STORED)`
		run(referenceImplicitError(q, referenceDiagnostic{Code: "42P17", Message: `cannot use generated column "b" in column generation expression`, Detail: "A generated column cannot reference another generated column.", Position: generatedStoredPosition(q, "b+1")}))
		run(referenceExec(`CREATE SCHEMA generated_atomic`, 0))
		run(referenceSelect(`SELECT relname FROM pg_class WHERE relname='first' OR relname='first_a_seq' OR relname='bad'`, []string{"relname"}, []string{"NAME"}, nil))
		run(referenceSelect(`SELECT * FROM sentinel`, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
	})
}

func TestReferenceGeneratedStoredQualifiedForwardReferences(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, run func(referenceStep)) {
		run(referenceExec(`CREATE TABLE "g.dot"(b INT GENERATED ALWAYS AS("g.dot"."a.dot"+1) STORED,"a.dot" INT DEFAULT 7)`, 0))
		run(referenceExec(`INSERT INTO "g.dot" DEFAULT VALUES`, 1))
		run(referenceSelect(`SELECT * FROM "g.dot"`, []string{"b", "a.dot"}, []string{"INT4", "INT4"}, [][]any{{int64(8), int64(7)}}))
	})
}

func TestReferenceGeneratedStoredAdjacentBoundaries(t *testing.T) {
	for _, kind := range []string{"", " VIRTUAL"} {
		t.Run(kind, func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, run func(referenceStep)) {
				q := `CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1)` + kind + `)`
				if backend.pg {
					run(referenceExec(q, 0))
					run(referenceExec(`INSERT INTO g(a) VALUES(2)`, 1))
					run(referenceSelect(`SELECT * FROM g`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
				} else {
					run(referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "virtual generated columns are unsupported"}))
				}
			})
		})
	}
	generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, run func(referenceStep)) {
		run(referenceExec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
		run(referenceExec(`INSERT INTO g(a) VALUES(2)`, 1))
		q := `ALTER TABLE g ALTER COLUMN b TYPE BIGINT`
		if backend.pg {
			run(referenceExec(q, 0))
			run(referenceSelect(`SELECT * FROM g`, []string{"a", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(3)}}))
		} else {
			run(referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "altering type of a generated column is unsupported"}))
			run(referenceSelect(`SELECT * FROM g`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
		}
	})
}

func TestReferenceGeneratedStoredAddBoundary(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, run func(referenceStep)) {
		run(referenceExec(`CREATE TABLE g(a INT)`, 0))
		run(referenceExec(`INSERT INTO g VALUES(2)`, 1))
		q := `ALTER TABLE g ADD COLUMN b INT GENERATED ALWAYS AS(a+1) STORED`
		if backend.pg {
			run(referenceExec(q, 0))
			run(referenceSelect(`SELECT * FROM g`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
		} else {
			run(referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "ADD COLUMN generated declarations are unsupported", Position: generatedStoredPosition(q, "GENERATED")}))
			run(referenceSelect(`SELECT * FROM g`, []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}}))
		}
	})
}
