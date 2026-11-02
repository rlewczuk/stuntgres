package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkBuildSQLUpdateFeatureSmoke(t *testing.T) {
	updateCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE "Update Smoke"("K""id" text,k2 text,payload text,note text)`, 0))
		run(fixture.Exec(`INSERT INTO "Update Smoke" VALUES('O''Brien',NULL,E'path\\tail',NULL),('remote''s',NULL,'old','replace'),('remote''s','other','partial-key','keep'),('other',NULL,'decoy','keep')`, 4))
		held := updateHeld(t, p, c, updateCall)
		const want = `UPDATE "Update Smoke" SET "K""id" = 'remote''s', k2 = NULL, payload = E'path\\tail', note = NULL WHERE "K""id" = 'remote''s' AND k2 IS NULL`
		s := updateSelect(updateCall, want, `"Update Smoke"`, "1 2", int64(2), `{"O'Brien",NULL}`, `{"remote's",NULL}`)
		updateBoth(t, p, b, c, s)
		generated := updateRead(t, p, b, held, s)
		const contents = `SELECT "K""id",k2,payload,note FROM "Update Smoke" ORDER BY "K""id",k2 NULLS FIRST`
		columns := []string{"K\"id", "k2", "payload", "note"}
		types := []string{"TEXT", "TEXT", "TEXT", "TEXT"}
		// Construction does not execute the returned statement or mutate any row.
		run(fixture.Select(contents, columns, types, [][]any{
			{"O'Brien", nil, `path\tail`, nil},
			{"other", nil, "decoy", "keep"},
			{"remote's", nil, "old", "replace"},
			{"remote's", "other", "partial-key", "keep"},
		}))
		// This must be the scanned value, not a handwritten equivalent UPDATE.
		run(fixture.Exec(generated, 1))
		run(fixture.Select(contents, columns, types, [][]any{
			{"O'Brien", nil, `path\tail`, nil},
			{"other", nil, "decoy", "keep"},
			{"remote's", nil, `path\tail`, nil},
			{"remote's", "other", "partial-key", "keep"},
		}))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, updateError(updateCall, "2202E", "target key array length must match number of key attributes", `"Update Smoke"`, "1 2", int64(2), `{"O'Brien",NULL}`, "{a}"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, updateError(updateCall, "21000", "source row not found", `"Update Smoke"`, "1", int64(1), "{absent}", "{remote}"))
		run(fixture.Exec("CREATE TABLE update_smoke_logical(gone text,id integer,payload text)", 0))
		run(fixture.Exec("ALTER TABLE update_smoke_logical DROP COLUMN gone", 0))
		run(fixture.Exec("INSERT INTO update_smoke_logical VALUES(1,'live')", 1))
		logical := updateSelect(updateCall, "UPDATE update_smoke_logical SET id = '9', payload = 'live' WHERE id = '9'", "update_smoke_logical", "1", int64(1), "{1}", "{9}")
		updateBoth(t, p, b, c, logical)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, logical)
	})
}
