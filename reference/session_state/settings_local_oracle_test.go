//go:build reference

package reference_test

import (
	"database/sql"
	"net/url"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSettingsLocalPrivilegeOracle(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"local_settings_reader"})
	p := fixture.NewPair(t)
	if _, err := fixture.Admin.ExecContext(p.Ctx, "CREATE ROLE local_settings_reader LOGIN PASSWORD 'local_settings_reader'"); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(fixture.DSN)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("local_settings_reader", "local_settings_reader")
	db, err := sql.Open("stuntgres_reference_postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, prepared := range []bool{false, true} {
		s := settingsReferenceError("SET LOCAL log_statement_stats='bogus'", "42501", `permission denied to set parameter "log_statement_stats"`, "", "")
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, db, p.PostgresRecord, s)
	}
}
