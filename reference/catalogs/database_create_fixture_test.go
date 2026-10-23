package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

type databaseCreateCase struct {
	t       *testing.T
	p       *fixture.Pair
	backend fixture.Backend
	conn    *sql.Conn
	names   *[]string
}

var databaseCreateRoles = []string{"dc_actor", "dc_owner", "dc_denied", "dc_inherit", "dc_reader"}

func databaseCreateScenario(t *testing.T, body func(*databaseCreateCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, databaseCreateRoles)
	names := []string{"dc_inherited", "dc_database_acl", "dc_block", "dc_batch", "dc_feature_denied", "dc_feature_block", "dc_no_such_database"}
	fixture.DatabaseReferenceCleanup(t, &names)
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			body(&databaseCreateCase{t, p, backend, conn, &names})
		})
	}
}

func (c *databaseCreateCase) run(s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, c.conn, c.backend.Recorder, s)
}

func (c *databaseCreateCase) remember(name string) {
	for _, old := range *c.names {
		if old == name {
			return
		}
	}
	*c.names = append(*c.names, name)
}

func (c *databaseCreateCase) create(name, options string, prepared bool) {
	c.t.Helper()
	c.remember(name)
	s := fixture.Exec("CREATE DATABASE "+pq.QuoteIdentifier(name)+" "+options, 0)
	if prepared {
		s = fixture.Prepared(s)
	}
	c.run(s)
}

func (c *databaseCreateCase) absent(name string) {
	c.t.Helper()
	c.run(fixture.Select("SELECT datname FROM pg_catalog.pg_database WHERE datname=$1", []string{"datname"}, []string{"NAME"}, nil, name))
}

func (c *databaseCreateCase) pool(name, role string) (*sql.DB, *sql.DB, *fixture.Recorder) {
	c.t.Helper()
	return fixture.DatabaseScenarioPool(c.t, c.p, c.backend.PG, name, role)
}

func databaseCreateDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic(code, message)
}

func databaseCreatePosition(query, token string, last bool) string {
	index := strings.Index(query, token)
	if last {
		index = strings.LastIndex(query, token)
	}
	return fixture.Position(len([]rune(query[:index])) + 1)
}

func (c *databaseCreateCase) error(query, code, message string) {
	c.t.Helper()
	c.run(fixture.ErrorDiagnostic(query, databaseCreateDiagnostic(code, message)))
}

func (c *databaseCreateCase) scalar(query string, args ...any) string {
	c.t.Helper()
	var value string
	if err := c.conn.QueryRowContext(c.p.Ctx, query, args...).Scan(&value); err != nil {
		c.t.Fatal(err)
	}
	return value
}
