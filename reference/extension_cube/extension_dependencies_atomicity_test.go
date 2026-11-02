package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExtensionDependenciesAtomicity(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "new_cube"
		if existing {
			name = "installed_cube"
		}
		t.Run("collision_"+name, func(t *testing.T) {
			earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE deps_sentinel(id integer NOT NULL)", 0))
				run(fixture.Exec("INSERT INTO deps_sentinel VALUES (17)", 1))
				run(fixture.Exec("SET extra_float_digits=0", 0))
				run(fixture.Exec("CREATE DOMAIN earth AS integer", 0))
				settings := fixture.OwnershipSnapshot(t, p, c, "SHOW search_path")
				domain := fixture.OwnershipSnapshot(t, p, c, "SELECT oid,typbasetype,typowner,typarray FROM pg_type WHERE oid='earth'::regtype")
				var before [][][]any
				if existing {
					cubeInstall(t, p, b, c)
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION cube_dim(cube) FROM PUBLIC", 0))
					run(fixture.Exec("CREATE TABLE deps_existing_cube(v cube)", 0))
					run(fixture.Exec("INSERT INTO deps_existing_cube VALUES ('(1,2)')", 1))
					before = dependencyCaptureCube(t, p, c)
				}
				for _, prepared := range []bool{false, true} {
					dependencyError(t, p, b, c, "CREATE EXTENSION earthdistance CASCADE", prepared, dependencyCollision("public"))
					if existing {
						dependencyUnchangedCube(t, p, c, before)
						run(fixture.CubeSelect(b, "SELECT v FROM deps_existing_cube", []string{"v"}, [][]any{{"(1, 2)"}}))
						dependencyNoEarthExtension(t, p, b, c)
					} else {
						dependencyAbsent(t, p, b, c)
					}
					fixture.OwnershipUnchanged(t, p, c, "SHOW search_path", settings)
					fixture.OwnershipUnchanged(t, p, c, "SELECT oid,typbasetype,typowner,typarray FROM pg_type WHERE oid='earth'::regtype", domain)
					run(fixture.Select("SELECT id FROM deps_sentinel ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}))
					run(fixture.Select("SELECT current_setting('extra_float_digits') AS digits", []string{"digits"}, []string{"TEXT"}, [][]any{{"0"}}))
					run(fixture.Select("SELECT conname FROM pg_constraint WHERE contypid='earth'::regtype", []string{"conname"}, []string{"NAME"}, [][]any{}))
				}
			})
		})
	}
	t.Run("late_privilege_failure", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"deps_atomic_installer"})
		earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE ROLE deps_atomic_installer", 0))
			run(fixture.Exec("CREATE TABLE deps_privilege_sentinel(v integer)", 0))
			run(fixture.Exec("INSERT INTO deps_privilege_sentinel VALUES (23)", 1))
			var database string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
				t.Fatal(err)
			}
			database = fixture.ExtensionTempIdentifier(database)
			run(fixture.Exec("GRANT CREATE ON DATABASE "+database+" TO deps_atomic_installer", 0))
			run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO deps_atomic_installer", 0))
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
				defer cancel()
				for _, q := range []string{"RESET ROLE", "REVOKE CREATE ON DATABASE " + database + " FROM deps_atomic_installer"} {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			path := fixture.OwnershipSnapshot(t, p, c, "SHOW search_path")
			for _, prepared := range []bool{false, true} {
				run(fixture.Exec("SET ROLE deps_atomic_installer", 0))
				dependencyError(t, p, b, c, "CREATE EXTENSION earthdistance CASCADE", prepared, dependencyDenied())
				run(fixture.Exec("RESET ROLE", 0))
				dependencyAbsent(t, p, b, c)
				fixture.OwnershipUnchanged(t, p, c, "SHOW search_path", path)
				run(fixture.Select("SELECT v FROM deps_privilege_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(23)}}))
			}
		})
	})
	for _, privateCube := range []bool{false, true} {
		name := "fresh_closure"
		if privateCube {
			name = "private_schema_and_cube"
		}
		t.Run("transaction_"+name, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "deps_private")
			earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE deps_tx_sentinel(v integer)", 0))
				run(fixture.Exec("INSERT INTO deps_tx_sentinel VALUES (31)", 1))
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				trun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
				if privateCube {
					for _, q := range []string{"CREATE SCHEMA deps_private", "CREATE EXTENSION cube SCHEMA deps_private", "SET LOCAL search_path=deps_private,public"} {
						trun(fixture.Exec(q, 0))
					}
				}
				trun(fixture.Prepared(fixture.Exec("CREATE EXTENSION earthdistance CASCADE", 0)))
				trun(fixture.Exec("CREATE TABLE deps_tx_values(v earth)", 0))
				trun(fixture.Exec("INSERT INTO deps_tx_values VALUES ('(6378168,0)'),(NULL)", 2))
				trun(fixture.EarthSelect(b, "SELECT v FROM deps_tx_values WHERE v IS NOT NULL", []string{"v"}, [][]any{{"(6378168, 0)"}}))
				dependencyVersions(t, p, b, tx, b.Recorder)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname IN ('cube','earthdistance') ORDER BY extname", []string{"extname"}, []string{"NAME"}, [][]any{}))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				dependencyAbsent(t, p, b, c)
				run(fixture.Select("SELECT nspname FROM pg_namespace WHERE nspname='deps_private'", []string{"nspname"}, []string{"NAME"}, [][]any{}))
				run(fixture.Select("SELECT v FROM deps_tx_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(31)}}))
				tx, err = c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				trun(fixture.Exec("CREATE EXTENSION earthdistance CASCADE", 0))
				trun(fixture.Exec("CREATE TABLE deps_committed(v earth)", 0))
				trun(fixture.Exec("INSERT INTO deps_committed VALUES ('6378168')", 1))
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				dependencyGraph(t, p, b, c, b.Recorder)
				dependencyVersions(t, p, b, b.DB, b.Recorder)
				run(fixture.EarthSelect(b, "SELECT v FROM deps_committed", []string{"v"}, [][]any{{"(6378168)"}}))
			})
		})
	}
	t.Run("precanceled_public_routes", func(t *testing.T) {
		earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			const query = "CREATE EXTENSION earthdistance CASCADE"
			stmt, err := b.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			// DB-owned routes reject the canceled context before obtaining a
			// driver connection. Candidate cancellation/discard is a separate
			// admitted-stage unit witness, not a race with a fast SQL script.
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, prepared := range []bool{false, true} {
				name := "direct"
				if prepared {
					name = "prepared"
				}
				t.Run(name, func(t *testing.T) {
					var err error
					if prepared {
						_, err = stmt.ExecContext(ctx)
					} else {
						_, err = b.DB.ExecContext(ctx, query)
					}
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("precanceled %s error %v", name, err)
					}
				})
			}
			healthy, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer healthy.Close()
			dependencyAbsent(t, p, b, healthy)
		})
	})
}
