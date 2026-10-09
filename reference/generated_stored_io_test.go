package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceGeneratedStoredByteaConcat(t *testing.T) {
	for _, domain := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("domain=%v/prepared=%v", domain, prepared), func(t *testing.T) {
				generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, run func(referenceStep)) {
					apply := func(s referenceStep) { s.prepared = prepared; run(s) }
					typ := "BYTEA"
					if domain {
						apply(referenceExec(`CREATE DOMAIN generation_bytes AS BYTEA`, 0))
						typ = "generation_bytes"
					}
					apply(referenceExec(`CREATE TABLE g(id INT,a `+typ+`,b TEXT GENERATED ALWAYS AS('x'::text || a) STORED,c TEXT GENERATED ALWAYS AS(a || 'x'::text) STORED)`, 0))
					apply(referenceExec(`INSERT INTO g(id,a) VALUES(1,'\x6162'),(2,NULL)`, 2))
					apply(referenceSelect(`SELECT id,a,b,c FROM g ORDER BY id`, []string{"id", "a", "b", "c"}, []string{"INT4", "BYTEA", "TEXT", "TEXT"}, [][]any{{int64(1), "ab", `x\x6162`, `\x6162x`}, {int64(2), nil, nil, nil}}))
					apply(referenceExec(`UPDATE g SET a='\x00ff' WHERE id=1`, 1))
					apply(referenceSelect(`SELECT id,a,b,c FROM g ORDER BY id`, []string{"id", "a", "b", "c"}, []string{"INT4", "BYTEA", "TEXT", "TEXT"}, [][]any{{int64(1), "\x00\xff", `x\x00ff`, `\x00ffx`}, {int64(2), nil, nil, nil}}))
					apply(referenceExec(`UPDATE g SET a=NULL WHERE id=1`, 1))
					apply(referenceSelect(`SELECT id,a,b,c FROM g ORDER BY id`, []string{"id", "a", "b", "c"}, []string{"INT4", "BYTEA", "TEXT", "TEXT"}, [][]any{{int64(1), nil, nil, nil}, {int64(2), nil, nil, nil}}))
				})
			})
		}
	}
}

func TestReferenceGeneratedStoredIntervalIODiagnostics(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("reverse=%v/prepared=%v", reverse, prepared), func(t *testing.T) {
				generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, run func(referenceStep)) {
					run(referenceExec(`CREATE DOMAIN generation_interval AS INTERVAL`, 0))
					q := `CREATE TABLE g(a generation_interval,b TEXT GENERATED ALWAYS AS(a::text) STORED)`
					if reverse {
						q = `CREATE TABLE g(a TEXT,b generation_interval GENERATED ALWAYS AS(a::generation_interval) STORED)`
					}
					s := referenceImplicitError(q, referenceDiagnostic{Code: "42P17", Message: "generation expression is not immutable"})
					s.prepared, s.errorStage = prepared, "execute"
					run(s)
					run(referenceImplicitError(`SELECT * FROM g`, referenceDiagnostic{Code: "42P01", Message: `relation "g" does not exist`, Position: "15"}))
					run(referenceExec(`CREATE TABLE g(a INT)`, 0))
					run(referenceExec(`INSERT INTO g VALUES(7)`, 1))
					run(referenceSelect(`SELECT a FROM g`, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				})
			})
		}
	}
}

func TestReferenceGeneratedStoredIntervalIOPlanningControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, run func(referenceStep)) {
				apply := func(s referenceStep) { s.prepared = prepared; run(s) }
				apply(referenceExec(`CREATE DOMAIN generation_interval AS INTERVAL`, 0))
				apply(referenceExec(`CREATE TABLE g(id INT,a generation_interval,b generation_interval GENERATED ALWAYS AS(a) STORED,c TEXT GENERATED ALWAYS AS(CASE WHEN FALSE THEN a::text ELSE 'safe'::text END) STORED)`, 0))
				apply(referenceExec(`INSERT INTO g(id,a) VALUES(1,'1 day'),(2,NULL)`, 2))
				apply(referenceSelect(`SELECT id,a,b,c FROM g ORDER BY id`, []string{"id", "a", "b", "c"}, []string{"INT4", "INTERVAL", "INTERVAL", "TEXT"}, [][]any{{int64(1), "1 day", "1 day", "safe"}, {int64(2), nil, nil, "safe"}}))
				apply(referenceExec(`UPDATE g SET a='2 days' WHERE id=1`, 1))
				apply(referenceSelect(`SELECT id,a,b,c FROM g ORDER BY id`, []string{"id", "a", "b", "c"}, []string{"INT4", "INTERVAL", "INTERVAL", "TEXT"}, [][]any{{int64(1), "2 days", "2 days", "safe"}, {int64(2), nil, nil, "safe"}}))
				apply(referenceExec(`CREATE TABLE pruned(id INT,a TEXT,b generation_interval GENERATED ALWAYS AS(CASE WHEN FALSE THEN a::generation_interval ELSE NULL END) STORED)`, 0))
				apply(referenceExec(`INSERT INTO pruned(id,a) VALUES(1,'not an interval'),(2,NULL)`, 2))
				apply(referenceSelect(`SELECT id,a,b FROM pruned ORDER BY id`, []string{"id", "a", "b"}, []string{"INT4", "TEXT", "INTERVAL"}, [][]any{{int64(1), "not an interval", nil}, {int64(2), nil, nil}}))
				apply(referenceExec(`UPDATE pruned SET a='still invalid' WHERE id=1`, 1))
				apply(referenceSelect(`SELECT id,a,b FROM pruned ORDER BY id`, []string{"id", "a", "b"}, []string{"INT4", "TEXT", "INTERVAL"}, [][]any{{int64(1), "still invalid", nil}, {int64(2), nil, nil}}))
			})
		})
	}
}
