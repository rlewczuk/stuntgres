package reference_test

import (
	"strings"
	"testing"
)

func referenceByteaDiagnosticRoutes(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, q string, want referenceDiagnostic, ddl bool) {
	t.Helper()
	for _, route := range []string{"exec", "query", "prepare"} {
		s := referenceImplicitError(q, want)
		s.selectRows = route == "query" || route == "prepare" && strings.HasPrefix(q, "SELECT ")
		s.prepared = route == "prepare"
		s.errorStage = "execute"
		if s.prepared && !ddl {
			s.errorStage = "prepare"
		}
		referenceRunStep(t, p.ctx, x, b.recorder, s)
	}
}

func TestReferenceByteaDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			referenceByteaExec(t, p.ctx, b.db, b, referenceExec("CREATE TABLE t(v BYTEA)", 0))
			for _, tc := range []struct{ q, code, message, pos string }{
				{`INSERT INTO t VALUES('\x0')`, "22023", "invalid hexadecimal data: odd number of digits", "22"},
				{`INSERT INTO t VALUES('\xGG')`, "22023", `invalid hexadecimal digit: "G"`, "22"},
				{`INSERT INTO t VALUES('\q')`, "22P02", "invalid input syntax for type bytea", "22"},
				{`/*é*/ INSERT INTO t VALUES('\q')`, "22P02", "invalid input syntax for type bytea", "28"},
				{`INSERT INTO t VALUES('\x0 0')`, "22023", `invalid hexadecimal digit: " "`, "22"},
				{"INSERT INTO t VALUES('\\x\f00')", "22023", "invalid hexadecimal digit: \"\f\"", "22"},
				{"INSERT INTO t VALUES('\\x\v00')", "22023", "invalid hexadecimal digit: \"\v\"", "22"},
				{`INSERT INTO t VALUES('\X41')`, "22P02", "invalid input syntax for type bytea", "22"},
				{`INSERT INTO t VALUES('\400')`, "22P02", "invalid input syntax for type bytea", "22"},
				{`INSERT INTO t VALUES('\08')`, "22P02", "invalid input syntax for type bytea", "22"},
				{`INSERT INTO t VALUES('\00')`, "22P02", "invalid input syntax for type bytea", "22"},
				{`INSERT INTO t VALUES('\')`, "22P02", "invalid input syntax for type bytea", "22"},
				{`CREATE TABLE bad(v BYTEA(1))`, "42601", `type modifier is not allowed for type "bytea"`, "20"},
				{`CREATE TABLE bad(v "bytea"(1))`, "42601", `type modifier is not allowed for type "bytea"`, "20"},
				{`/*é*/ CREATE TABLE bad(v BYTEA(1))`, "42601", `type modifier is not allowed for type "bytea"`, "26"},
				{`SELECT v FROM t WHERE v='\q'`, "22P02", "invalid input syntax for type bytea", "25"},
			} {
				t.Run(tc.q, func(t *testing.T) {
					referenceByteaDiagnosticRoutes(t, p, b, b.db, tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.pos}, tc.code == "42601")
				})
			}
			referenceByteaRaw(t, p.ctx, b.db, b, "SELECT v FROM t", false, []string{"v"}, []int{1}, nil, false)
		})
	}
}

func TestReferenceByteaOutputDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			}()
			referenceByteaExec(t, p.ctx, conn, b, referenceExec("CREATE TABLE t(v BYTEA)", 0))
			referenceByteaExec(t, p.ctx, conn, b, referenceExec("CREATE TABLE nn(v BYTEA,n INTEGER NOT NULL)", 0))
			for _, mode := range []struct{ name, detail string }{
				{"hex", `Failing row contains (\x0020275c7e7f80ff, null).`},
				{"escape", "Failing row contains (\\000 '\\\\~\\177\\200\\377, null)."},
			} {
				referenceByteaExec(t, p.ctx, conn, b, referenceExec("SET bytea_output="+mode.name, 0))
				referenceRunStep(t, p.ctx, conn, b.recorder, referenceSelect("SHOW bytea_output", []string{"bytea_output"}, []string{"TEXT"}, [][]any{{mode.name}}))
				s := referenceImplicitError(`INSERT INTO nn VALUES('\x0020275c7e7f80ff',NULL)`, referenceDiagnostic{Code: "23502", Message: `null value in column "n" of relation "nn" violates not-null constraint`, Detail: mode.detail, Schema: "public", Table: "nn", Column: "n"})
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, conn, b.recorder, s)
			}
			s := referenceImplicitError("SET bytea_output=bogus", referenceDiagnostic{Code: "22023", Message: `invalid value for parameter "bytea_output": "bogus"`, Hint: "Available values: escape, hex."})
			s.errorStage = "execute"
			referenceRunStep(t, p.ctx, conn, b.recorder, s)
			referenceRunStep(t, p.ctx, conn, b.recorder, referenceSelect("SHOW bytea_output", []string{"bytea_output"}, []string{"TEXT"}, [][]any{{"escape"}}))
			referenceRunStep(t, p.ctx, conn, b.recorder, referenceError(`INSERT INTO t VALUES('\x41'),('\q')`, "22P02"))
			referenceByteaRaw(t, p.ctx, conn, b, "SELECT v FROM t", false, []string{"v"}, []int{1}, nil, false)
			referenceByteaExec(t, p.ctx, conn, b, referenceExec("SET bytea_output=DEFAULT", 0))
			referenceByteaExec(t, p.ctx, conn, b, referenceExec("BEGIN", 0))
			referenceByteaExec(t, p.ctx, conn, b, referenceExec("SET LOCAL bytea_output=escape", 0))
			referenceRunStep(t, p.ctx, conn, b.recorder, referenceSelect("SHOW bytea_output", []string{"bytea_output"}, []string{"TEXT"}, [][]any{{"escape"}}))
			local := referenceImplicitError(`INSERT INTO nn VALUES('\x00',NULL)`, referenceDiagnostic{Code: "23502", Message: `null value in column "n" of relation "nn" violates not-null constraint`, Detail: `Failing row contains (\000, null).`, Schema: "public", Table: "nn", Column: "n"})
			local.errorStage = "execute"
			referenceRunStep(t, p.ctx, conn, b.recorder, local)
			referenceByteaExec(t, p.ctx, conn, b, referenceExec("ROLLBACK", 0))
			referenceRunStep(t, p.ctx, conn, b.recorder, referenceSelect("SHOW bytea_output", []string{"bytea_output"}, []string{"TEXT"}, [][]any{{"hex"}}))
		})
	}
}
