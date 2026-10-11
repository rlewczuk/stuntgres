package reference_test

import (
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringLiteralSourceTypedEscapes(t *testing.T) {
	p := fixture.NewScenario(t)
	step := func(s fixture.Step) { s.NonContext = true; p.Step(t, s) }
	step(fixture.Exec("CREATE TABLE inputs(id INT,big BIGINT,flag BOOL)", 0))
	for _, prepared := range []bool{false, true} {
		s := fixture.Exec(`INSERT INTO inputs VALUES (E'\061',E'\062',E'\164')`, 1)
		s.Prepared = prepared
		step(s)
	}
	sentinel := fixture.Select("SELECT * FROM inputs", []string{"id", "big", "flag"}, []string{"INT4", "INT8", "BOOL"}, [][]any{{int64(1), int64(2), true}, {int64(1), int64(2), true}})
	sentinel.Bag = true
	step(sentinel)
	for _, tc := range []struct{ column, typ string }{{"id", "integer"}, {"big", "bigint"}, {"flag", "boolean"}} {
		q := "INSERT INTO inputs (" + tc.column + ") VALUES ('1'),(E'\\x71')"
		position := fixture.Position(len("INSERT INTO inputs ("+tc.column+") VALUES ('1'),(") + 1)
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type " + tc.typ + `: "q"`, Position: position})
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			step(s)
			step(sentinel)
		}
	}
}

func TestReferenceStringLiteralSourceDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			peer := db
			if !backend.PG {
				peer = p.Native
			}
			if err := peer.Ping(); err != nil {
				t.Fatal(err)
			}
			run := func(s fixture.Step) { s.NonContext = true; fixture.RunStep(t, p.Ctx, db, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(label TEXT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES ('seed')", 1))
			sentinel := fixture.Select("SELECT * FROM t", []string{"label"}, []string{"TEXT"}, [][]any{{"seed"}})
			for _, tc := range stringLiteralErrors() {
				for _, prefix := range []string{"", "/*é*/ ", "/*😀*/ "} {
					position := tc.position
					if prefix != "" && position != "" {
						n, _ := strconv.Atoi(position)
						position = strconv.Itoa(n + 6)
					}
					for _, op := range []string{"exec", "query", "prepare"} {
						s := fixture.ImplicitError(prefix+"INSERT INTO t VALUES ("+tc.source, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: position, Hint: tc.hint})
						s.SelectRows = op == "query"
						s.Prepared = op == "prepare"
						s.ErrorStage = "execute"
						if s.Prepared {
							s.ErrorStage = "prepare"
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
					s := fixture.ImplicitError("INSERT INTO t VALUES ('valid'),("+tc.source, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: position, Hint: tc.hint})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
					run(sentinel)
					peerSentinel := sentinel
					peerSentinel.NonContext = true
					fixture.RunStep(t, p.Ctx, peer, backend.Recorder, peerSentinel)
				}
			}
		})
	}
}
