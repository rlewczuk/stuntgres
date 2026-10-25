package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAvailableExtensionsTransactions(t *testing.T) {
	availableExtensionsCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		var db *sql.DB
		for _, b := range p.Backends() {
			if b.PG == pg {
				db = b.DB
			}
		}
		peer, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		a, err := c.PrepareContext(p.Ctx, "SELECT * FROM pg_available_extensions WHERE name=$1")
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		v, err := c.PrepareContext(p.Ctx, "SELECT * FROM pg_available_extension_versions WHERE name=$1")
		if err != nil {
			t.Fatal(err)
		}
		defer v.Close()
		for _, commit := range []bool{false, true} {
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SAVEPOINT before_install", 0))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION unaccent", 0))
			availableExtensionsState(t, p, c, r, pg, true)
			availableExtensionsHeld(t, p, a, false, pg, availableExtensionsRow("1.1"), "unaccent")
			availableExtensionsHeld(t, p, v, true, pg, availableVersionsRow(true), "unaccent")
			availableExtensionsState(t, p, peer, r, pg, false)
			if !commit {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ROLLBACK TO before_install", 0))
				availableExtensionsState(t, p, c, r, pg, false)
				availableExtensionsHeld(t, p, a, false, pg, availableExtensionsRow(nil), "unaccent")
				availableExtensionsHeld(t, p, v, true, pg, availableVersionsRow(false), "unaccent")
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ROLLBACK", 0))
				availableExtensionsState(t, p, peer, r, pg, false)
			} else {
				var retained *sql.Rows
				if !pg {
					retained, err = peer.QueryContext(p.Ctx, "SELECT * FROM pg_available_extensions WHERE name='unaccent'")
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
					availableExtensionsMetadata(t, retained, availableExtensionsColumns, availableExtensionsTypes, false)
				}
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("COMMIT", 0))
				availableExtensionsState(t, p, peer, r, pg, true)
				availableExtensionsHeld(t, p, a, false, pg, availableExtensionsRow("1.1"), "unaccent")
				availableExtensionsHeld(t, p, v, true, pg, availableVersionsRow(true), "unaccent")
				if retained != nil {
					availableExtensionsRetained(t, retained, false, availableExtensionsRow(nil))
					retained.Close()
				}
			}
		}
	})
}
