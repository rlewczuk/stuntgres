package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func queryDistinctOnScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, backend, c)
		})
	}
}

func queryDistinctOnEvents(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE events(id INT NOT NULL,k INT,stamp TIMESTAMP,payload TEXT)", 0))
	run(fixture.Exec("INSERT INTO events VALUES(1,1,'2025-01-01','old1'),(2,1,'2025-01-02','new1a'),(3,1,'2025-01-02','new1b'),(4,2,'2025-01-01','old2'),(5,2,'2025-01-03','new2'),(6,NULL,'2025-01-01','oldnull'),(7,NULL,'2025-01-04','newnull')", 7))
}

func queryDistinctOnHidden() fixture.Step {
	return fixture.Select("SELECT DISTINCT ON(k) id,payload FROM events ORDER BY k ASC NULLS LAST,stamp DESC,id DESC", []string{"id", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "new1b"}, {int64(5), "new2"}, {int64(7), "newnull"}})
}

func queryDistinctOnHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for range 2 {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, stmt, s.Query}, b.Recorder, s)
	}
}
