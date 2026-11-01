package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

type fullMixedIdentity struct {
	oid, rowType, namespace, owner, attributes int64
	kind                                       string
}

func fullMixedIdentityOf(f *fullVacuumCase, name string) fullMixedIdentity {
	f.t.Helper()
	var identity fullMixedIdentity
	if err := f.c.QueryRowContext(f.p.Ctx, "SELECT oid,reltype,relnamespace,relowner,relkind,relnatts FROM pg_class WHERE oid=$1::regclass", name).Scan(&identity.oid, &identity.rowType, &identity.namespace, &identity.owner, &identity.kind, &identity.attributes); err != nil {
		f.t.Fatal(err)
	}
	return identity
}

func fullMixedReader(f *fullVacuumCase) *sql.Conn {
	f.t.Helper()
	reader := vacuumConn(f.t, f.p, f.b)
	vacuumRun(f.t, f.p, f.b, reader, fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
	// The caller deliberately reads no target until every AX rewrite finishes.
	vacuumRows(f.t, f.p, f.b, reader, "full_mixed_anchor", 9)
	return reader
}

func TestReferenceVacuumFullMixedRetention(t *testing.T) {
	for _, fullFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "concurrent_then_full", true: "full_then_concurrent"}[fullFirst], func(t *testing.T) {
			fullVacuumCases(t, func(f *fullVacuumCase) {
				for _, step := range []fixture.Step{
					fixture.Exec("CREATE TABLE full_mixed_anchor(id INT)", 0),
					fixture.Exec("INSERT INTO full_mixed_anchor VALUES(9)", 1),
					fixture.Exec("CREATE TABLE full_mixed_base(id INT PRIMARY KEY)", 0),
					fixture.Exec("INSERT INTO full_mixed_base VALUES(1),(2)", 2),
					fixture.Exec("CREATE MATERIALIZED VIEW full_mixed_mv AS SELECT id FROM full_mixed_base", 2),
					fixture.Exec("CREATE UNIQUE INDEX full_mixed_mv_ix ON full_mixed_mv(id)", 0),
				} {
					f.run(step)
				}
				identity := fullMixedIdentityOf(f, "full_mixed_mv")
				query := "SELECT id FROM full_mixed_mv ORDER BY id"
				held := vacuumHeld(f.t, f.p, f.c, query)
				a := fullMixedReader(f)
				defer a.ExecContext(f.p.Ctx, "ROLLBACK")
				f.run(fixture.Exec("UPDATE full_mixed_base SET id=3 WHERE id=1", 1))
				pair := []string{"REFRESH MATERIALIZED VIEW CONCURRENTLY full_mixed_mv", "VACUUM FULL full_mixed_mv"}
				if fullFirst {
					pair[0], pair[1] = pair[1], pair[0]
				}
				replace := func() {
					for _, command := range pair {
						before, index := f.locator("full_mixed_mv"), f.locator("full_mixed_mv_ix")
						f.command(fixture.Exec(command, 0))
						if command == "VACUUM FULL full_mixed_mv" {
							f.rewritten("full_mixed_mv", before)
							f.rewritten("full_mixed_mv_ix", index)
						} else {
							// PG concurrent refresh applies an in-place diff; it does not
							// promise a physical rewrite. Native replacement locators are
							// proven separately, not conflated with PG physical filenodes.
							after, afterIndex := f.locator("full_mixed_mv"), f.locator("full_mixed_mv_ix")
							if after.oid != before.oid || after.locator <= 0 || afterIndex.oid != index.oid || afterIndex.locator <= 0 {
								f.t.Fatal("concurrent refresh lost storage identity")
							}
						}
						if fullMixedIdentityOf(f, "full_mixed_mv") != identity {
							f.t.Fatal("mixed replacement changed defining identity")
						}
					}
				}
				replace()
				b := fullMixedReader(f)
				defer b.ExecContext(f.p.Ctx, "ROLLBACK")
				f.run(fixture.Exec("UPDATE full_mixed_base SET id=4 WHERE id=2", 1))
				replace()
				vacuumRows(f.t, f.p, f.b, a, "full_mixed_mv", 1, 2)
				vacuumRows(f.t, f.p, f.b, b, "full_mixed_mv", 2, 3)
				current := fixture.Select(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(4)}})
				f.run(current)
				vacuumRun(f.t, f.p, f.b, held, current)
				vacuumRun(f.t, f.p, f.b, a, fixture.Exec("ROLLBACK", 0))
				vacuumRun(f.t, f.p, f.b, b, fixture.Exec("ROLLBACK", 0))
				before, index := f.locator("full_mixed_mv"), f.locator("full_mixed_mv_ix")
				f.command(fixture.Exec("VACUUM FULL full_mixed_mv", 0))
				f.rewritten("full_mixed_mv", before)
				f.rewritten("full_mixed_mv_ix", index)
				f.run(current)
				f.emptyProgress()
			})
		})
	}
	t.Run("repeated_truncate_full", func(t *testing.T) {
		fullVacuumCases(t, func(f *fullVacuumCase) {
			for _, step := range []fixture.Step{
				fixture.Exec("CREATE TABLE full_mixed_anchor(id INT)", 0),
				fixture.Exec("INSERT INTO full_mixed_anchor VALUES(9)", 1),
				fixture.Exec("CREATE TABLE full_mixed_heap(id INT PRIMARY KEY)", 0),
				fixture.Exec("INSERT INTO full_mixed_heap VALUES(1),(2)", 2),
			} {
				f.run(step)
			}
			identity := fullMixedIdentityOf(f, "full_mixed_heap")
			query := "SELECT id FROM full_mixed_heap ORDER BY id"
			held := vacuumHeld(f.t, f.p, f.c, query)
			rewrite := func(command string) {
				before, index := f.locator("full_mixed_heap"), f.locator("full_mixed_heap_pkey")
				f.command(fixture.Exec(command, 0))
				f.rewritten("full_mixed_heap", before)
				f.rewritten("full_mixed_heap_pkey", index)
				if fullMixedIdentityOf(f, "full_mixed_heap") != identity {
					f.t.Fatal("TRUNCATE/FULL changed defining identity")
				}
			}
			a := fullMixedReader(f)
			defer a.ExecContext(f.p.Ctx, "ROLLBACK")
			rewrite("VACUUM FULL full_mixed_heap")
			rewrite("TRUNCATE full_mixed_heap")
			f.run(fixture.Exec("INSERT INTO full_mixed_heap VALUES(3)", 1))
			b := fullMixedReader(f)
			defer b.ExecContext(f.p.Ctx, "ROLLBACK")
			rewrite("VACUUM FULL full_mixed_heap")
			rewrite("TRUNCATE full_mixed_heap")
			f.run(fixture.Exec("INSERT INTO full_mixed_heap VALUES(4)", 1))
			c := fullMixedReader(f)
			defer c.ExecContext(f.p.Ctx, "ROLLBACK")
			rewrite("VACUUM FULL full_mixed_heap")
			vacuumRows(f.t, f.p, f.b, a, "full_mixed_heap")
			vacuumRows(f.t, f.p, f.b, b, "full_mixed_heap")
			vacuumRows(f.t, f.p, f.b, c, "full_mixed_heap", 4)
			current := fixture.Select(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}})
			f.run(current)
			vacuumRun(f.t, f.p, f.b, held, current)
			for _, reader := range []*sql.Conn{a, b, c} {
				vacuumRun(f.t, f.p, f.b, reader, fixture.Exec("ROLLBACK", 0))
			}
			f.emptyProgress()
		})
	})
	for _, metadataFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "truncate_then_compatible_rename_owner", true: "compatible_rename_owner_then_truncate"}[metadataFirst], func(t *testing.T) {
			fullVacuumCases(t, func(f *fullVacuumCase) {
				for _, step := range []fixture.Step{
					fixture.Exec("CREATE ROLE full_mixed_owner", 0),
					fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO full_mixed_owner", 0),
					fixture.Exec("CREATE TABLE full_mixed_anchor(id INT)", 0),
					fixture.Exec("INSERT INTO full_mixed_anchor VALUES(9)", 1),
					fixture.Exec("CREATE TABLE full_mixed_meta(id INT)", 0),
					fixture.Exec("CREATE UNIQUE INDEX full_mixed_meta_ix ON full_mixed_meta(id)", 0),
					fixture.Exec("INSERT INTO full_mixed_meta VALUES(1),(2)", 2),
				} {
					f.run(step)
				}
				identity := fullMixedIdentityOf(f, "full_mixed_meta")
				a := fullMixedReader(f)
				defer a.ExecContext(f.p.Ctx, "ROLLBACK")
				before, index := f.locator("full_mixed_meta"), f.locator("full_mixed_meta_ix")
				f.command(fixture.Exec("VACUUM FULL full_mixed_meta", 0))
				f.rewritten("full_mixed_meta", before)
				f.rewritten("full_mixed_meta_ix", index)
				if after := fullMixedIdentityOf(f, "full_mixed_meta"); after != identity {
					f.t.Fatal("FULL changed defining identity", after, identity)
				}
				if !metadataFirst {
					f.command(fixture.Exec("TRUNCATE full_mixed_meta", 0))
				}
				f.run(fixture.Exec("ALTER TABLE full_mixed_meta RENAME TO full_mixed_renamed", 0))
				f.run(fixture.Exec("ALTER TABLE full_mixed_renamed OWNER TO full_mixed_owner", 0))
				if metadataFirst {
					f.command(fixture.Exec("TRUNCATE full_mixed_renamed", 0))
				}
				f.run(fixture.Exec("INSERT INTO full_mixed_renamed VALUES(4)", 1))
				before, index = f.locator("full_mixed_renamed"), f.locator("full_mixed_meta_ix")
				f.command(fixture.Exec("VACUUM FULL full_mixed_renamed", 0))
				f.rewritten("full_mixed_renamed", before)
				f.rewritten("full_mixed_meta_ix", index)
				var newOwner int64
				if err := f.c.QueryRowContext(f.p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='full_mixed_owner'").Scan(&newOwner); err != nil {
					f.t.Fatal(err)
				}
				after := fullMixedIdentityOf(f, "full_mixed_renamed")
				identity.owner = newOwner
				if after != identity {
					f.t.Fatal("compatible metadata transition changed other identity", after, identity)
				}
				vacuumRows(f.t, f.p, f.b, a, "full_mixed_renamed")
				vacuumRows(f.t, f.p, f.b, f.c, "full_mixed_renamed", 4)
				vacuumRun(f.t, f.p, f.b, a, fixture.Exec("ROLLBACK", 0))
				f.emptyProgress()
			}, "full_mixed_owner")
		})
	}
}
