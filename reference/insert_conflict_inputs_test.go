package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceInsertConflictProducers(t *testing.T) {
	for _, source := range []string{"VALUES(1),(2)", "SELECT k FROM s ORDER BY k LIMIT 3", "SELECT k FROM s UNION SELECT k FROM s", "(SELECT k FROM s)", "SELECT k FROM s WHERE TRUE"} {
		t.Run(source, func(t *testing.T) {
			insertConflictSteps(t, referenceExec("CREATE TABLE s(k INT)", 0), referenceExec("INSERT INTO s VALUES(1),(1),(2)", 3), referenceExec("CREATE TABLE t(k INT UNIQUE)", 0), referenceExec("INSERT INTO t VALUES(1)", 1),
				referenceExec("INSERT INTO t(k) "+source+" ON CONFLICT(k) DO NOTHING", 1), insertConflictRows("SELECT k FROM t ORDER BY k", []any{int64(1)}, []any{int64(2)}))
		})
	}
	insertConflictSteps(t, referenceExec("CREATE TABLE t(k INT DEFAULT 1 UNIQUE)", 0), referenceExec("INSERT INTO t DEFAULT VALUES ON CONFLICT DO NOTHING", 1), referenceExec("INSERT INTO t DEFAULT VALUES ON CONFLICT(k) DO NOTHING", 0), insertConflictRows("SELECT k FROM t", []any{int64(1)}))
	insertConflictSteps(t, referenceExec("CREATE TABLE t(k INT GENERATED ALWAYS AS IDENTITY UNIQUE,p TEXT)", 0), referenceExec("INSERT INTO t OVERRIDING SYSTEM VALUE VALUES(1,'kept')", 1), referenceExec("INSERT INTO t OVERRIDING SYSTEM VALUE VALUES(1,'ignored'),(2,'new') ON CONFLICT(k) DO NOTHING", 1), referenceExec("INSERT INTO t OVERRIDING USER VALUE VALUES(99,'ignored') ON CONFLICT(k) DO NOTHING", 0), insertConflictRows("SELECT k FROM t ORDER BY k", []any{int64(1)}, []any{int64(2)}))
	insertConflictSteps(t, referenceExec("CREATE TABLE t(n INT,k INT GENERATED ALWAYS AS (n+1) STORED UNIQUE)", 0), referenceExec("INSERT INTO t(n) VALUES(1),(1),(2) ON CONFLICT(k) DO NOTHING", 2), insertConflictRows("SELECT k FROM t ORDER BY k", []any{int64(2)}, []any{int64(3)}))
}
func TestReferenceInsertConflictInputs(t *testing.T) {
	insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE t(k INT UNIQUE,p TEXT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,'kept')", 1))
		query := "INSERT INTO t(k,p) VALUES($1,$2) ON CONFLICT(k) DO NOTHING"
		stmt, err := b.db.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: query}
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(query, 1, int64(2), "new"))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(query, 0, int64(1), nil))
		for _, args := range [][]any{{int64(3)}, {int64(3), "x", true}} {
			r, err := stmt.ExecContext(p.ctx, args...)
			if err == nil || r != nil {
				t.Fatal("invalid parameter frame accepted", args, r, err)
			}
		}
		run(referenceSelect("SELECT k,p FROM t ORDER BY k", []string{"k", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "kept"}, {int64(2), "new"}}))
	})
}
func TestReferenceInsertConflictSyntax(t *testing.T) {
	insertConflictSteps(t, referenceExec("CREATE TABLE \"conflict\"(\"nothing\" INT CONSTRAINT \"do\" UNIQUE)", 0), referenceExec("INSERT INTO \"conflict\" VALUES(1),(1) ON CONFLICT ON CONSTRAINT \"do\" DO NOTHING", 1), referenceSelect("SELECT \"nothing\" FROM \"conflict\"", []string{"nothing"}, []string{"INT4"}, [][]any{{int64(1)}}))
	for _, suffix := range []string{"DO NOTHING garbage", "DO NOTHING DO NOTHING", "DO", "DO NOTHING WHERE TRUE"} {
		t.Run(suffix, func(t *testing.T) {
			insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE TABLE t(k INT UNIQUE)", 0))
				q := "INSERT INTO t VALUES(1) ON CONFLICT " + suffix
				token := ""
				position := len(q) + 1
				message := "syntax error at end of input"
				switch suffix {
				case "DO NOTHING garbage":
					token = "garbage"
				case "DO NOTHING DO NOTHING":
					token = "DO"
				case "DO NOTHING WHERE TRUE":
					token = "WHERE"
				}
				if token != "" {
					position = strings.LastIndex(q, token) + 1
					message = "syntax error at or near \"" + token + "\""
				}
				want := controlsReferenceDiagnostic("42601", message)
				want.Position = referencePosition(position)
				s := referenceErrorDiagnostic(q, want)
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
				s.prepared = true
				s.errorStage = "prepare"
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
				referenceRunStep(t, p.ctx, b.db, b.recorder, insertConflictRows("SELECT k FROM t"))
			})
		})
	}
}
func TestReferenceInsertConflictValidationAtomicity(t *testing.T) {
	insertConflictSteps(t, referenceExec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE,p INT NOT NULL,CONSTRAINT positive CHECK(p>0))", 0), referenceExec("INSERT INTO t VALUES(1,1)", 1),
		referenceErrorDiagnostic("INSERT INTO t VALUES(2,2),(1,-1) ON CONFLICT DO NOTHING", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: "new row for relation \"t\" violates check constraint \"positive\"", Detail: "Failing row contains (1, -1).", Schema: "public", Table: "t", Constraint: "positive"}),
		referenceErrorDiagnostic("INSERT INTO t VALUES(1,NULL) ON CONFLICT(k) DO NOTHING", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: "null value in column \"p\" of relation \"t\" violates not-null constraint", Detail: "Failing row contains (1, null).", Schema: "public", Table: "t", Column: "p"}),
		referenceSelect("SELECT k,p FROM t", []string{"k", "p"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
	insertConflictSteps(t, referenceExec("CREATE SEQUENCE draws", 0), referenceExec("CREATE TABLE t(k INT UNIQUE,p INT DEFAULT nextval('draws'),CONSTRAINT positive CHECK(k>0))", 0), referenceExec("INSERT INTO t(k) VALUES(1)", 1), referenceExec("INSERT INTO t(k) VALUES(1),(2) ON CONFLICT DO NOTHING", 1),
		referenceSelect("SELECT last_value FROM draws", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(3)}}),
		referenceErrorDiagnostic("INSERT INTO t(k) VALUES(3),(-1) ON CONFLICT DO NOTHING", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: "new row for relation \"t\" violates check constraint \"positive\"", Detail: "Failing row contains (-1, 5).", Schema: "public", Table: "t", Constraint: "positive"}),
		referenceSelect("SELECT last_value FROM draws", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(5)}}), insertConflictRows("SELECT k FROM t ORDER BY k", []any{int64(1)}, []any{int64(2)}))
	for _, q := range []string{"INSERT INTO t VALUES(1,'bad'::INT) ON CONFLICT DO NOTHING", "INSERT INTO t SELECT 1,1/0 ON CONFLICT DO NOTHING"} {
		t.Run(q, func(t *testing.T) {
			insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				referenceLockExec(t, p.ctx, b.db, "CREATE TABLE t(k INT UNIQUE,p INT)", 0)
				referenceLockExec(t, p.ctx, b.db, "INSERT INTO t VALUES(1,1)", 1)
				d := controlsReferenceDiagnostic("22012", "division by zero")
				if strings.Contains(q, "bad") {
					d = controlsReferenceDiagnostic("22P02", "invalid input syntax for type integer: \"bad\"")
					d.Position = referencePosition(strings.Index(q, "'bad'") + 1)
				}
				referenceRunStep(t, p.ctx, b.db, b.recorder, referenceErrorDiagnostic(q, d))
				referenceRunStep(t, p.ctx, b.db, b.recorder, insertConflictRows("SELECT k FROM t", []any{int64(1)}))
			})
		})
	}
}
