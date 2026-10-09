package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceDefaultDeclarationPrecedence(t *testing.T) {
	// These are independently expected utility-transformation diagnostics.
	// PostgreSQL executes before native, including when native reports a
	// competing declaration/cooking error.
	for _, tc := range []struct{ name, query, code, message, token string }{
		{"duplicate-before-nullability", `CREATE TABLE bad(i INT DEFAULT 1 DEFAULT 2 NULL NOT NULL)`, "42601", `multiple default values specified for column "i" of table "bad"`, "DEFAULT 2"},
		{"duplicate-before-cooking", `CREATE TABLE bad(i INT DEFAULT 'oops',j INT DEFAULT 1 DEFAULT 2)`, "42601", `multiple default values specified for column "j" of table "bad"`, "DEFAULT 2"},
		{"nullability-before-duplicate", `CREATE TABLE bad(i INT NULL NOT NULL DEFAULT 1 DEFAULT 2)`, "42601", `conflicting NULL/NOT NULL declarations for column "i" of table "bad"`, "NOT NULL"},
		{"duplicate-column-before-bad-input", `CREATE TABLE bad(j INT DEFAULT 1 DEFAULT 2,i INT DEFAULT 'oops')`, "42601", `multiple default values specified for column "j" of table "bad"`, "DEFAULT 2"},
		{"duplicate-before-later-type", `CREATE TABLE bad(i INT DEFAULT 1 DEFAULT 2,j missing_type)`, "42601", `multiple default values specified for column "i" of table "bad"`, "DEFAULT 2"},
		{"type-before-later-duplicate", `CREATE TABLE bad(i missing_type,j INT DEFAULT 1 DEFAULT 2)`, "42704", `type "missing_type" does not exist`, "missing_type"},
		{"earlier-column-nullability", `CREATE TABLE bad(i INT NULL NOT NULL,j INT DEFAULT 1 DEFAULT 2)`, "42601", `conflicting NULL/NOT NULL declarations for column "i" of table "bad"`, "NOT NULL"},
		{"nullability-before-cooking", `CREATE TABLE bad(i INT DEFAULT 'oops',j INT NULL NOT NULL)`, "42601", `conflicting NULL/NOT NULL declarations for column "j" of table "bad"`, "NOT NULL"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.name, prepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				want := referenceDiagnostic{Code: tc.code, Message: tc.message, Position: fmt.Sprint(strings.Index(tc.query, tc.token) + 1)}
				s := referenceImplicitError(tc.query, want)
				s.prepared, s.errorStage = prepared, "execute"
				defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *referenceRecorder) {
					run := func(step referenceStep) { step.prepared = prepared; referenceRunStep(t, p.ctx, db, r, step) }
					run(referenceExec(`CREATE TABLE sentinel(id INT,v INT DEFAULT 7)`, 0))
					run(referenceExec(`INSERT INTO sentinel(id) VALUES(1)`, 1))
					var before bytes.Buffer
					if db == p.memory {
						if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
							t.Fatal(err)
						}
					}
					t.Run("full-diagnostic", func(t *testing.T) { referenceRunStep(t, p.ctx, db, r, s) })
					run(referenceImplicitError(`SELECT * FROM bad`, referenceDiagnostic{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"}))
					run(referenceSelect(`SELECT * FROM sentinel ORDER BY id`, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}}))
					if db == p.memory {
						var after bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
							t.Fatal("failed declaration changed persistent defaults/data", err, after.String())
						}
					}
					run(referenceExec(`INSERT INTO sentinel(id) VALUES(2)`, 1))
					run(referenceSelect(`SELECT * FROM sentinel ORDER BY id`, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(7)}}))
					run(referenceExec(`DELETE FROM sentinel WHERE id=2`, 1))
				})
				t.Run("native-failed-restore-preservation", func(t *testing.T) {
					var before bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
						t.Fatal(err)
					}
					err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(tc.query))
					t.Run("full-diagnostic", func(t *testing.T) { referenceAssertDiagnostic(t, err, s.diagnostic) })
					var after bytes.Buffer
					if e := stuntgres.Dump(p.ctx, p.native, &after); e != nil || before.String() != after.String() {
						t.Fatal("failed restore changed persistent defaults/data", e, after.String())
					}
					run := func(step referenceStep) { step.prepared = prepared; p.step(t, step) }
					run(referenceImplicitError(`SELECT * FROM bad`, referenceDiagnostic{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"}))
					run(referenceExec(`INSERT INTO sentinel(id) VALUES(2)`, 1))
					run(referenceSelect(`SELECT * FROM sentinel ORDER BY id`, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(7)}}))
				})
			})
		}
	}
}
