package reference_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceOIDAtomicityAndCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE o(v OID)", 0))
	p.step(t, referenceExec("INSERT INTO o VALUES('7')", 1))
	p.step(t, referenceExec("CREATE TABLE empty(v OID)", 0))
	eager := referenceImplicitError("SELECT 'x'::oid FROM empty", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "x"`, Position: "8"})
	eager.errorStage, eager.selectRows = "prepare", true
	p.step(t, referencePrepared(eager))
	for _, backend := range p.backends() {
		func() {
			q := "INSERT INTO o VALUES('1'),($1)"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
			unchanged := referenceSelect("SELECT v FROM o", []string{"v"}, []string{"OID"}, [][]any{{int64(7)}})
			for _, tc := range []struct{ text, code string }{{"x", "22P02"}, {"0b10", "22P02"}, {"1_000", "22P02"}, {"4294967296x", "22P02"}, {"4294967296", "22003"}, {"-2147483649", "22003"}, {"18446744073709551616x", "22003"}} {
				message := `invalid input syntax for type oid: "` + tc.text + `"`
				if tc.code == "22003" {
					message = `value "` + tc.text + `" is out of range for type oid`
				}
				literal := "/*é*/ INSERT INTO o VALUES('1'),('" + tc.text + "')"
				position := strconv.Itoa(utf8.RuneCountInString(literal[:strings.LastIndex(literal, "('")]) + 2)
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(literal, referenceDiagnostic{Code: tc.code, Message: message, Position: position})
					s.prepared, s.errorStage = prepared, "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
					oidReferenceRows(t, p.ctx, backend, backend.db, unchanged)
				}
				s := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: message, Context: "unnamed portal parameter $1 = '...'"}, tc.text)
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, held, backend.recorder, s)
				oidReferenceRows(t, p.ctx, backend, backend.db, unchanged)
			}
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceImplicitError("INSERT INTO o VALUES('1'),((-1)::int8::oid)", referenceDiagnostic{Code: "22003", Message: "OID out of range"}))
			oidReferenceRows(t, p.ctx, backend, backend.db, unchanged)
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			statement, err := backend.db.PrepareContext(ctx, "SELECT v FROM o")
			if statement != nil {
				statement.Close()
				t.Fatal("canceled Prepare exposed statement")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := backend.db.ExecContext(ctx, "INSERT INTO o VALUES('0')"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			oidReferenceRows(t, p.ctx, backend, backend.db, unchanged)
		}()
	}
	p.step(t, referenceExec("CREATE TABLE nn(v OID NOT NULL)", 0))
	p.step(t, referenceImplicitError("INSERT INTO nn VALUES('4294967295'),(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "v" of relation "nn" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "nn", Column: "v"}))
	p.step(t, referenceExec("CREATE TABLE detail(i INT NOT NULL,v OID)", 0))
	p.step(t, referenceImplicitError("INSERT INTO detail VALUES(1,'0'),(NULL,'4294967295')", referenceDiagnostic{Code: "23502", Message: `null value in column "i" of relation "detail" violates not-null constraint`, Detail: "Failing row contains (null, 4294967295).", Schema: "public", Table: "detail", Column: "i"}))
	for _, backend := range p.backends() {
		oidReferenceRows(t, p.ctx, backend, backend.db, referenceSelect("SELECT v FROM nn", []string{"v"}, []string{"OID"}, nil))
		oidReferenceRows(t, p.ctx, backend, backend.db, referenceSelect("SELECT * FROM detail", []string{"i", "v"}, []string{"INT4", "OID"}, nil))
	}
	iteration, stop := context.WithCancel(p.ctx)
	rows, err := p.memory.QueryContext(context.WithValue(iteration, referenceRecorderKey{}, p.memoryRecord), "SELECT v FROM o")
	if err != nil {
		stop()
		t.Fatal(err)
	}
	defer rows.Close()
	stop()
	if rows.Next() || !errors.Is(rows.Err(), context.Canceled) {
		t.Fatal("native canceled OID iteration", rows.Err())
	}
}
