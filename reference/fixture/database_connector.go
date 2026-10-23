package fixture

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"net/url"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/ngrok/sqlmw"
	"stuntgres"
)

// DatabaseReferenceCleanup must be registered before the scenario and after role
// cleanup. Names may be appended later, before attempting each CREATE. Pool and
// schema cleanup therefore precedes DROP DATABASE, which precedes DROP ROLE.
func DatabaseReferenceCleanup(t *testing.T, names *[]string) {
	t.Helper()
	t.Cleanup(func() {
		if !Started {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for i := len(*names) - 1; i >= 0; i-- {
			if _, err := Admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+pq.QuoteIdentifier((*names)[i])); err != nil {
				t.Errorf("database cleanup %q: %v", (*names)[i], err)
			}
		}
	})
}

// DatabaseScenarioPool selects an exact decoded database name. SQL uses the same
// middleware as the parent; raw is reserved for native controls. It does not Ping:
// callers can assert failed physical admission without a fixture fatal error.
func DatabaseScenarioPool(t *testing.T, p *Pair, pg bool, database, role string) (wrapped, raw *sql.DB, recorder *Recorder) {
	t.Helper()
	var connector driver.Connector
	if pg {
		u, err := url.Parse(DSN)
		if err != nil {
			t.Fatal(err)
		}
		u.Path, u.RawPath = "/"+database, ""
		if role != "" {
			u.User = url.UserPassword(role, "role-core-fixture")
		}
		connector, err = pq.NewConnector(u.String())
		if err != nil {
			t.Fatal(err)
		}
	} else {
		base := p.NativeConnector
		if role != "" {
			// The fixture's original opaque key is known; never derive it from
			// current_database(), whose NAME may be clipped.
			c, err := base.Driver().(*stuntgres.Driver).OpenConnectorAs(p.NativeDSN, role)
			if err != nil {
				t.Fatal(err)
			}
			base = c.(*stuntgres.Connector)
		}
		connector = base.WithDatabase(database)
	}
	return databaseScenarioPools(t, connector, pg)
}

func databaseScenarioPools(t *testing.T, connector driver.Connector, pg bool) (*sql.DB, *sql.DB, *Recorder) {
	t.Helper()
	raw := sql.OpenDB(connector)
	wrappedDriver := sqlmw.Driver(NoticeReferenceDriver{connector}, Interceptor{})
	c, err := wrappedDriver.(driver.DriverContext).OpenConnector("")
	if err != nil {
		t.Fatal(err)
	}
	wrapped := sql.OpenDB(c)
	recorder := &Recorder{RawBackend: RawNative}
	if pg {
		recorder.RawBackend = RawPostgres
	}
	t.Cleanup(func() {
		if err := wrapped.Close(); err != nil {
			t.Error(err)
		}
		if err := raw.Close(); err != nil {
			t.Error(err)
		}
	})
	return wrapped, raw, recorder
}
