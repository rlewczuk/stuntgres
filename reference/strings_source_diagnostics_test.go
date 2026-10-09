package reference_test

import (
	"strconv"
	"testing"
)

func TestReferenceStringLiteralSourceTypedEscapes(t *testing.T) {
	p := newReferenceScenario(t)
	step := func(s referenceStep) { s.nonContext = true; p.step(t, s) }
	step(referenceExec("CREATE TABLE inputs(id INT,big BIGINT,flag BOOL)", 0))
	for _, prepared := range []bool{false, true} {
		s := referenceExec(`INSERT INTO inputs VALUES (E'\061',E'\062',E'\164')`, 1)
		s.prepared = prepared
		step(s)
	}
	sentinel := referenceSelect("SELECT * FROM inputs", []string{"id", "big", "flag"}, []string{"INT4", "INT8", "BOOL"}, [][]any{{int64(1), int64(2), true}, {int64(1), int64(2), true}})
	sentinel.bag = true
	step(sentinel)
	for _, tc := range []struct{ column, typ string }{{"id", "integer"}, {"big", "bigint"}, {"flag", "boolean"}} {
		q := "INSERT INTO inputs (" + tc.column + ") VALUES ('1'),(E'\\x71')"
		position := referencePosition(len("INSERT INTO inputs ("+tc.column+") VALUES ('1'),(") + 1)
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError(q, referenceDiagnostic{Code: "22P02", Message: "invalid input syntax for type " + tc.typ + `: "q"`, Position: position})
			s.prepared = prepared
			s.errorStage = "execute"
			if prepared {
				s.errorStage = "prepare"
			}
			step(s)
			step(sentinel)
		}
	}
}

func TestReferenceStringLiteralSourceDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			peer := db
			if !backend.pg {
				peer = p.native
			}
			if err := peer.Ping(); err != nil {
				t.Fatal(err)
			}
			run := func(s referenceStep) { s.nonContext = true; referenceRunStep(t, p.ctx, db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(label TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES ('seed')", 1))
			sentinel := referenceSelect("SELECT * FROM t", []string{"label"}, []string{"TEXT"}, [][]any{{"seed"}})
			for _, tc := range stringLiteralErrors() {
				for _, prefix := range []string{"", "/*é*/ ", "/*😀*/ "} {
					position := tc.position
					if prefix != "" && position != "" {
						n, _ := strconv.Atoi(position)
						position = strconv.Itoa(n + 6)
					}
					for _, op := range []string{"exec", "query", "prepare"} {
						s := referenceImplicitError(prefix+"INSERT INTO t VALUES ("+tc.source, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: position, Hint: tc.hint})
						s.selectRows = op == "query"
						s.prepared = op == "prepare"
						s.errorStage = "execute"
						if s.prepared {
							s.errorStage = "prepare"
						}
						run(s)
					}
				}
				position := tc.position
				if position != "" {
					n, _ := strconv.Atoi(position)
					position = strconv.Itoa(n + 10)
				}
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError("INSERT INTO t VALUES ('valid'),("+tc.source, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: position, Hint: tc.hint})
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					run(s)
					run(sentinel)
					peerSentinel := sentinel
					peerSentinel.nonContext = true
					referenceRunStep(t, p.ctx, peer, backend.recorder, peerSentinel)
				}
			}
		})
	}
}
