package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// A held utility is prepared once, before target creation. Every invocation
// below goes through that same physical statement, never a replacement Prepare.
type databaseDropRoute struct {
	mode string
	held fixture.Executor
}

func databaseDropPrepare(c *databaseCreateCase, mode, query string) databaseDropRoute {
	c.t.Helper()
	r := databaseDropRoute{mode: mode, held: c.conn}
	if mode == "held" {
		stmt, err := c.conn.PrepareContext(c.p.Ctx, query)
		if err != nil {
			c.t.Fatal("effect-free DROP preparation", err)
		}
		c.t.Cleanup(func() {
			if err := stmt.Close(); err != nil {
				c.t.Error(err)
			}
		})
		r.held = fixture.HeldStatement{Executor: c.conn, Statement: stmt, SQL: query}
	}
	return r
}

func (r databaseDropRoute) run(c *databaseCreateCase, s fixture.Step) {
	c.t.Helper()
	if r.mode == "fresh" {
		s = fixture.Prepared(s)
	}
	if s.State != "" {
		s.ErrorStage = "execute"
	}
	fixture.RunStep(c.t, c.p.Ctx, r.held, c.backend.Recorder, s)
}

func databaseDropNotice(t *testing.T, records *[]fixture.Diagnostic, name string, count int) {
	t.Helper()
	want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "database \"" + name + "\" does not exist, skipping"}
	if len(*records) != count {
		t.Fatalf("DROP notices = %+v, want %d copies of %+v", *records, count, want)
	}
	for _, got := range *records {
		if got != want {
			t.Fatalf("DROP notice = %+v, want %+v", got, want)
		}
	}
	*records = nil
}

func databaseDropClose(t *testing.T, pools ...*sql.DB) {
	t.Helper()
	for _, db := range pools {
		if err := db.Close(); err != nil {
			t.Fatal("physical database pool close", err)
		}
	}
}
