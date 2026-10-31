package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstathashindexTransactions(t *testing.T) {
	t.Run("memberships", func(t *testing.T) {
		pgstathashindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("INSERT INTO ph_part.ph_heap VALUES(1),(2),(3)", 3))
			run(fixture.Exec("CREATE INDEX ph_sibling ON ph_part.ph_heap USING hash(k)", 0))
			q := "SELECT * FROM ph_part.pgstathashindex('ph_part.ph_sibling')"
			pgstathashindexStats(t, p, b, c, q, 3, 0)
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO ph_part.ph_heap VALUES(4),(0)", 2))
			run(fixture.Exec("SAVEPOINT ph_save", 0))
			// Private DML may not be published in the sampled effective root.
			// Both engines retain callability and the released output shape.
			checkPrivate := func() {
				pgstathashindexStats(t, p, b, c, q, -1, -1)
			}
			checkPrivate()
			run(fixture.Exec("UPDATE ph_part.ph_heap SET k=5 WHERE k=2", 1))
			checkPrivate()
			run(fixture.Exec("DELETE FROM ph_part.ph_heap WHERE k=3", 1))
			checkPrivate()
			pgstathashindexBoth(t, p, b, c, fixture.Select("SELECT k FROM ph_part.ph_heap ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(4)}, {int64(5)}}))
			run(fixture.Exec("ROLLBACK TO SAVEPOINT ph_save", 0))
			checkPrivate()
			pgstathashindexBoth(t, p, b, c, fixture.Select("SELECT k FROM ph_part.ph_heap ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}))
			run(fixture.Exec("ROLLBACK", 0))
			pgstathashindexBoth(t, p, b, c, fixture.Select("SELECT k FROM ph_part.ph_heap ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
			run(fixture.Exec("REINDEX INDEX ph_part.ph_sibling", 0))
			pgstathashindexStats(t, p, b, c, q, 3, 0)
		})
	})
	t.Run("installation", func(t *testing.T) {
		pgstathashindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("DROP EXTENSION pgstattuple", 0))
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			count := func(target *sql.Conn, n int64) {
				fixture.RunStep(t, p.Ctx, target, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pgstattuple'", []string{"n"}, []string{"INT8"}, [][]any{{n}}))
			}
			for _, ending := range []string{"ROLLBACK", "COMMIT"} {
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SAVEPOINT ph_install", 0))
				run(fixture.Exec("CREATE EXTENSION pgstattuple SCHEMA ph_part", 0))
				pgstathashindexStats(t, p, b, c, "SELECT * FROM ph_part.pgstathashindex('ph_part.ph_index')", 0, 0)
				count(c, 1)
				count(peer, 0)
				run(fixture.Exec("ROLLBACK TO SAVEPOINT ph_install", 0))
				count(c, 0)
				run(fixture.Exec("CREATE EXTENSION pgstattuple SCHEMA ph_part", 0))
				run(fixture.Exec(ending, 0))
				want := int64(0)
				if ending == "COMMIT" {
					want = 1
				}
				count(peer, want)
			}
			pgstathashindexStats(t, p, b, peer, "SELECT * FROM ph_part.pgstathashindex('ph_part.ph_index')", 0, 0)
		})
	})
}
