package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDblinkGetPkeyFeatureSmoke(t *testing.T) {
	pkeyCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			`CREATE TABLE "Pkey Smoke"(gone text,"A" integer,payload text,"C" integer,PRIMARY KEY("C","A"))`,
			`ALTER TABLE "Pkey Smoke" DROP COLUMN gone`,
			"CREATE TABLE pkey_smoke_no_key(id integer)",
		} {
			run(fixture.Exec(q, 0))
		}
		held := pkeyHeld(t, p, c, pkeyCall)
		check := func(name string, s fixture.Step) {
			t.Run(name, func(t *testing.T) {
				s.CheckMetadata = pkeyMetadata(b.PG)
				pkeyRoutes(t, p, b, c, &held, s)
			})
		}
		initial := pkeyRows([][]any{{int64(1), "C"}, {int64(2), "A"}}, `"Pkey Smoke"`)
		check("empty_table", initial)
		run(fixture.Exec(`INSERT INTO "Pkey Smoke" VALUES(11,'untouched',33),(22,'decoy',44)`, 2))
		check("populated_table", initial)
		check("strict_null", pkeyRows(nil, nil))
		check("no_primary_key", pkeyRows(nil, "pkey_smoke_no_key"))
		run(fixture.Exec(`ALTER TABLE "Pkey Smoke" RENAME COLUMN "C" TO "C new"`, 0))
		current := pkeyRows([][]any{{int64(1), "C new"}, {int64(2), "A"}}, `"Pkey Smoke"`)
		check("current_metadata", current)
		t.Run("missing_and_recovery", func(t *testing.T) {
			pkeyRoutes(t, p, b, c, &held, pkeyError(pkeyCall, "42P01", `relation "missing_pkey_smoke" does not exist`, "missing_pkey_smoke"))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, current)
		})
		t.Run("genuine_result_identity", func(t *testing.T) {
			pkeyRoutes(t, p, b, c, nil, pkeyNominalLinks())
		})
		const saved = "SELECT position,colname FROM pkey_smoke_saved ORDER BY position"
		run(fixture.Exec(`CREATE VIEW pkey_smoke_saved AS SELECT * FROM pk_ext.dblink_get_pkey('"Pkey Smoke"'::text)`, 0))
		savedRows := fixture.Select(saved, []string{"position", "colname"}, []string{"INT4", "TEXT"}, current.Rows)
		t.Run("retained_definition", func(t *testing.T) {
			pkeyRoutes(t, p, b, c, nil, savedRows)
		})
		contents := fixture.Select(`SELECT "A",payload,"C new" FROM "Pkey Smoke" ORDER BY "A"`, []string{"A", "payload", "C new"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(11), "untouched", int64(33)}, {int64(22), "decoy", int64(44)}})
		run(contents)
		if !b.PG {
			// Native controls have no PostgreSQL operation. This phase is not
			// a backend skip: all SQL witnesses above run on both backends.
			t.Run("native_controls", func(t *testing.T) {
				old, err := c.QueryContext(p.Ctx, saved)
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				meta, err := old.ColumnTypes()
				if err != nil || len(meta) != 2 {
					t.Fatal("retained pkey descriptor", meta, err)
				}
				dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, pkeyNominalLinks())
				fixture.RunStep(t, p.Ctx, c, b.Recorder, savedRows)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, current)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, contents)
				for _, want := range []struct {
					position int64
					name     string
				}{{1, "C new"}, {2, "A"}} {
					if !old.Next() {
						t.Fatal("lost published pkey row", old.Err())
					}
					var position int64
					var name string
					if err := old.Scan(&position, &name); err != nil || position != want.position || name != want.name {
						t.Fatal("published pkey changed", position, name, err)
					}
				}
				if old.Next() || old.Err() != nil || meta[0].Name() != "position" || meta[0].DatabaseTypeName() != "INT4" || meta[1].Name() != "colname" || meta[1].DatabaseTypeName() != "TEXT" {
					t.Fatal("published pkey tail/metadata", old.Err(), meta)
				}
				if fixture.ExtensionDump(t, p.Ctx, p.Native) != dump {
					t.Fatal("pkey logical redump changed")
				}
			})
		}
	})
}
