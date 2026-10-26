package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTSTemplateLifecycle(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		var db *sql.DB
		for _, b := range p.Backends() {
			if b.PG == pg { db = b.DB }
		}
		peer, err := db.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer peer.Close()
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		a, err := c.PrepareContext(p.Ctx, tsTemplateQuery)
		if err != nil { t.Fatal(err) }
		defer a.Close()
		b, err := peer.PrepareContext(p.Ctx, tsTemplateQuery)
		if err != nil { t.Fatal(err) }
		defer b.Close()
		run := func(q string) { fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0)) }
		state := func(ownerPresent, peerPresent bool) (tsTemplateIdentity, tsTemplateIdentity) {
			t.Helper()
			owner := tsTemplateRead(t, p, c, pg, ownerPresent)
			other := tsTemplateRead(t, p, peer, pg, peerPresent)
			if tsTemplateHeld(t, p, a, pg, ownerPresent) != owner || tsTemplateHeld(t, p, b, pg, peerPresent) != other { t.Fatal("held template differs from current catalog") }
			if ownerPresent && tsTemplateLinked(t, p, c, r, pg) != owner { t.Fatal("owner identity changed during read") }
			if peerPresent && tsTemplateLinked(t, p, peer, r, pg) != other { t.Fatal("peer identity changed during read") }
			if ownerPresent && peerPresent && tsTemplateNumeric(t, p, c) != tsTemplateNumeric(t, p, peer) { t.Fatal("sessions disagree on helper identities") }
			return owner, other
		}
		run("CREATE SCHEMA ext_addon")
		state(false, false)
		run("BEGIN")
		run("SAVEPOINT before_install")
		run("CREATE EXTENSION unaccent SCHEMA ext_addon")
		state(true, false)
		run("ROLLBACK TO before_install")
		state(false, false)
		run("CREATE EXTENSION unaccent SCHEMA ext_addon")
		state(true, false)
		run("ROLLBACK")
		state(false, false)
		run("BEGIN")
		run("CREATE EXTENSION unaccent SCHEMA ext_addon")
		state(true, false)
		run("COMMIT")
		original, other := state(true, true)
		originalHandlers := tsTemplateNumeric(t, p, c)
		if original != other { t.Fatal("commit did not publish exact template") }
		capability := fixture.Select("SELECT ext_addon.unaccent('Hôtel')", []string{"unaccent"}, []string{"TEXT"}, [][]any{{"Hotel"}})
		fixture.RunStep(t, p.Ctx, c, r, capability)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(capability))
		run("BEGIN")
		run("DROP EXTENSION unaccent")
		_, other = state(false, true)
		if other != original { t.Fatal("private drop changed peer identity") }
		if tsTemplateNumeric(t, p, peer) != originalHandlers { t.Fatal("private drop changed peer helpers") }
		run("ROLLBACK")
		restored, other := state(true, true)
		if restored != original || other != original { t.Fatal("drop rollback did not restore exact identity/options") }
		if tsTemplateNumeric(t, p, c) != originalHandlers { t.Fatal("drop rollback changed helper identities") }
		// Native rows are eager immutable captures. pq streams, so this witness
		// deliberately retains only a native result while another connection DDLs.
		var saved *sql.Rows
		if !pg {
			saved, err = peer.QueryContext(p.Ctx, tsTemplateQuery, "unaccent")
			if err != nil { t.Fatal(err) }
			defer saved.Close()
			tsTemplateMetadata(t, saved, false)
		}
		run("BEGIN")
		run("DROP EXTENSION unaccent")
		run("COMMIT")
		state(false, false)
		for _, handler := range []int64{originalHandlers.init, originalHandlers.lexize} {
			var count int64
			if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_catalog.pg_proc WHERE oid=$1", handler).Scan(&count); err != nil || count != 0 { t.Fatal("dropped template helper remains", count, err) }
		}
		run("CREATE EXTENSION unaccent SCHEMA ext_addon")
		fresh := tsTemplateLinked(t, p, c, r, pg)
		freshHandlers := tsTemplateNumeric(t, p, c)
		if fresh.oid == original.oid || freshHandlers.init == originalHandlers.init || freshHandlers.lexize == originalHandlers.lexize { t.Fatal("reinstall reused deleted template/helper identity") }
		if saved != nil && tsTemplateConsume(t, saved, false, true) != original { t.Fatal("retained template changed after drop/reinstall") }
		current, other := state(true, true)
		if current != fresh || other != fresh { t.Fatal("reinstall was not atomically published") }
	})
}
