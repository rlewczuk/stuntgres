package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceVacuumFullMetadataRetention(t *testing.T) {
	for _, tc := range []struct {
		name          string
		owner         bool
		metadataFirst bool
		denied        bool
		materialized  bool
	}{
		{name: "full_then_rename"},
		{name: "rename_then_full", metadataFirst: true},
		{name: "full_then_owner", owner: true},
		{name: "owner_then_full", owner: true, metadataFirst: true},
		{name: "current_acl_denied_full_then_owner", owner: true, denied: true},
		{name: "current_acl_denied_owner_then_full", owner: true, metadataFirst: true, denied: true},
		{name: "concurrent_mv_full_then_rename", materialized: true},
		{name: "concurrent_mv_rename_then_full", materialized: true, metadataFirst: true},
		{name: "concurrent_mv_full_then_owner", materialized: true, owner: true},
		{name: "concurrent_mv_owner_then_full", materialized: true, owner: true, metadataFirst: true},
		{name: "concurrent_mv_acl_denied_full_then_owner", materialized: true, owner: true, denied: true},
		{name: "concurrent_mv_acl_denied_owner_then_full", materialized: true, owner: true, metadataFirst: true, denied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var roles []string
			if tc.owner {
				roles = []string{"full_meta_owner", "full_meta_reader"}
			}
			fullVacuumCases(t, func(f *fullVacuumCase) {
				alterKind, relationKind, deniedKind := "TABLE", "r", "table"
				setup := []fixture.Step{
					fixture.Exec("CREATE TABLE full_meta_anchor(id INT)", 0),
					fixture.Exec("INSERT INTO full_meta_anchor VALUES(9)", 1),
				}
				if tc.materialized {
					alterKind, relationKind, deniedKind = "MATERIALIZED VIEW", "m", "materialized view"
					setup = append(setup,
						fixture.Exec("CREATE TABLE full_meta_base(id INT PRIMARY KEY)", 0),
						fixture.Exec("INSERT INTO full_meta_base VALUES(1),(2)", 2),
						fixture.Exec("CREATE MATERIALIZED VIEW full_meta_target AS SELECT id FROM full_meta_base", 2),
						fixture.Exec("CREATE UNIQUE INDEX full_meta_target_pkey ON full_meta_target(id)", 0),
					)
				} else {
					setup = append(setup,
						fixture.Exec("CREATE TABLE full_meta_target(id INT PRIMARY KEY)", 0),
						fixture.Exec("INSERT INTO full_meta_target VALUES(1),(2)", 2),
					)
				}
				for _, step := range setup {
					f.run(step)
				}
				identity := fullMixedIdentityOf(f, "full_meta_target")
				indexIdentity := fullMixedIdentityOf(f, "full_meta_target_pkey")
				if identity.oid <= 0 || identity.rowType <= 0 || identity.namespace <= 0 || identity.owner <= 0 || identity.kind != relationKind || identity.attributes != 1 || indexIdentity.oid <= 0 || indexIdentity.owner != identity.owner || indexIdentity.kind != "i" {
					f.t.Fatal("missing independently identified heap/index metadata", identity, indexIdentity)
				}
				var newOwner int64
				if tc.owner {
					for _, query := range []string{
						"CREATE ROLE full_meta_owner",
						"CREATE ROLE full_meta_reader",
						"GRANT USAGE,CREATE ON SCHEMA public TO full_meta_owner",
						"GRANT USAGE ON SCHEMA public TO full_meta_reader",
						"REVOKE ALL ON full_meta_anchor,full_meta_target FROM PUBLIC",
						"GRANT SELECT ON full_meta_anchor,full_meta_target TO full_meta_reader",
					} {
						f.run(fixture.Exec(query, 0))
					}
					if err := f.c.QueryRowContext(f.p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='full_meta_owner'").Scan(&newOwner); err != nil {
						f.t.Fatal(err)
					}
					if newOwner <= 0 || newOwner == identity.owner {
						f.t.Fatal("ownership fixture did not establish a distinct owner", newOwner)
					}
				}
				reader := vacuumConn(f.t, f.p, f.b)
				defer func() {
					if _, err := reader.ExecContext(f.p.Ctx, "ROLLBACK"); err != nil {
						f.t.Error(err)
					}
					if tc.owner {
						if _, err := reader.ExecContext(f.p.Ctx, "RESET ROLE"); err != nil {
							f.t.Error(err)
						}
					}
				}()
				if tc.owner {
					vacuumRun(f.t, f.p, f.b, reader, fixture.Exec("SET ROLE full_meta_reader", 0))
					vacuumRun(f.t, f.p, f.b, reader, fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{"full_meta_reader"}}))
				}
				vacuumRun(f.t, f.p, f.b, reader, fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
				// Establish the old frontier through the anchor only. Neither a
				// target SELECT nor target Prepare may obstruct the writer's AX.
				vacuumRows(f.t, f.p, f.b, reader, "full_meta_anchor", 9)
				if tc.materialized {
					beforeRefresh, beforeIndex := f.locator("full_meta_target"), f.locator("full_meta_target_pkey")
					f.run(fixture.Exec("UPDATE full_meta_base SET id=3 WHERE id=1", 1))
					f.command(fixture.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY full_meta_target", 0))
					afterRefresh, afterIndex := f.locator("full_meta_target"), f.locator("full_meta_target_pkey")
					// PostgreSQL concurrent refresh need not change physical
					// locators. Only stable OIDs and actual nonzero storage are
					// required here; every subsequent FULL remains strictly fresh.
					if beforeRefresh.locator <= 0 || beforeIndex.locator <= 0 || afterRefresh.oid != beforeRefresh.oid || afterIndex.oid != beforeIndex.oid || afterRefresh.locator <= 0 || afterIndex.locator <= 0 || fullMixedIdentityOf(f, "full_meta_target") != identity || fullMixedIdentityOf(f, "full_meta_target_pkey") != indexIdentity {
						f.t.Fatal("concurrent refresh lost actual heap/index identity")
					}
					vacuumRows(f.t, f.p, f.b, f.c, "full_meta_target", 2, 3)
				} else {
					f.run(fixture.Exec("UPDATE full_meta_target SET id=3 WHERE id=1", 1))
				}
				name := "full_meta_target"
				checkIdentity := func() {
					f.t.Helper()
					if got := fullMixedIdentityOf(f, name); got != identity {
						f.t.Fatal("metadata/FULL changed unrelated heap identity", got, identity)
					}
					if got := fullMixedIdentityOf(f, "full_meta_target_pkey"); got != indexIdentity {
						f.t.Fatal("metadata/FULL changed unrelated index identity", got, indexIdentity)
					}
				}
				rewrite := func() {
					f.t.Helper()
					heap, index := f.locator(name), f.locator("full_meta_target_pkey")
					f.command(fixture.Exec("VACUUM FULL "+name, 0))
					f.rewritten(name, heap)
					f.rewritten("full_meta_target_pkey", index)
					checkIdentity()
				}
				metadata := func() {
					f.t.Helper()
					heap, index := f.locator(name), f.locator("full_meta_target_pkey")
					if tc.owner {
						f.run(fixture.Exec("ALTER "+alterKind+" "+name+" OWNER TO full_meta_owner", 0))
						identity.owner, indexIdentity.owner = newOwner, newOwner
					} else {
						f.run(fixture.Exec("ALTER "+alterKind+" full_meta_target RENAME TO full_meta_renamed", 0))
						name = "full_meta_renamed"
					}
					// Metadata alone never supplies a fabricated physical rewrite.
					f.unchanged(name, heap)
					f.unchanged("full_meta_target_pkey", index)
					checkIdentity()
				}
				if tc.metadataFirst {
					metadata()
					rewrite()
				} else {
					rewrite()
					metadata()
				}
				heap, index := f.locator(name), f.locator("full_meta_target_pkey")
				if tc.denied {
					f.run(fixture.Exec("REVOKE SELECT ON full_meta_target FROM full_meta_reader", 0))
				}
				query := "SELECT id FROM " + name + " ORDER BY id"
				var target fixture.Executor = reader
				var afterError fixture.Executor = reader
				if f.prepared {
					// Both statements bind on the actual retained reader only after
					// all writer work. Prepare the anchor before any transaction error.
					target = vacuumHeld(f.t, f.p, reader, query)
					afterError = vacuumHeld(f.t, f.p, reader, "SELECT id FROM full_meta_anchor ORDER BY id")
				}
				if tc.denied {
					denied := fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for "+deniedKind+" full_meta_target"))
					denied.SelectRows = true
					vacuumRun(f.t, f.p, f.b, target, denied)
					aborted := fixture.ErrorDiagnostic("SELECT id FROM full_meta_anchor ORDER BY id", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
					aborted.SelectRows = true
					vacuumRun(f.t, f.p, f.b, afterError, aborted)
				} else {
					vacuumRun(f.t, f.p, f.b, target, fixture.Select(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				}
				vacuumRun(f.t, f.p, f.b, reader, fixture.Exec("ROLLBACK", 0))
				if tc.owner {
					vacuumRun(f.t, f.p, f.b, reader, fixture.Exec("RESET ROLE", 0))
				}
				// The old-reader outcome is independent of the actual current rows.
				f.run(fixture.Select(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
				f.unchanged(name, heap)
				f.unchanged("full_meta_target_pkey", index)
				checkIdentity()
				f.emptyProgress()
			}, roles...)
		})
	}
}
