package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func romanianConfigurationSettings(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
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
	for _, input := range []string{"romanian", "ROMANIAN", "RoMaNiAn", `pg_catalog."romanian"`} {
		for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: set, SQL: setSQL}} {
			fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select(setSQL, []string{"config"}, []string{"TEXT"}, [][]any{{"pg_catalog.romanian"}}, input))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: current, SQL: currentSQL}, b.Recorder, fixture.Select(currentSQL, []string{"config"}, []string{"TEXT"}, [][]any{{"pg_catalog.romanian"}}))
		}
		regtsExec(t, p, b, c, "SET default_text_search_config='"+input+"'", 0)
	}
	for _, input := range []string{`pg_catalog."Romanian"`, "romanian_missing", "romanian..invalid"} {
		d := dictionaryDiagnostic("22023", `invalid value for parameter "default_text_search_config": "`+input+`"`)
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SET default_text_search_config='"+input+"'", d)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: current, SQL: currentSQL}, b.Recorder, fixture.Select(currentSQL, []string{"config"}, []string{"TEXT"}, [][]any{{"pg_catalog.romanian"}}))
		}
	}
	for _, prepared := range []bool{false, true} {
		d := dictionaryDiagnostic("0A000", "cross-database references are not implemented: foreign_db.pg_catalog.romanian")
		s := fixture.ErrorDiagnostic("SET default_text_search_config='foreign_db.pg_catalog.romanian'", d)
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: current, SQL: currentSQL}, b.Recorder, fixture.Select(currentSQL, []string{"config"}, []string{"TEXT"}, [][]any{{"pg_catalog.romanian"}}))
	}
	romanianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('OCOLUL abruptă și') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'abrupt':2 'ocol':1 'și':3"}}))
}
