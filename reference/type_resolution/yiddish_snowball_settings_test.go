package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func yiddishConfigurationSettings(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	const currentSQL = "SELECT current_setting('default_text_search_config') AS config"
	const setSQL = "SELECT set_config('default_text_search_config',$1::text,false) AS config"
	var prior string
	if err := c.QueryRowContext(p.Ctx, currentSQL).Scan(&prior); err != nil { t.Fatal(err) }
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second); defer cancel()
		if _, err := c.ExecContext(ctx, "SELECT set_config('default_text_search_config',$1::text,false)", prior); err != nil { t.Error("restore prior configuration", err) }
	}()
	current, err := c.PrepareContext(p.Ctx, currentSQL)
	if err != nil { t.Fatal(err) }
	defer current.Close()
	set, err := c.PrepareContext(p.Ctx, setSQL)
	if err != nil { t.Fatal(err) }
	defer set.Close()
	heldCurrent := fixture.HeldStatement{Executor: c, Statement: current, SQL: currentSQL}
	heldSet := fixture.HeldStatement{Executor: c, Statement: set, SQL: setSQL}
	for _, input := range []string{"yiddish", "YIDDISH", "YiDdIsH", "pg_catalog.yiddish", `pg_catalog."yiddish"`} {
		for _, e := range []fixture.Executor{c, heldSet} {
			fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select(setSQL, []string{"config"}, []string{"TEXT"}, [][]any{{"pg_catalog.yiddish"}}, input))
			fixture.RunStep(t, p.Ctx, heldCurrent, b.Recorder, fixture.Select(currentSQL, []string{"config"}, []string{"TEXT"}, [][]any{{"pg_catalog.yiddish"}}))
		}
		regtsExec(t, p, b, c, "SET default_text_search_config='"+input+"'", 0)
	}
	for _, input := range []string{`pg_catalog."Yiddish"`, "yiddish_missing", "yiddish..invalid"} {
		d := dictionaryDiagnostic("22023", `invalid value for parameter "default_text_search_config": "`+input+`"`)
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SET default_text_search_config='"+input+"'", d)
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, heldCurrent, b.Recorder, fixture.Select(currentSQL, []string{"config"}, []string{"TEXT"}, [][]any{{"pg_catalog.yiddish"}}))
		}
		s := fixture.ErrorDiagnostic(setSQL, d, input)
		s.SelectRows, s.ErrorStage = true, "execute"
		for _, e := range []fixture.Executor{c, heldSet} {
			fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, heldCurrent, b.Recorder, fixture.Select(currentSQL, []string{"config"}, []string{"TEXT"}, [][]any{{"pg_catalog.yiddish"}}))
		}
	}
	lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('קינדער RATS') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'rats':2 'קינד':1"}}))
}
