package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func databaseSelectionFatal(code, message string) fixture.Diagnostic {
	d := databaseCreateDiagnostic(code, message)
	d.Severity, d.SeverityUnlocalized = "FATAL", "FATAL"
	return d
}

func databaseSelectionProbe(c *databaseCreateCase, name, role string, want *fixture.Diagnostic) {
	c.t.Helper()
	db, _, _ := c.pool(name, role)
	defer db.Close()
	err := db.PingContext(c.p.Ctx)
	if want != nil {
		fixture.AssertDiagnostic(c.t, err, want)
	} else if err != nil {
		c.t.Fatal(err)
	}
}

func TestReferenceDatabaseSelectionAdmission(t *testing.T) {
	databaseCreateScenario(t, func(c *databaseCreateCase) {
		t := c.t
		c.run(fixture.Exec("CREATE ROLE dc_reader LOGIN PASSWORD 'role-core-fixture'", 0))
		c.run(fixture.Exec("CREATE ROLE dc_owner", 0))
		c.run(fixture.Exec("GRANT dc_owner TO dc_reader", 0))
		for _, target := range []struct{ name, options string }{
			{"ds_disabled", "ALLOW_CONNECTIONS false CONNECTION LIMIT 0"},
			{"ds_zero", "CONNECTION LIMIT 0"},
			{"ds_one", "CONNECTION LIMIT 1"},
			{"ds_peer", "CONNECTION LIMIT 1"},
			{"ds_unlimited", "CONNECTION LIMIT -1"},
		} {
			c.create(target.name, target.options, false)
		}
		c.run(fixture.Exec("REVOKE CONNECT ON DATABASE ds_disabled FROM PUBLIC", 0))
		c.run(fixture.Exec("REVOKE CONNECT ON DATABASE ds_zero FROM PUBLIC", 0))
		missing := databaseSelectionFatal("3D000", "database \"ds_missing\" does not exist")
		databaseSelectionProbe(c, "ds_missing", "dc_reader", &missing)
		c.absent("ds_missing")
		disabled := databaseSelectionFatal("55000", "database \"ds_disabled\" is not currently accepting connections")
		databaseSelectionProbe(c, "ds_disabled", "dc_reader", &disabled)
		databaseSelectionProbe(c, "ds_disabled", "", &disabled)
		denied := databaseSelectionFatal("42501", "permission denied for database \"ds_zero\"")
		denied.Detail = "User does not have CONNECT privilege."
		databaseSelectionProbe(c, "ds_zero", "dc_reader", &denied)
		c.run(fixture.Exec("GRANT CONNECT ON DATABASE ds_zero TO dc_reader", 0))
		zero := databaseSelectionFatal("53300", "too many connections for database \"ds_zero\"")
		databaseSelectionProbe(c, "ds_zero", "dc_reader", &zero)
		databaseSelectionProbe(c, "ds_zero", "", nil)

		// Only the wrapped pool opens a physical owner: the fixture's raw pool
		// must remain unused while testing a one-slot database.
		one, _, rec := c.pool("ds_one", "dc_reader")
		one.SetMaxOpenConns(1)
		one.SetMaxIdleConns(1)
		owner, err := one.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close()
		full := databaseSelectionFatal("53300", "too many connections for database \"ds_one\"")
		databaseSelectionProbe(c, "ds_one", "dc_reader", &full)
		databaseSelectionProbe(c, "ds_peer", "dc_reader", nil)
		fixture.RunStep(t, c.p.Ctx, owner, rec, fixture.Exec("SET ROLE dc_owner", 0))
		databaseSelectionProbe(c, "ds_one", "dc_reader", &full)
		fixture.RunStep(t, c.p.Ctx, owner, rec, fixture.Exec("RESET ROLE", 0))
		databaseSelectionTwoSessions(c, "ds_unlimited")
		databaseSelectionSlotLifecycle(c)
	})
}

// Returning a borrower retains its reservation; closing its pool releases it.
func databaseSelectionSlotLifecycle(c *databaseCreateCase) {
	c.t.Helper()
	c.create("ds_slot", "CONNECTION LIMIT 1", false)
	full := databaseSelectionFatal("53300", "too many connections for database \"ds_slot\"")
	pool, _, _ := c.pool("ds_slot", "dc_reader")
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	conn, err := pool.Conn(c.p.Ctx)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		c.t.Fatal(err)
	}
	databaseSelectionProbe(c, "ds_slot", "dc_reader", &full)
	if err := pool.Close(); err != nil {
		c.t.Fatal(err)
	}
	databaseSelectionProbe(c, "ds_slot", "dc_reader", nil)
	super, _, _ := c.pool("ds_slot", "")
	if err := super.PingContext(c.p.Ctx); err != nil {
		c.t.Fatal(err)
	}
	databaseSelectionProbe(c, "ds_slot", "dc_reader", &full)
	databaseSelectionProbe(c, "ds_slot", "", nil)
	if err := super.Close(); err != nil {
		c.t.Fatal(err)
	}
	databaseSelectionProbe(c, "ds_slot", "dc_reader", nil)
}

func databaseSelectionTwoSessions(c *databaseCreateCase, name string) {
	c.t.Helper()
	pool, _, _ := c.pool(name, "dc_reader")
	defer pool.Close()
	var owners []*sql.Conn
	defer func() {
		for _, owner := range owners {
			owner.Close()
		}
	}()
	for range 2 {
		owner, err := pool.Conn(c.p.Ctx)
		if err != nil {
			c.t.Fatal(err)
		}
		owners = append(owners, owner)
	}
}
