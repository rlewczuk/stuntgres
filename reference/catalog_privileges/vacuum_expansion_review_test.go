package reference_test

import (
	"context"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceVacuumF7ExpansionAuthority(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			for _, change := range []string{"child_grant", "child_revoke", "no_main_child_revoke", "root_grant", "drop_child", "drop_explicit", "drop_omitted", "recreate_explicit", "rename_explicit", "truncate_explicit", "view_revoke"} {
				t.Run("work_wait/"+change, func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"expansion_actor"})
					vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						owner, holder := vacuumConn(t, p, b), vacuumConn(t, p, b)
						run := func(s fixture.Step) { vacuumRun(t, p, b, owner, s) }
						for _, q := range []string{"CREATE ROLE expansion_actor", "GRANT USAGE ON SCHEMA public TO expansion_actor", "CREATE TABLE expansion_anchor(id INT)", "CREATE TABLE expansion_parent(id INT)", "CREATE TABLE expansion_child() INHERITS(expansion_parent)", "CREATE TABLE expansion_later(id INT)", "GRANT MAINTAIN ON expansion_anchor,expansion_later TO expansion_actor"} {
							run(fixture.Exec(q, 0))
						}
						run(fixture.Exec("INSERT INTO expansion_parent VALUES(1)", 1))
						run(fixture.Exec("INSERT INTO expansion_child VALUES(2)", 1))
						run(fixture.Exec("INSERT INTO expansion_later VALUES(3)", 1))
						if change != "root_grant" {
							run(fixture.Exec("GRANT MAINTAIN ON expansion_parent TO expansion_actor", 0))
						}
						if change != "child_grant" {
							run(fixture.Exec("GRANT MAINTAIN ON expansion_child TO expansion_actor", 0))
						}
						if change == "view_revoke" {
							run(fixture.Exec("CREATE VIEW expansion_view AS SELECT 1 AS id", 0))
							run(fixture.Exec("GRANT MAINTAIN ON expansion_view TO expansion_actor", 0))
						}
						var notices []fixture.Diagnostic
						_, worker, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
						if change != "drop_omitted" {
							fixture.RunStep(t, p.Ctx, worker, rec, fixture.Exec("SET ROLE expansion_actor", 0))
						}
						target, q := "expansion_parent", "VACUUM expansion_parent"
						switch change {
						case "no_main_child_revoke":
							q = "VACUUM (PROCESS_MAIN false) expansion_parent"
						case "root_grant":
							target = "expansion_anchor"
							q = "VACUUM expansion_anchor,expansion_parent"
						case "drop_child":
							target = "expansion_anchor"
							q = "VACUUM expansion_anchor,expansion_parent"
						case "drop_explicit", "recreate_explicit", "rename_explicit", "truncate_explicit":
							target = "expansion_anchor"
							q = "VACUUM expansion_anchor,expansion_later"
						case "drop_omitted":
							target = "expansion_anchor"
							q = "VACUUM"
						case "view_revoke":
							target = "expansion_view"
							q = "VACUUM (PROCESS_MAIN false) expansion_view"
						}
						vacuumRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
						defer holder.ExecContext(p.Ctx, "ROLLBACK")
						vacuumRun(t, p, b, holder, fixture.Exec("LOCK TABLE ONLY "+target+" IN SHARE MODE", 0))
						notices = nil
						join := vacuumPhaseStart(t, p, worker, q, held)
						vacuumPhaseWait(t, p, owner, target)
						var want []fixture.Diagnostic
						switch change {
						case "child_grant":
							run(fixture.Exec("GRANT MAINTAIN ON expansion_child TO expansion_actor", 0))
						case "child_revoke", "no_main_child_revoke":
							run(fixture.Exec("REVOKE MAINTAIN ON expansion_child FROM expansion_actor", 0))
							want = []fixture.Diagnostic{vacuumPhaseWarning("01000", `permission denied to vacuum "expansion_child", skipping it`)}
						case "root_grant":
							run(fixture.Exec("GRANT MAINTAIN ON expansion_parent TO expansion_actor", 0))
							want = []fixture.Diagnostic{vacuumPhaseWarning("01000", `permission denied to vacuum "expansion_parent", skipping it`)}
						case "drop_child":
							run(fixture.Exec("DROP TABLE expansion_child", 0))
						case "drop_explicit", "drop_omitted", "recreate_explicit":
							run(fixture.Exec("DROP TABLE expansion_later", 0))
							if change == "recreate_explicit" {
								run(fixture.Exec("CREATE TABLE expansion_later(id INT)", 0))
								run(fixture.Exec("INSERT INTO expansion_later VALUES(11)", 1))
								run(fixture.Exec("GRANT MAINTAIN ON expansion_later TO expansion_actor", 0))
							}
							if change != "drop_omitted" {
								want = []fixture.Diagnostic{vacuumPhaseWarning("42P01", `skipping vacuum of "expansion_later" --- relation no longer exists`)}
							}
						case "rename_explicit":
							run(fixture.Exec("ALTER TABLE expansion_later RENAME TO expansion_renamed", 0))
							run(fixture.Exec("CREATE TABLE expansion_later(id INT)", 0))
							run(fixture.Exec("INSERT INTO expansion_later VALUES(11)", 1))
							run(fixture.Exec("GRANT MAINTAIN ON expansion_later TO expansion_actor", 0))
						case "truncate_explicit":
							run(fixture.Exec("TRUNCATE expansion_later", 0))
						case "view_revoke":
							run(fixture.Exec("REVOKE MAINTAIN ON expansion_view FROM expansion_actor", 0))
							want = []fixture.Diagnostic{vacuumPhaseWarning("01000", `permission denied to vacuum "expansion_view", skipping it`)}
						}
						vacuumRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
						join()
						vacuumPhaseNotices(t, notices, want)
						switch change {
						case "child_grant":
							vacuumCount(t, p.Ctx, owner, "expansion_parent", 1)
							vacuumCount(t, p.Ctx, owner, "expansion_child", 1)
						case "child_revoke":
							vacuumCount(t, p.Ctx, owner, "expansion_parent", 1)
							vacuumCount(t, p.Ctx, owner, "expansion_child", 0)
						case "no_main_child_revoke":
							vacuumCount(t, p.Ctx, owner, "expansion_parent", 0)
							vacuumCount(t, p.Ctx, owner, "expansion_child", 0)
						case "root_grant":
							vacuumCount(t, p.Ctx, owner, "expansion_anchor", 1)
							vacuumCount(t, p.Ctx, owner, "expansion_parent", 0)
							vacuumCount(t, p.Ctx, owner, "expansion_child", 1)
						case "drop_child":
							vacuumCount(t, p.Ctx, owner, "expansion_anchor", 1)
							vacuumCount(t, p.Ctx, owner, "expansion_parent", 1)
						case "drop_explicit", "drop_omitted":
							vacuumCount(t, p.Ctx, owner, "expansion_anchor", 1)
						case "recreate_explicit":
							vacuumCount(t, p.Ctx, owner, "expansion_anchor", 1)
							vacuumCount(t, p.Ctx, owner, "expansion_later", 0)
						case "rename_explicit":
							vacuumCount(t, p.Ctx, owner, "expansion_anchor", 1)
							vacuumCount(t, p.Ctx, owner, "expansion_renamed", 1)
							vacuumCount(t, p.Ctx, owner, "expansion_later", 0)
							vacuumRows(t, p, b, owner, "expansion_renamed", 3)
						case "truncate_explicit":
							vacuumCount(t, p.Ctx, owner, "expansion_anchor", 1)
							vacuumCount(t, p.Ctx, owner, "expansion_later", 1)
							vacuumRows(t, p, b, owner, "expansion_later")
						case "view_revoke":
							vacuumCount(t, p.Ctx, owner, "expansion_parent", 0)
							vacuumCount(t, p.Ctx, owner, "expansion_child", 0)
						}
						vacuumRun(t, p, b, owner, fixture.Select("SELECT id FROM ONLY expansion_parent ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
						if change != "drop_child" {
							vacuumRows(t, p, b, owner, "expansion_child", 2)
						}
						if change == "recreate_explicit" || change == "rename_explicit" {
							vacuumRows(t, p, b, owner, "expansion_later", 11)
						}
						vacuumProgressEmpty(t, p, b, owner)
					})
				})
			}
			for _, blocked := range []string{"root_as", "root_work", "child_work", "omitted_work", "partition_only_work"} {
				t.Run("origin_skip/"+blocked, func(t *testing.T) {
					vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						owner, holder := vacuumConn(t, p, b), vacuumConn(t, p, b)
						for _, q := range []string{"CREATE TABLE expansion_parent(id INT)", "CREATE TABLE expansion_child() INHERITS(expansion_parent)"} {
							vacuumRun(t, p, b, owner, fixture.Exec(q, 0))
						}
						var notices []fixture.Diagnostic
						_, worker, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
						target, mode, q := "expansion_parent", "SHARE", "VACUUM (SKIP_LOCKED) expansion_parent"
						if blocked == "root_as" {
							mode = "ACCESS EXCLUSIVE"
						}
						if blocked == "child_work" {
							target = "expansion_child"
						}
						if blocked == "omitted_work" {
							q = "VACUUM (SKIP_LOCKED)"
						}
						if blocked == "partition_only_work" {
							vacuumRun(t, p, b, owner, fixture.Exec("CREATE TABLE expansion_partition(id INT) PARTITION BY RANGE(id)", 0))
							target = "expansion_partition"
							q = "VACUUM (SKIP_LOCKED,PROCESS_MAIN false) ONLY expansion_partition"
						}
						h := vacuumHeld(t, p, worker, q)
						vacuumRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
						defer holder.ExecContext(p.Ctx, "ROLLBACK")
						nativePartitionBoundary := blocked == "partition_only_work" && !b.PG
						if nativePartitionBoundary {
							// Existing LOCK partition setup is not a VACUUM capability. Keep the
							// real PG holder and supplement native with canonical manager coverage.
							vacuumRun(t, p, b, holder, fixture.ImplicitError("LOCK TABLE ONLY "+target+" IN "+mode+" MODE", fixture.Diagnostic{Code: "0A000", Message: "locking partition hierarchies is unsupported"}))
							vacuumRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
						} else {
							vacuumRun(t, p, b, holder, fixture.Exec("LOCK TABLE ONLY "+target+" IN "+mode+" MODE", 0))
						}
						notices = nil
						if held {
							fixture.RunStep(t, p.Ctx, h, rec, fixture.Exec(q, 0))
						} else {
							fixture.RunStep(t, p.Ctx, worker, rec, fixture.Exec(q, 0))
						}
						var want []fixture.Diagnostic
						parentCount, childCount := int64(0), int64(0)
						switch blocked {
						case "root_as":
							want = []fixture.Diagnostic{vacuumPhaseWarning("55P03", `skipping vacuum of "expansion_parent" --- lock not available`)}
						case "root_work":
							childCount = 1
							want = []fixture.Diagnostic{vacuumPhaseWarning("55P03", `skipping vacuum of "expansion_parent" --- lock not available`)}
						case "child_work":
							parentCount = 1
						case "omitted_work":
							childCount = 1
						case "partition_only_work":
							want = []fixture.Diagnostic{vacuumPhaseWarning("01000", `VACUUM ONLY of partitioned table "expansion_partition" has no effect`)}
							if !nativePartitionBoundary {
								want = append(want, vacuumPhaseWarning("55P03", `skipping vacuum of "expansion_partition" --- lock not available`))
							}
						}
						vacuumPhaseNotices(t, notices, want)
						vacuumCount(t, p.Ctx, owner, "expansion_parent", parentCount)
						vacuumCount(t, p.Ctx, owner, "expansion_child", childCount)
						vacuumProgressEmpty(t, p, b, owner)
					})
				})
			}
			for _, shape := range []string{"denied_root", "only_parent", "only_denied_partition", "cross_schema", "diamond_denials", "diamond_overlap", "diamond_repeat"} {
				t.Run("expansion/"+shape, func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"expansion_actor"})
					if shape == "cross_schema" {
						fixture.SchemaReferenceCleanup(t, "expansion_private")
					}
					vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						owner := vacuumConn(t, p, b)
						run := func(q string) { vacuumRun(t, p, b, owner, fixture.Exec(q, 0)) }
						for _, q := range []string{"CREATE ROLE expansion_actor", "GRANT USAGE ON SCHEMA public TO expansion_actor", "CREATE TABLE expansion_parent(id INT)", "CREATE TABLE expansion_child() INHERITS(expansion_parent)"} {
							run(q)
						}
						q := "VACUUM expansion_parent"
						var want []fixture.Diagnostic
						pc, cc := int64(1), int64(1)
						switch shape {
						case "denied_root":
							run("GRANT MAINTAIN ON expansion_child TO expansion_actor")
							pc = 0
							want = []fixture.Diagnostic{vacuumPhaseWarning("01000", `permission denied to vacuum "expansion_parent", skipping it`)}
						case "only_parent":
							run("GRANT MAINTAIN ON expansion_parent TO expansion_actor")
							q = "VACUUM ONLY expansion_parent"
							cc = 0
						case "only_denied_partition":
							run("CREATE TABLE expansion_partition(id INT) PARTITION BY RANGE(id)")
							q = "VACUUM ONLY expansion_partition"
							pc = 0
							cc = 0
							want = []fixture.Diagnostic{vacuumPhaseWarning("01000", `permission denied to vacuum "expansion_partition", skipping it`), vacuumPhaseWarning("01000", `VACUUM ONLY of partitioned table "expansion_partition" has no effect`)}
						case "cross_schema":
							run("CREATE SCHEMA expansion_private")
							run("DROP TABLE expansion_child")
							run("CREATE TABLE expansion_private.expansion_child() INHERITS(expansion_parent)")
							run("GRANT MAINTAIN ON expansion_parent,expansion_private.expansion_child TO expansion_actor")
						default:
							run("CREATE TABLE expansion_a() INHERITS(expansion_parent)")
							run("CREATE TABLE expansion_b() INHERITS(expansion_parent)")
							run("CREATE TABLE expansion_g() INHERITS(expansion_a,expansion_b)")
							if shape == "diamond_denials" {
								run("GRANT MAINTAIN ON expansion_parent,expansion_child TO expansion_actor")
								var a, bid int64
								if err := owner.QueryRowContext(p.Ctx, "SELECT 'expansion_a'::regclass::oid,'expansion_b'::regclass::oid").Scan(&a, &bid); err != nil {
									t.Fatal(err)
								}
								names := []string{"expansion_a", "expansion_b"}
								if a > bid {
									names[0], names[1] = names[1], names[0]
								}
								for _, name := range append(names, "expansion_g") {
									want = append(want, vacuumPhaseWarning("01000", `permission denied to vacuum "`+name+`", skipping it`))
								}
							} else if shape == "diamond_overlap" {
								q = "VACUUM expansion_parent,expansion_a"
							} else {
								q = "VACUUM expansion_parent,expansion_parent"
								pc = 2
								cc = 2
							}
						}
						var notices []fixture.Diagnostic
						_, worker, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
						if shape != "diamond_overlap" && shape != "diamond_repeat" {
							fixture.RunStep(t, p.Ctx, worker, rec, fixture.Exec("SET ROLE expansion_actor", 0))
						}
						notices = nil
						vacuumPhaseExecute(t, p, worker, rec, held, fixture.Exec(q, 0))
						vacuumPhaseNotices(t, notices, want)
						vacuumCount(t, p.Ctx, owner, "expansion_parent", pc)
						child := "expansion_child"
						if shape == "cross_schema" {
							child = "expansion_private.expansion_child"
						}
						vacuumCount(t, p.Ctx, owner, child, cc)
						if strings.HasPrefix(shape, "diamond_") {
							counts := []int64{0, 0, 0}
							if shape == "diamond_overlap" {
								counts = []int64{2, 1, 2}
							} else if shape == "diamond_repeat" {
								counts = []int64{2, 2, 2}
							}
							for i, name := range []string{"expansion_a", "expansion_b", "expansion_g"} {
								vacuumCount(t, p.Ctx, owner, name, counts[i])
							}
						}
						vacuumProgressEmpty(t, p, b, owner)
						if shape == "cross_schema" {
							run("DROP TABLE expansion_private.expansion_child")
							run("DROP SCHEMA expansion_private")
						}
					})
				})
			}
			t.Run("foreign_temp_work_gate", func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"expansion_temp_actor"})
				vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					owner := vacuumConn(t, p, b)
					vacuumRun(t, p, b, owner, fixture.Exec("CREATE TEMP TABLE expansion_foreign(id INT)", 0))
					var ns string
					if err := owner.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_namespace n JOIN pg_class c ON c.relnamespace=n.oid WHERE c.oid='expansion_foreign'::regclass").Scan(&ns); err != nil {
						t.Fatal(err)
					}
					qualified := `"` + strings.ReplaceAll(ns, `"`, `""`) + `".expansion_foreign`
					var notices []fixture.Diagnostic
					_, worker, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					vacuumRun(t, p, b, owner, fixture.Exec("BEGIN", 0))
					defer owner.ExecContext(p.Ctx, "ROLLBACK")
					vacuumRun(t, p, b, owner, fixture.Exec("LOCK TABLE ONLY expansion_foreign IN SHARE MODE", 0))
					notices = nil
					vacuumPhaseExecute(t, p, worker, rec, held, fixture.Exec("VACUUM (SKIP_LOCKED,PROCESS_MAIN false) "+qualified, 0))
					vacuumPhaseNotices(t, notices, []fixture.Diagnostic{vacuumPhaseWarning("55P03", `skipping vacuum of "expansion_foreign" --- lock not available`)})
					vacuumRun(t, p, b, owner, fixture.Exec("ROLLBACK", 0))
					notices = nil
					vacuumPhaseExecute(t, p, worker, rec, held, fixture.Exec("VACUUM "+qualified, 0))
					vacuumPhaseNotices(t, notices, nil)
					vacuumRun(t, p, b, owner, fixture.Exec("CREATE ROLE expansion_temp_actor", 0))
					schema := `"` + strings.ReplaceAll(ns, `"`, `""`) + `"`
					vacuumRun(t, p, b, owner, fixture.Exec("GRANT USAGE ON SCHEMA "+schema+" TO expansion_temp_actor", 0))
					defer func() {
						// Match the established role/schema cleanup budget; keep the physical
						// TEMP owner alive until ACL dependencies and the table are removed.
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
						defer cancel()
						for _, q := range []string{"REVOKE USAGE ON SCHEMA " + schema + " FROM expansion_temp_actor", "DROP TABLE IF EXISTS expansion_foreign"} {
							if _, err := owner.ExecContext(ctx, q); err != nil {
								t.Error("foreign TEMP fixture cleanup", err)
							}
						}
					}()
					fixture.RunStep(t, p.Ctx, worker, rec, fixture.Exec("SET ROLE expansion_temp_actor", 0))
					notices = nil
					vacuumPhaseExecute(t, p, worker, rec, held, fixture.Exec("VACUUM "+qualified, 0))
					vacuumPhaseNotices(t, notices, []fixture.Diagnostic{vacuumPhaseWarning("01000", `permission denied to vacuum "expansion_foreign", skipping it`)})
					vacuumRun(t, p, b, owner, fixture.Exec("GRANT MAINTAIN ON "+qualified+" TO expansion_temp_actor", 0))
					notices = nil
					vacuumPhaseExecute(t, p, worker, rec, held, fixture.Exec("VACUUM "+qualified, 0))
					vacuumPhaseNotices(t, notices, nil)
					if !b.PG {
						// Maintenance namespace authority does not grant peer TEMP data access.
						denied := fixture.ImplicitError("SELECT id FROM "+qualified, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema " + ns, Position: fixture.Position(len("SELECT id FROM ") + 1)})
						denied.SelectRows = true
						fixture.RunStep(t, p.Ctx, worker, rec, denied)
					}
					fixture.RunStep(t, p.Ctx, worker, rec, fixture.Exec("RESET ROLE", 0))
					vacuumCount(t, p.Ctx, owner, "expansion_foreign", 0)
					vacuumProgressEmpty(t, p, b, worker)
				})
			})
		})
	}
}
