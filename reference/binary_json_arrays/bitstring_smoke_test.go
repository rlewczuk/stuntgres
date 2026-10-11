package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitstringFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE bs_smoke(id INTEGER,f BIT(3),v VARBIT(5),u VARBIT,d BIT,g \"bit\")", 0))
	p.Step(t, fixture.Exec("INSERT INTO bs_smoke VALUES(1,B'001',B'',X'0f',B'0',B'10'),(2,B'100',B'10',X'A',B'1',B''),(3,NULL,NULL,NULL,NULL,NULL)", 3))
	for _, backend := range p.Backends() {
		check := func() {
			s := fixture.Select("SELECT f,v,u,d,g FROM bs_smoke ORDER BY id", []string{"f", "v", "u", "d", "g"}, []string{"BIT", "VARBIT", "VARBIT", "BIT", "BIT"}, [][]any{{"001", "", "00001111", "0", "10"}, {"100", "10", "1010", "1", ""}, {nil, nil, nil, nil, nil}})
			s.CheckMetadata = bitstringReferenceMetadata(backend.PG, []int64{3, 5, -1, 1, -1}, []bool{true, true, true, true, true})
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
		}
		check()
		bitstringReferenceError(t, p, backend, backend.DB, "SELECT B'2'", nil, true, true, "22P02", `"2" is not a valid binary digit`, "8")
		bitstringReferenceError(t, p, backend, backend.DB, "INSERT INTO bs_smoke(d) VALUES(B'10')", nil, false, false, "22026", "bit string length 2 does not match type bit(1)", "")
		bitstringReferenceError(t, p, backend, backend.DB, "INSERT INTO bs_smoke(id,f) VALUES(9,B'001'),(10,B'10')", nil, false, false, "22026", "bit string length 2 does not match type bit(3)", "")
		check()
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE bs_smoke_bind(f BIT(3),v VARBIT(5))", 0))
		for _, tc := range []struct {
			column         string
			good, bad      any
			state, message string
		}{
			{"f", "001", "10", "22026", "bit string length 2 does not match type bit(3)"},
			{"v", "", "101010", "22001", "bit string too long for type bit varying(5)"},
		} {
			q := "INSERT INTO bs_smoke_bind(" + tc.column + ") VALUES($1)"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
				for _, value := range []any{tc.good, nil} {
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, value))
				}
				bitstringReferenceError(t, p, backend, held, q, []any{tc.bad}, false, false, tc.state, tc.message, "")
				bitstringReferenceError(t, p, backend, held, q, []any{"2"}, false, false, "22P02", `"2" is not a valid binary digit`, "")
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, tc.good))
			}()
		}
		s := fixture.Select("SELECT f,v FROM bs_smoke_bind", []string{"f", "v"}, []string{"BIT", "VARBIT"}, [][]any{{"001", nil}, {nil, nil}, {"001", nil}, {nil, ""}, {nil, nil}, {nil, ""}})
		s.Bag = true
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
	}
	// The control phase replays actual emitted native SQL on PostgreSQL. Remove
	// only this scenario's already-asserted PG objects to permit clean replay.
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE bs_smoke;DROP TABLE bs_smoke_bind"); err != nil {
			t.Fatal(err)
		}
	}
	bitstringControlRoundTrip(t, p)
	p.Step(t, fixture.Select("SELECT f,v,u,d,g FROM bs_smoke ORDER BY id", []string{"f", "v", "u", "d", "g"}, []string{"BIT", "VARBIT", "VARBIT", "BIT", "BIT"}, [][]any{{"001", "", "00001111", "0", "10"}, {"100", "10", "1010", "1", ""}, {nil, nil, nil, nil, nil}}))
}
