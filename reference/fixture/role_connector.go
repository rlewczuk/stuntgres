package fixture

import (
	"database/sql"
	"net/url"
	"stuntgres"
	"testing"
)

func RoleScenarioLoginPool(t *testing.T, d *stuntgres.Driver, pg bool, dsn, name string) *sql.DB {
	t.Helper()
	var db *sql.DB
	if pg {
		u, err := url.Parse(DSN)
		if err != nil {
			t.Fatal(err)
		}
		u.User = url.UserPassword(name, "role-core-fixture")
		var errOpen error
		db, errOpen = sql.Open("postgres", u.String())
		if errOpen != nil {
			t.Fatal(errOpen)
		}
	} else {
		c, err := d.OpenConnectorAs(dsn, name)
		if err != nil {
			t.Fatal(err)
		}
		db = sql.OpenDB(c)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}
