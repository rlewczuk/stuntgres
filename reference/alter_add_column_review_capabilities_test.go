package reference_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestReferenceAlterAddColumnReviewDefaultLists(t *testing.T) {
	cases := []struct {
		name, query string
		drop        bool
	}{
		{"add_then_set", "ALTER TABLE t ADD c INT DEFAULT 7, ALTER b SET DEFAULT 9", false},
		{"set_then_add", "ALTER TABLE t ALTER b SET DEFAULT 9, ADD c INT DEFAULT 7", false},
		{"add_then_drop", "ALTER TABLE t ADD c INT DEFAULT 7, ALTER b DROP DEFAULT", true},
		{"drop_then_add", "ALTER TABLE t ALTER b DROP DEFAULT, ADD c INT DEFAULT 7", true},
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", tc.name, prepared), func(t *testing.T) {
				alterAddReferenceScenario(t, func(p *referencePair, b referenceBackend) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec("CREATE TABLE t(id INT,b INT DEFAULT 3)", 0))
					run(referenceExec("INSERT INTO t(id) VALUES(1)", 1))
					if b.pg {
						s := referenceExec(tc.query, 0)
						s.prepared = prepared
						run(s)
						run(referenceExec("INSERT INTO t(id) VALUES(2)", 1))
						var future any = int64(9)
						if tc.drop {
							future = nil
						}
						run(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(3), int64(7)}, {int64(2), future, int64(7)}}))
					} else {
						index := strings.Index(tc.query, ",")
						if strings.Contains(tc.name, "add_then") {
							index = strings.LastIndex(tc.query, "ALTER ")
						}
						failure := referenceImplicitError(tc.query, referenceDiagnostic{Code: "0A000", Message: "ALTER TABLE action lists are unsupported", Position: referencePosition(len([]rune(tc.query[:index])) + 1)})
						failure.prepared = prepared
						failure.errorStage = "execute"
						if prepared {
							failure.errorStage = "prepare"
						}
						run(failure)
						run(referenceSelect("SELECT * FROM t", []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(3)}}))
						run(referenceExec("INSERT INTO t(id) VALUES(2)", 1))
						run(referenceSelect("SELECT b FROM t ORDER BY id", []string{"b"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(3)}}))
					}
				})
			})
		}
	}
}

func TestReferenceAlterAddColumnReviewSerialIdentityBoundary(t *testing.T) {
	for _, identity := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("identity=%v/prepared=%v", identity, prepared), func(t *testing.T) {
				alterAddReferenceScenario(t, func(p *referencePair, b referenceBackend) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec("CREATE TABLE t(id INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
					query := "ALTER TABLE t ADD b SERIAL"
					message := "ADD COLUMN serial declarations are unsupported"
					token := "b SERIAL"
					if identity {
						query = "ALTER TABLE t ADD b INT GENERATED ALWAYS AS IDENTITY"
						message = "ADD COLUMN identity declarations are unsupported"
						token = "GENERATED"
					}
					if b.pg {
						s := referenceExec(query, 0)
						s.prepared = prepared
						run(s)
						bag := referenceSelect("SELECT b FROM t", []string{"b"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
						bag.bag = true
						run(bag)
						run(referenceExec("INSERT INTO t(id) VALUES(3)", 1))
						run(referenceSelect("SELECT b FROM t WHERE id=3", []string{"b"}, []string{"INT4"}, [][]any{{int64(3)}}))
						referenceTableDDLMetadata(t, p.ctx, b.db, b.pg, prepared, "SELECT * FROM t", []string{"id", "b"}, []string{"INT4", "INT4"}, []bool{true, false}, 3)
					} else {
						failure := referenceImplicitError(query, referenceDiagnostic{Code: "0A000", Message: message, Position: referencePosition(len([]rune(query[:strings.Index(query, token)])) + 1)})
						failure.prepared = prepared
						failure.errorStage = "execute"
						run(failure)
						run(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
						run(referenceExec("ALTER TABLE t ADD b INT DEFAULT 7", 0))
						run(referenceSelect("SELECT b FROM t ORDER BY id", []string{"b"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}))
					}
				})
			})
		}
	}
}
