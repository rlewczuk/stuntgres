//go:build reference

package reference_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceDefaultExternalVolatileAndSubqueryBoundaries(t *testing.T) {
	p := newReferencePair(t)
	record := &referenceRecorder{}
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.postgres, record, s) }
	// The minimum fixed-nextval DEFAULT surface also supports native replay.
	run(referenceExec(`CREATE SEQUENCE default_seq`, 0))
	run(referenceExec(`CREATE TABLE volatile_d(id INT,v BIGINT DEFAULT nextval('default_seq'))`, 0))
	run(referenceExec(`INSERT INTO volatile_d(id) VALUES(1),(2),(3)`, 3))
	run(referenceExec(`INSERT INTO volatile_d VALUES(4,DEFAULT)`, 1))
	run(referenceSelect(`SELECT * FROM volatile_d ORDER BY id`, []string{"id", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}, {int64(4), int64(4)}}))
	run(referenceImplicitError(`CREATE TABLE bad (i integer DEFAULT (SELECT 1))`, referenceDiagnostic{Code: "0A000", Message: "cannot use subquery in DEFAULT expression", Position: "37"}))
	nativeRecord := &referenceRecorder{}
	for _, step := range []referenceStep{
		referenceExec(`CREATE SEQUENCE default_seq`, 0),
		referenceExec(`CREATE TABLE volatile_d(id INT,v BIGINT DEFAULT nextval('default_seq'))`, 0),
		referenceExec(`INSERT INTO volatile_d(id) VALUES(1),(2),(3)`, 3),
		referenceExec(`INSERT INTO volatile_d VALUES(4,DEFAULT)`, 1),
		referenceSelect(`SELECT * FROM volatile_d ORDER BY id`, []string{"id", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}, {int64(4), int64(4)}}),
	} {
		referenceRunStep(t, p.ctx, p.memory, nativeRecord, step)
	}
	// Native rejects the excluded grammar rather than inventing a function result.
	for _, tc := range []struct{ q, code, message, token string }{
		{`CREATE TABLE bad (i integer DEFAULT (SELECT 1))`, "42601", `syntax error at or near "SELECT"`, "SELECT"},
	} {
		position := fmt.Sprint(utf8.RuneCountInString(tc.q[:strings.Index(tc.q, tc.token)]) + 1)
		referenceRunStep(t, p.ctx, p.memory, &referenceRecorder{}, referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: position}))
	}
}
