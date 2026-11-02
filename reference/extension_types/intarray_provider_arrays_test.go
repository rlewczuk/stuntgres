package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func intarraySeedArrays(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ia_values(id integer NOT NULL,v integer[])", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ia_values VALUES (1,'{1,-2,3}'),(2,'{}'),(3,'[0:1]={4,5}'),(4,'{{1,2},{3,4}}'),(5,'{1,NULL,3}'),(6,NULL)", 6))
}

func intarrayArrayRows() [][]any {
	return [][]any{{int64(1), "{1,-2,3}"}, {int64(2), "{}"}, {int64(3), "[0:1]={4,5}"}, {int64(4), "{{1,2},{3,4}}"}, {int64(5), "{1,NULL,3}"}, {int64(6), nil}}
}

func intarrayArrayCheck(b fixture.Backend) fixture.Step {
	return intarraySelect(b, "SELECT id,v FROM ia_values ORDER BY id", []string{"id", "v"}, []string{"INT4", "_INT4"}, []bool{false, true}, intarrayArrayRows())
}

func intarrayArrayReads(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	intarrayBoth(t, p, b, c, intarrayArrayCheck(b))
	for _, q := range []string{"SELECT v FROM ia_values WHERE id=6", "SELECT v FROM ia_values WHERE id=99"} {
		values := [][]any{{nil}}
		if q == "SELECT v FROM ia_values WHERE id=99" {
			values = [][]any{}
		}
		intarrayBoth(t, p, b, c, intarraySelect(b, q, []string{"v"}, []string{"_INT4"}, []bool{true}, values))
	}
	q := "SELECT $1::integer[] AS v"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, value := range []any{"{1,-2,3}", "{}", "[0:1]={4,5}", "{{1,2},{3,4}}", "{1,NULL,3}", "{-2147483648,2147483647}", nil} {
		arguments := []any{value}
		if value != nil {
			arguments = append(arguments, []byte(value.(string)))
		}
		for _, argument := range arguments {
			s := intarraySelect(b, q, []string{"v"}, []string{"_INT4"}, nil, [][]any{{value}}, argument)
			intarrayBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
	}
}

func TestReferenceIntarrayProviderSharedArrays(t *testing.T) {
	intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		intarraySeedArrays(t, p, b, c)
		intarrayArrayReads(t, p, b, c)
		intarrayInstall(t, p, b, c)
		intarrayArrayReads(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP EXTENSION intarray", 0))
		intarrayArrayReads(t, p, b, c)
		for _, prepared := range []bool{false, true} {
			want := fixture.ControlsReferenceDiagnostic("22003", `value "2147483648" is out of range for type integer`)
			want.Position = "8"
			stage := "execute"
			if prepared {
				stage = "prepare"
			}
			intarrayError(t, p, b, c, "SELECT '{2147483648}'::integer[]", prepared, stage, want)
			want.Position = ""
			want.Context = "unnamed portal parameter $1 = '...'"
			intarrayError(t, p, b, c, "SELECT $1::integer[]", prepared, "execute", want, "{2147483648}")
		}
	})
}
