package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const iscnRelation = "information_schema.information_schema_catalog_name"
const iscnDatabase = "Catalog Name DB"

var iscnColumns = []string{"catalog_name"}
var iscnTypes = []string{"NAME"}

func iscnScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *sql.DB, *fixture.Recorder)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"iscn_reader"})
	var databases []string
	fixture.DatabaseReferenceCleanup(t, &databases)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			if b.PG {
				databases = append(databases, iscnDatabase)
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE DATABASE "Catalog Name DB"`, 0))
			pool, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, iscnDatabase, "")
			c, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if _, err := c.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
					t.Error(err)
				}
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, b, c, pool, recorder)
		})
	}
}

func iscnStep(projection, predicate string, present bool, args ...any) fixture.Step {
	q := "SELECT " + projection + " FROM " + iscnRelation
	if predicate != "" {
		q += " WHERE " + predicate
	}
	var rows [][]any
	if present {
		rows = [][]any{{iscnDatabase}}
	}
	return fixture.Select(q, iscnColumns, iscnTypes, rows, args...)
}

func iscnHold(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) fixture.HeldStatement {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: c, Statement: s, SQL: query}
}

func iscnRoutes(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, held fixture.HeldStatement, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, s)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
	fixture.RunStep(t, p.Ctx, held, r, s)
}
