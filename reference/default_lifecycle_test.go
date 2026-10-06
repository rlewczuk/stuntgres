//go:build reference

package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceDefaultExpressionsAndDescriptorLifecycle(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
			schemaReferenceCleanup(t, "nested")
			for _, token := range []string{"AND", "OR"} {
				run(referenceImplicitError(`CREATE TABLE bad(b BOOL DEFAULT TRUE `+token+` FALSE)`, referenceDiagnostic{Code: "42601", Message: `syntax error at or near "` + token + `"`, Position: "38"}))
			}
			run(referenceImplicitError(`CREATE TABLE e(id INT,b BOOL DEFAULT (1 < 2 AND NOT FALSE),n INT DEFAULT NULL,u BOOL DEFAULT (FALSE OR NULL),v BOOL DEFAULT NOT NULL)`, referenceDiagnostic{Code: "42601", Message: `syntax error at or near "NOT"`, Position: "125"}))
			run(referenceExec(`CREATE TABLE e(id INT,b BOOL DEFAULT (1 < 2 AND NOT FALSE),n INT DEFAULT NULL,u BOOL DEFAULT (FALSE OR NULL),v BOOL DEFAULT (NOT NULL))`, 0))
			run(referenceExec(`INSERT INTO e(id) VALUES(1),(2)`, 2))
			run(referenceSelect(`SELECT * FROM e ORDER BY id`, []string{"id", "b", "n", "u", "v"}, []string{"INT4", "BOOL", "INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, nil, nil, nil}, {int64(2), true, nil, nil, nil}}))
			run(referenceExec(`CREATE TABLE source(id INT,first INT DEFAULT 7 NULL,last INT CONSTRAINT def DEFAULT 9 CONSTRAINT nn NOT NULL)`, 0))
			run(referenceExec(`CREATE TABLE clone(LIKE source)`, 0))
			run(referenceExec(`INSERT INTO clone(id,last) VALUES(1,3)`, 1))
			run(referenceSelect(`SELECT * FROM clone`, []string{"id", "first", "last"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), nil, int64(3)}}))
			run(referenceExec(`INSERT INTO source(id) VALUES(1)`, 1))
			run(referenceExec(`COMMENT ON TABLE source IS 'preserve defaults'`, 0))
			run(referenceExec(`COMMENT ON COLUMN source.last IS 'surviving attribute'`, 0))
			run(referenceExec(`ALTER TABLE source ALTER COLUMN last DROP NOT NULL`, 0))
			run(referenceExec(`ALTER TABLE source ALTER COLUMN last SET NOT NULL`, 0))
			run(referenceExec(`CREATE TABLE IF NOT EXISTS source(i INT DEFAULT 'oops')`, 0))
			run(referenceExec(`ALTER TABLE source DROP COLUMN first`, 0))
			run(referenceExec(`INSERT INTO source(id) VALUES(2)`, 1))
			run(referenceSelect(`SELECT * FROM source ORDER BY id`, []string{"id", "last"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}, {int64(2), int64(9)}}))
			run(referenceExec(`CREATE SCHEMA nested CREATE TABLE d(i INT DEFAULT 17)`, 0))
			run(referenceExec(`INSERT INTO nested.d DEFAULT VALUES`, 1))
			run(referenceSelect(`SELECT * FROM nested.d`, []string{"i"}, []string{"INT4"}, [][]any{{int64(17)}}))
		})
	}
}
