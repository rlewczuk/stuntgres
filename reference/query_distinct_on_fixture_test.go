package reference_test

import (
	"database/sql"
	"testing"
)

func queryDistinctOnScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, backend, c)
		})
	}
}

func queryDistinctOnEvents(run func(referenceStep)) {
	run(referenceExec("CREATE TABLE events(id INT NOT NULL,k INT,stamp TIMESTAMP,payload TEXT)", 0))
	run(referenceExec("INSERT INTO events VALUES(1,1,'2025-01-01','old1'),(2,1,'2025-01-02','new1a'),(3,1,'2025-01-02','new1b'),(4,2,'2025-01-01','old2'),(5,2,'2025-01-03','new2'),(6,NULL,'2025-01-01','oldnull'),(7,NULL,'2025-01-04','newnull')", 7))
}

func queryDistinctOnHidden() referenceStep {
	return referenceSelect("SELECT DISTINCT ON(k) id,payload FROM events ORDER BY k ASC NULLS LAST,stamp DESC,id DESC", []string{"id", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "new1b"}, {int64(5), "new2"}, {int64(7), "newnull"}})
}

func queryDistinctOnHeld(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, s referenceStep) {
	t.Helper()
	stmt, err := c.PrepareContext(p.ctx, s.query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for range 2 {
		referenceRunStep(t, p.ctx, referenceHeldStatement{c, stmt, s.query}, b.recorder, s)
	}
}
