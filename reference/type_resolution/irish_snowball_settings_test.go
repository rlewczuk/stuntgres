package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func irishConfigurationSettings(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	const currentSQL = "SELECT current_setting('default_text_search_config') AS config"
	const setSQL = "SELECT set_config('default_text_search_config',$1::text,false) AS config"
	var prior string
	if err := c.QueryRowContext(p.Ctx, currentSQL).Scan(&prior); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := c.ExecContext(ctx, "SELECT set_config('default_text_search_config',$1::text,false)", prior); err != nil {
			t.Error("restore session configuration", err)
		}
	}()
	current, err := c.PrepareContext(p.Ctx, currentSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	set, err := c.PrepareContext(p.Ctx, setSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	for _, input := range []string{"irish", "IRISH", "IrIsH", `pg_catalog."irish"`} {
		for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: set, SQL: setSQL}} {
			fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select(setSQL, []string{"config"}, []string{"TEXT"}, [][]any{{"pg_catalog.irish"}}, input))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: current, SQL: currentSQL}, b.Recorder, fixture.Select(currentSQL, []string{"config"}, []string{"TEXT"}, [][]any{{"pg_catalog.irish"}}))
		}
		regtsExec(t, p, b, c, "SET default_text_search_config='"+input+"'", 0)
	}
	for _, input := range []string{`pg_catalog."Irish"`, "irish_missing", "irish..invalid"} {
		d := dictionaryDiagnostic("22023", `invalid value for parameter "default_text_search_config": "`+input+`"`)
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SET default_text_search_config='"+input+"'", d)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: current, SQL: currentSQL}, b.Recorder, fixture.Select(currentSQL, []string{"config"}, []string{"TEXT"}, [][]any{{"pg_catalog.irish"}}))
		}
	}
	irishSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('RATS abhcóideacht agus') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'abhcóid':2 'agus':3 'rats':1"}}))
}
