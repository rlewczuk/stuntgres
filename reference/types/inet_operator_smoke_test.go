package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceINETOperatorFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, s := range []fixture.Step{
			fixture.Select("SELECT ~ inet '192.168.1.6' AS n,inet '192.168.1.6' & inet '0.0.0.255' AS a,inet '192.168.1.6' | inet '0.0.0.255' AS o", []string{"n", "a", "o"}, []string{"INET", "INET", "INET"}, [][]any{{"63.87.254.249", "0.0.0.6", "192.168.1.255"}}),
			fixture.Select("SELECT inet '192.168.1.6' + 25 AS a,25 + inet '192.168.1.6' AS b,inet '192.168.1.43' - 36 AS c,inet '192.168.1.43' - inet '192.168.1.19' AS d", []string{"a", "b", "c", "d"}, []string{"INET", "INET", "INET", "INT8"}, [][]any{{"192.168.1.31", "192.168.1.31", "192.168.1.7", int64(24)}}),
			fixture.Select("SELECT inet '::ffff:ffff:ffff:ffff' + 1 AS carry,inet '192.168.1.255/24' + 1 AS prefix", []string{"carry", "prefix"}, []string{"INET", "INET"}, [][]any{{"0:0:0:1::", "192.168.2.0/24"}}),
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		}
		s := fixture.Select("SELECT NULL::inet + 1 AS h,NULL::inet - inet '::' AS d", []string{"h", "d"}, []string{"INET", "INT8"}, [][]any{{nil, nil}})
		s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
			inetOperatorMetadata(t, r, b.PG, []string{"h", "d"}, []string{"INET", "INT8"})
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		inetOperatorError(t, p, b, "SELECT inet '1.2.3.4' & inet '::1'", fixture.ControlsReferenceDiagnostic("22023", "cannot AND inet values of different sizes"))
		q := "SELECT $1::inet + $2::bigint AS h"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, s := range []fixture.Step{
			fixture.Select(q, []string{"h"}, []string{"INET"}, [][]any{{"::2"}}, "::1", int64(1)),
			fixture.Select(q, []string{"h"}, []string{"INET"}, [][]any{{"::3"}}, []byte("::2"), int64(1)),
			fixture.Select(q, []string{"h"}, []string{"INET"}, [][]any{{nil}}, nil, int64(1)),
			fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22003", "result is out of range"), "::", int64(-1)),
			fixture.Select(q, []string{"h"}, []string{"INET"}, [][]any{{"::4"}}, "::3", int64(1)),
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		for _, s := range inetOperatorStoredSetup {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		inetOperatorStoredAssertions(t, p, b)
	}
}
