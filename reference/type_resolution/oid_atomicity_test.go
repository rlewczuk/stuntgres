package reference_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceOIDAtomicityAndCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE o(v OID)", 0))
	p.Step(t, fixture.Exec("INSERT INTO o VALUES('7')", 1))
	p.Step(t, fixture.Exec("CREATE TABLE empty(v OID)", 0))
	eager := fixture.ImplicitError("SELECT 'x'::oid FROM empty", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "x"`, Position: "8"})
	eager.ErrorStage, eager.SelectRows = "prepare", true
	p.Step(t, fixture.Prepared(eager))
	for _, backend := range p.Backends() {
		func() {
			q := "INSERT INTO o VALUES('1'),($1)"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			unchanged := fixture.Select("SELECT v FROM o", []string{"v"}, []string{"OID"}, [][]any{{int64(7)}})
			for _, tc := range []struct{ text, code string }{{"x", "22P02"}, {"0b10", "22P02"}, {"1_000", "22P02"}, {"4294967296x", "22P02"}, {"4294967296", "22003"}, {"-2147483649", "22003"}, {"18446744073709551616x", "22003"}} {
				message := `invalid input syntax for type oid: "` + tc.text + `"`
				if tc.code == "22003" {
					message = `value "` + tc.text + `" is out of range for type oid`
				}
				literal := "/*é*/ INSERT INTO o VALUES('1'),('" + tc.text + "')"
				position := strconv.Itoa(utf8.RuneCountInString(literal[:strings.LastIndex(literal, "('")]) + 2)
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(literal, fixture.Diagnostic{Code: tc.code, Message: message, Position: position})
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					fixture.OidReferenceRows(t, p.Ctx, backend, backend.DB, unchanged)
				}
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: message, Context: "unnamed portal parameter $1 = '...'"}, tc.text)
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
				fixture.OidReferenceRows(t, p.Ctx, backend, backend.DB, unchanged)
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.ImplicitError("INSERT INTO o VALUES('1'),((-1)::int8::oid)", fixture.Diagnostic{Code: "22003", Message: "OID out of range"}))
			fixture.OidReferenceRows(t, p.Ctx, backend, backend.DB, unchanged)
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			statement, err := backend.DB.PrepareContext(ctx, "SELECT v FROM o")
			if statement != nil {
				statement.Close()
				t.Fatal("canceled Prepare exposed statement")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := backend.DB.ExecContext(ctx, "INSERT INTO o VALUES('0')"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			fixture.OidReferenceRows(t, p.Ctx, backend, backend.DB, unchanged)
		}()
	}
	p.Step(t, fixture.Exec("CREATE TABLE nn(v OID NOT NULL)", 0))
	p.Step(t, fixture.ImplicitError("INSERT INTO nn VALUES('4294967295'),(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "nn" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "nn", Column: "v"}))
	p.Step(t, fixture.Exec("CREATE TABLE detail(i INT NOT NULL,v OID)", 0))
	p.Step(t, fixture.ImplicitError("INSERT INTO detail VALUES(1,'0'),(NULL,'4294967295')", fixture.Diagnostic{Code: "23502", Message: `null value in column "i" of relation "detail" violates not-null constraint`, Detail: "Failing row contains (null, 4294967295).", Schema: "public", Table: "detail", Column: "i"}))
	for _, backend := range p.Backends() {
		fixture.OidReferenceRows(t, p.Ctx, backend, backend.DB, fixture.Select("SELECT v FROM nn", []string{"v"}, []string{"OID"}, nil))
		fixture.OidReferenceRows(t, p.Ctx, backend, backend.DB, fixture.Select("SELECT * FROM detail", []string{"i", "v"}, []string{"INT4", "OID"}, nil))
	}
	iteration, stop := context.WithCancel(p.Ctx)
	rows, err := p.Memory.QueryContext(context.WithValue(iteration, fixture.RecorderKey{}, p.MemoryRecord), "SELECT v FROM o")
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
