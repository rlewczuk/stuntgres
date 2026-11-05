package reference_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func postgresFDWUnboundControls(t *testing.T) {
	t.Helper()
	p := fixture.NewScenario(t)
	b := externalSQLNativeBackend(p)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	postgresFDWInstall(run)
	run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER pf_server", 0))
	run(fixture.Exec("CREATE FOREIGN TABLE pf_table(id integer) SERVER pf_server", 0))
	run(fixture.Exec("CREATE TABLE pf_local(v integer)", 0))
	run(fixture.Exec("INSERT INTO pf_local VALUES(73)", 1))
	rows, err := b.DB.QueryContext(p.Ctx, "SELECT id FROM pf_table")
	if rows != nil {
		rows.Close()
		t.Fatal("unbound stock provider published Rows")
	}
	var unavailable *stuntgres.ExternalSQLUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatal("unbound demanded execution did not expose honest host capability boundary", err)
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		t.Fatal("unbound host capability fabricated SQLSTATE", state.SQLState())
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	var marker string
	for _, line := range strings.Split(dump.String(), "\n") {
		if strings.HasPrefix(line, "-- stuntgres-external-sql-v1 ") {
			marker = line + "\n"
			break
		}
	}
	if marker == "" {
		t.Fatal("source-unbound stock origin omitted its explicit prerequisite")
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{strings.Replace(dump.String(), marker, "", 1), marker + dump.String()} {
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(invalid)); err == nil {
			t.Fatal("unbound missing/duplicate prerequisite accepted")
		}
		run(fixture.Select("SELECT v FROM pf_local", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}))
	}
}
