package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceParameterACLSharedIsolation(t *testing.T) {
	parameterSharedScenario(t, "isolation", func(t *testing.T, p *fixture.Pair, a, b fixture.Backend) {
		ra, rb := parameterSharedRun(t, p, a), parameterSharedRun(t, p, b)
		first := parameterSharedConn(t, p, a, "ps_reader")
		second := parameterSharedConn(t, p, b, "ps_reader")
		fixture.RunStep(t, p.Ctx, first, a.Recorder, fixture.Exec("SET application_name='ps_value_a'", 0))
		fixture.RunStep(t, p.Ctx, second, b.Recorder, fixture.Exec("SET application_name='ps_value_b'", 0))
		ra(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO ps_reader", 0))
		rb(parameterSharedInquiry("ps_reader", "lc_messages", "SET", true))
		parameterSharedConsumers(t, p, b, second, true)
		fixture.RunStep(t, p.Ctx, first, a.Recorder, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"ps_value_a"}}))
		fixture.RunStep(t, p.Ctx, second, b.Recorder, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"ps_value_b"}}))
		if a.PG { return } // Driver and opaque DSN controls have no PostgreSQL API.
		d := p.NativeConnector.Driver().(*stuntgres.Driver)
		for _, isolated := range []struct{ driver *stuntgres.Driver; key string }{
			{stuntgres.NewDriver(), p.NativeDSN},
			{d, "shared-parameter isolated key /? dbname=not-a-parser"},
		} {
			connector, err := isolated.driver.OpenConnector(isolated.key)
			if err != nil { t.Fatal(err) }
			db, recorder := fixture.RecordedNativePool(t, connector)
			func() {
				defer db.Close()
				fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT rolname FROM pg_roles WHERE rolname='ps_reader'", []string{"rolname"}, []string{"NAME"}, nil))
				fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec("CREATE ROLE ps_reader", 0))
				fixture.RunStep(t, p.Ctx, db, recorder, parameterSharedInquiry("ps_reader", "lc_messages", "SET", false))
				fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT parname FROM pg_parameter_acl WHERE parname='lc_messages'", []string{"parname"}, []string{"TEXT"}, nil))
			}()
		}
		// Close a fresh selected pool and reopen against the same exact key;
		// pool lifetime cannot own the committed cluster authority.
		base := p.NativeConnector.WithDatabase("ps_isolation_b")
		pool := sql.OpenDB(base)
		if err := pool.PingContext(p.Ctx); err != nil { t.Fatal(err) }
		if err := pool.Close(); err != nil { t.Fatal(err) }
		reopened, recorder := fixture.RecordedNativePool(t, base)
		fixture.RunStep(t, p.Ctx, reopened, recorder, parameterSharedInquiry("ps_reader", "lc_messages", "SET", true))
	})
}
