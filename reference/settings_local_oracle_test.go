//go:build reference

package reference_test

import (
	"database/sql"
	"net/url"
	"testing"
)

func TestReferenceSettingsLocalPrivilegeOracle(t *testing.T) {
	roleReferenceCleanup(t, []string{"local_settings_reader"})
	p := newReferencePair(t)
	if _, err := referenceAdmin.ExecContext(p.ctx, "CREATE ROLE local_settings_reader LOGIN PASSWORD 'local_settings_reader'"); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(referenceDSN)
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
		s.prepared = prepared
		referenceRunStep(t, p.ctx, db, p.postgresRecord, s)
	}
}
