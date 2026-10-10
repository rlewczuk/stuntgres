package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"net/url"
	"testing"

	"github.com/lib/pq"
	"github.com/ngrok/sqlmw"
	"stuntgres"
)

// Shared direct/prepared execution uses only existing public APIs.
func roleAttributesRun(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor, prepared bool, s referenceStep) {
	t.Helper()
	s.prepared = prepared || s.prepared
	referenceRunStep(t, p.ctx, c, b.recorder, s)
}

func roleAttributesPhysical(t *testing.T, p *referencePair, b referenceBackend, name string) (driver.Conn, error) {
	t.Helper()
	var connector driver.Connector
	var err error
	if b.pg {
		u, e := url.Parse(referenceDSN)
		if e != nil {
			t.Fatal(e)
		}
		u.User = url.UserPassword(name, "role-attributes-fixture")
		connector, err = pq.NewConnector(u.String())
	} else {
		connector, err = p.nativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs("reference/"+t.Name(), name)
	}
	if err != nil {
		t.Fatal(err)
	}
	conn, err := connector.Connect(p.ctx)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func roleAttributesPool(t *testing.T, p *referencePair, b referenceBackend, name string) *sql.DB {
	t.Helper()
	var connector driver.Connector
	var err error
	if b.pg {
		u, err := url.Parse(referenceDSN)
		if err != nil {
			t.Fatal(err)
		}
		u.User = url.UserPassword(name, "role-attributes-fixture")
		connector, err = pq.NewConnector(u.String())
		if err != nil {
			t.Fatal(err)
		}
	} else {
		connector, err = p.nativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs("reference/"+t.Name(), name)
		if err != nil {
			t.Fatal(err)
		}
	}
	wrapped := sqlmw.Driver(noticeReferenceDriver{connector}, referenceInterceptor{})
	configured, err := wrapped.(driver.DriverContext).OpenConnector("")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(configured)
	t.Cleanup(func() { db.Close() })
	return db
}

// Privileged catalog facts are oracle-only, not a native pg_authid API.
func roleAttributesFacts(t *testing.T, p *referencePair, name string, want []any) {
	t.Helper()
	rows, err := referenceAdmin.QueryContext(p.ctx, `SELECT rolsuper,rolcreatedb,rolcreaterole,rolcanlogin,rolinherit,rolreplication,rolbypassrls,rolconnlimit,rolpassword IS NULL,rolvaliduntil::text FROM pg_authid WHERE rolname=$1`, name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing oracle role", name, rows.Err())
	}
	got := make([]any, 10)
	dest := make([]any, 10)
	for i := range got {
		dest[i] = &got[i]
	}
	if err := rows.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	for i, value := range got {
		if bytes, ok := value.([]byte); ok {
			value = string(bytes)
		}
		if value != want[i] {
			t.Errorf("oracle %s field %d = %v (%T), want %v (%T)", name, i, value, value, want[i], want[i])
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra oracle facts", rows.Err())
	}
}

func roleAttributesDefaults(login bool) []any {
	return []any{false, false, false, login, true, false, false, int64(-1), true, nil}
}

func roleAttributesDenied(query, detail string, prepared bool) referenceStep {
	return roleReferenceError(query, "42501", "permission denied to alter role", detail, prepared, "execute")
}
