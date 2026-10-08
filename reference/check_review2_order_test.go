//go:build reference

package reference_test

import (
	"fmt"
	"testing"
)

func referenceCheckReview2OrderError(detail string) referenceDiagnostic {
	return referenceDiagnostic{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
}

func TestReferenceCheckReview2OrderReverseMixed(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			// The shared CHECK fixture runs PostgreSQL's entire independent
			// expected stream first, so a native failure cannot mask its golden.
			// Prospective golden: REL_18_0 dependency.c:912-919,1045-1050
			// sorts object addresses descending and reports them in reverse.
			// Earlier view creation must precede the later CHECK, not vice versa.
			referenceCheckBackends(t, func(t *testing.T, run func(referenceStep)) {
				exec := func(q string, count int64) {
					s := referenceExec(q, count)
					s.prepared = prepared
					run(s)
				}
				exec("CREATE SEQUENCE s", 0)
				exec("CREATE VIEW v AS SELECT nextval('s') AS n", 0)
				exec("CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')>0))", 0)
				exec("INSERT INTO t VALUES(7)", 1)
				s := referenceImplicitError("DROP SEQUENCE s", referenceCheckReview2OrderError("view v depends on sequence s\nconstraint c on table t depends on sequence s"))
				s.prepared = prepared
				run(s)
				run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(1)}}))
				run(referenceSelect("SELECT n FROM v", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
				exec("INSERT INTO t VALUES(8)", 1)
				run(referenceSelect("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
			})
		})
	}
}

func TestReferenceCheckReview2OrderMultipleOwners(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			referenceCheckBackends(t, func(t *testing.T, run func(referenceStep)) {
				run(referenceExec("CREATE SEQUENCE s", 0))
				run(referenceExec("CREATE TABLE z(a INT CONSTRAINT cz CHECK(nextval('s')>0) NO INHERIT)", 0))
				run(referenceExec("CREATE TABLE a(a INT CONSTRAINT ca CHECK(nextval('s')>0))", 0))
				run(referenceExec("INSERT INTO z VALUES(11)", 1))
				run(referenceExec("INSERT INTO a VALUES(22)", 1))
				// Independent address-order expectation: names are intentionally
				// reversed. Neither backend's observed Detail supplies this golden.
				s := referenceImplicitError("DROP SEQUENCE s", referenceCheckReview2OrderError("constraint cz on table z depends on sequence s\nconstraint ca on table a depends on sequence s"))
				s.prepared = prepared
				run(s)
				run(referenceSelect("SELECT a FROM z", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}}))
				run(referenceSelect("SELECT a FROM a", []string{"a"}, []string{"INT4"}, [][]any{{int64(22)}}))
				run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}))
				run(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
			})
		})
	}
}
