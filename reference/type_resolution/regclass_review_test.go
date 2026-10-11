package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegclassMixedDefaultReplay(t *testing.T) {
	p := fixture.NewScenario(t)
	replayed := false
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			if !b.PG || !replayed {
				for _, q := range []string{
					"CREATE TABLE rr_order_text(v TEXT)",
					"CREATE TABLE rr_order_early(v TEXT)",
					"CREATE TABLE rr_order_late(v INT)",
					"ALTER TABLE rr_order_early ALTER COLUMN v SET DEFAULT ('x'::text || 'rr_order_late'::regclass)",
					"ALTER TABLE rr_order_text ALTER COLUMN v SET DEFAULT ('x'::text || 'rr_order_late'::regclass)",
					"ALTER TABLE rr_order_early ADD COLUMN r regclass DEFAULT 'rr_order_late' CHECK(r='rr_order_late'::regclass)",
					"CREATE TABLE rr_order_trigger(r regclass DEFAULT 'rr_order_late')",
				} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO rr_order_text DEFAULT VALUES", 1))
				run(fixture.Exec("INSERT INTO rr_order_early DEFAULT VALUES", 1))
				run(fixture.Exec("INSERT INTO rr_order_trigger DEFAULT VALUES", 1))
			}
			check := fixture.Select("SELECT v,r FROM rr_order_early", []string{"v", "r"}, []string{"TEXT", "REGCLASS"}, [][]any{{"xrr_order_late", "rr_order_late"}})
			run(check)
			textCheck := fixture.Select("SELECT v FROM rr_order_text", []string{"v"}, []string{"TEXT"}, [][]any{{"xrr_order_late"}})
			run(textCheck)
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				fixture.ArrayReferenceNativeRoundtrip(t, p, c, b, check, fixture.Exec("CREATE TABLE rr_order_allocator(v INT)", 0))
				target := fixture.NewFixture(t, false, true)
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE rr_order_fresh_allocator(v INT)", 0))
				if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal("fresh mixed-default Restore", err)
				}
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO rr_order_text DEFAULT VALUES", 1))
				freshText := textCheck
				freshText.Rows = append(freshText.Rows, []any{"xrr_order_late"})
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, freshText)
				if p.Postgres != nil {
					if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
						t.Fatal("PostgreSQL replay of actual emitted SQL", err, dump.String())
					}
					replayed = true
				}
			}
			run(fixture.Exec("INSERT INTO rr_order_early DEFAULT VALUES", 1))
			run(fixture.Exec("INSERT INTO rr_order_text DEFAULT VALUES", 1))
			textCheck.Rows = append(textCheck.Rows, []any{"xrr_order_late"})
			run(textCheck)
			run(fixture.Exec("INSERT INTO rr_order_trigger DEFAULT VALUES", 1))
			check.Rows = append(check.Rows, []any{"xrr_order_late", "rr_order_late"})
			run(check)
			run(fixture.RegclassRows("SELECT r FROM rr_order_trigger", "r", "rr_order_late", "rr_order_late"))
			run(fixture.Select("SELECT count(*) AS n FROM pg_attrdef WHERE adrelid='rr_order_early'::regclass::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
			run(fixture.Select("SELECT count(*) AS n FROM pg_constraint WHERE conname='rr_order_early_r_check'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			run(fixture.Exec("DROP TABLE rr_order_early,rr_order_text,rr_order_trigger,rr_order_late", 0))
		})
	}
}

func TestReferenceRegclassCascadedViewDependencies(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
					for _, q := range []string{
						"CREATE TABLE rr_chain_target(v INT)",
						"CREATE VIEW rr_chain_view AS SELECT v FROM rr_chain_target",
						"CREATE VIEW rr_chain_leaf AS SELECT v FROM rr_chain_view",
						"CREATE TABLE rr_chain_default(id INT,r regclass DEFAULT 'rr_chain_leaf')",
						"CREATE TABLE rr_chain_check(id INT,r regclass,CONSTRAINT rr_chain_bound CHECK(r='rr_chain_leaf'::regclass))",
					} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO rr_chain_target VALUES(7)", 1))
					run(fixture.Exec("INSERT INTO rr_chain_default(id) VALUES(1)", 1))
					run(fixture.Exec("INSERT INTO rr_chain_check VALUES(1,'rr_chain_leaf')", 1))
					var oid int64
					if err := c.QueryRowContext(p.Ctx, "SELECT 'rr_chain_leaf'::regclass::oid::bigint").Scan(&oid); err != nil {
						t.Fatal(err)
					}
					restrict := fixture.ImplicitError("DROP TABLE rr_chain_target RESTRICT", fixture.Diagnostic{
						Code: "2BP01", Message: "cannot drop table rr_chain_target because other objects depend on it",
						Detail: "view rr_chain_view depends on table rr_chain_target\nview rr_chain_leaf depends on view rr_chain_view\ndefault value for column r of table rr_chain_default depends on view rr_chain_leaf\nconstraint rr_chain_bound on table rr_chain_check depends on view rr_chain_leaf",
						Hint:   "Use DROP ... CASCADE to drop the dependent objects too.",
					})
					restrict.Prepared = prepared
					restrict.ErrorStage = "execute"
					run(restrict)
					run(fixture.Select("SELECT v FROM rr_chain_leaf", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
					for _, table := range []string{"rr_chain_default", "rr_chain_check"} {
						run(fixture.RegclassRows("SELECT r FROM "+table, "r", "rr_chain_leaf"))
					}
					notices = nil
					s := fixture.Exec("DROP TABLE rr_chain_target CASCADE", 0)
					s.Prepared = prepared
					run(s)
					fixture.DomainReferenceNotice(t, &notices, "drop cascades to 4 other objects", "drop cascades to view rr_chain_view\ndrop cascades to view rr_chain_leaf\ndrop cascades to default value for column r of table rr_chain_default\ndrop cascades to constraint rr_chain_bound on table rr_chain_check")
					run(fixture.Exec("INSERT INTO rr_chain_default(id) VALUES(2)", 1))
					run(fixture.Exec("INSERT INTO rr_chain_check VALUES(2,0::regclass)", 1))
					run(fixture.RegclassRows("SELECT r FROM rr_chain_default ORDER BY id", "r", strconv.FormatInt(oid, 10), nil))
					run(fixture.RegclassRows("SELECT r FROM rr_chain_check ORDER BY id", "r", strconv.FormatInt(oid, 10), "-"))
					for _, q := range []string{
						"SELECT count(*) AS n FROM pg_attrdef WHERE adrelid='rr_chain_default'::regclass::oid",
						"SELECT count(*) AS n FROM pg_constraint WHERE conname='rr_chain_bound'",
						"SELECT count(*) AS n FROM pg_class WHERE relname='rr_chain_view' OR relname='rr_chain_leaf' OR relname='rr_chain_target'",
					} {
						run(fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					}
				})
			}
		})
	}
}

func TestReferenceRegclassWriteDiagnosticPresentation(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "rr_diag_scope")
	fixture.RoleReferenceCleanup(t, []string{"rr_diag_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{
				"CREATE SCHEMA rr_diag_scope", "CREATE TABLE rr_diag_scope.original(v INT)",
				"CREATE DOMAIN rr_diag_domain AS regclass", "CREATE DOMAIN rr_diag_array_domain AS regclass[]",
				"CREATE TABLE rr_diag(id INT,n INT,r regclass,d rr_diag_domain,a regclass[],da rr_diag_array_domain,CONSTRAINT rr_diag_bound CHECK(n>0))",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO rr_diag VALUES(1,1,0::regclass,0::regclass,'{0,NULL}','{0,NULL}'),(2,1,'rr_diag_scope.original','rr_diag_scope.original','{rr_diag_scope.original,NULL}','{rr_diag_scope.original,NULL}')", 2))
			run(fixture.Exec(`ALTER TABLE rr_diag_scope.original RENAME TO "Moved.Target"`, 0))
			for _, prepared := range []bool{false, true} {
				for _, returning := range []bool{false, true} {
					for _, id := range []int{1, 2} {
						q := fmt.Sprintf("UPDATE rr_diag SET n=0 WHERE id=%d", id)
						if returning {
							q += " RETURNING r,d,a,da"
						}
						detail := fmt.Sprintf("%d, 0, -, -, {-,NULL}, {-,NULL}", id)
						if id == 2 {
							detail = `2, 0, rr_diag_scope."Moved.Target", rr_diag_scope."Moved.Target", {"rr_diag_scope.\"Moved.Target\"",NULL}, {"rr_diag_scope.\"Moved.Target\"",NULL}`
						}
						regclassReviewWriteError(t, p, c, b.Recorder, prepared, returning, q, detail)
						q = "INSERT INTO rr_diag VALUES(3,0,0::regclass,0::regclass,'{0,NULL}','{0,NULL}')"
						if id == 2 {
							q = `INSERT INTO rr_diag VALUES(3,0,'rr_diag_scope."Moved.Target"','rr_diag_scope."Moved.Target"','{"rr_diag_scope.\"Moved.Target\"",NULL}','{"rr_diag_scope.\"Moved.Target\"",NULL}')`
						}
						if returning {
							q += " RETURNING r,d,a,da"
						}
						detail = "3, 0, -, -, {-,NULL}, {-,NULL}"
						if id == 2 {
							detail = `3, 0, rr_diag_scope."Moved.Target", rr_diag_scope."Moved.Target", {"rr_diag_scope.\"Moved.Target\"",NULL}, {"rr_diag_scope.\"Moved.Target\"",NULL}`
						}
						regclassReviewWriteError(t, p, c, b.Recorder, prepared, returning, q, detail)
					}
				}
			}
			run(fixture.Select("SELECT id,n FROM rr_diag ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}}))
			if b.PG {
				// PostgreSQL's established column-ACL oracle; native GRANT remains excluded.
				for _, q := range []string{
					`ALTER TABLE rr_diag ALTER COLUMN r SET DEFAULT 'rr_diag_scope."Moved.Target"'`,
					`ALTER TABLE rr_diag ALTER COLUMN d SET DEFAULT 'rr_diag_scope."Moved.Target"'`,
					`ALTER TABLE rr_diag ALTER COLUMN a SET DEFAULT '{"rr_diag_scope.\"Moved.Target\"",NULL}'`,
					`ALTER TABLE rr_diag ALTER COLUMN da SET DEFAULT '{"rr_diag_scope.\"Moved.Target\"",NULL}'`,
					"CREATE ROLE rr_diag_reader", "GRANT USAGE ON SCHEMA public TO rr_diag_reader",
					"GRANT INSERT(n),UPDATE(n),SELECT(id,r,a) ON rr_diag TO rr_diag_reader", "SET ROLE rr_diag_reader",
				} {
					run(fixture.Exec(q, 0))
				}
				for _, prepared := range []bool{false, true} {
					for _, returning := range []bool{false, true} {
						q := "UPDATE rr_diag SET n=0 WHERE id=2"
						if returning {
							q += " RETURNING r,a"
						}
						regclassReviewWriteError(t, p, c, b.Recorder, prepared, returning, q, `id, n, r, a) = (2, 0, rr_diag_scope."Moved.Target", {"rr_diag_scope.\"Moved.Target\"",NULL}`)
						q = "INSERT INTO rr_diag(n) VALUES(0)"
						if returning {
							q += " RETURNING r,a"
						}
						regclassReviewWriteError(t, p, c, b.Recorder, prepared, returning, q, `id, n, r, a) = (null, 0, rr_diag_scope."Moved.Target", {"rr_diag_scope.\"Moved.Target\"",NULL}`)
					}
				}
				run(fixture.Exec("RESET ROLE", 0))
			}
			run(fixture.Select("SELECT id,n FROM rr_diag ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}}))
		})
	}
}

func TestReferenceRegclassContextualConstructorParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE rr_param_target(v INT)", 0))
			var oid int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'rr_param_target'::regclass::oid::bigint").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				query          string
				peer, repeated bool
			}{
				{"SELECT ARRAY[$1]::regclass[] AS a", false, false},
				{"SELECT ARRAY[$1,'rr_param_target'::regclass] AS a", true, false},
				{"SELECT ARRAY[$1,$1]::regclass[] AS a", false, true},
				{"SELECT ARRAY[$1,'rr_param_target'::regclass,$1] AS a", true, true},
			} {
				stmt, err := c.PrepareContext(p.Ctx, tc.query)
				if err != nil {
					t.Fatal(tc.query, err)
				}
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.query}
				for _, input := range []any{"rr_param_target", strconv.FormatInt(oid, 10), oid, nil, "0", int64(0), "rr_param_target"} {
					value := "rr_param_target"
					if input == nil {
						value = "NULL"
					} else if input == "0" || input == int64(0) {
						value = "-"
					}
					want := "{" + value
					if tc.peer {
						want += ",rr_param_target"
					}
					if tc.repeated {
						want += "," + value
					}
					want += "}"
					s := fixture.Select(tc.query, []string{"a"}, []string{"_REGCLASS"}, [][]any{{want}}, input)
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { regclassMetadata(t, rows, b.PG, true) }
					run(s)
					run(fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				for _, input := range []any{"rr_param_missing", "", "08"} {
					code, message, portal := "42P01", `relation "rr_param_missing" does not exist`, "unnamed portal parameter $1 = '...'"
					if input == "" {
						code, message, portal = "42602", "invalid name syntax", "unnamed portal parameter $1 = ''"
					} else if input == "08" {
						code, message = "22P02", `invalid input syntax for type oid: "08"`
					}
					s := regclassError(tc.query, code, message, "", portal, input)
					s.ErrorStage = "execute"
					run(s)
					run(fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				stmt.Close()
			}
		})
	}
}

func regclassReviewWriteError(t *testing.T, p *fixture.Pair, c *sql.Conn, record *fixture.Recorder, prepared, returning bool, q, detail string) {
	t.Helper()
	s := fixture.ImplicitError(q, fixture.CheckViolation("rr_diag", "rr_diag_bound", detail))
	s.Prepared = prepared
	s.SelectRows = returning
	s.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, c, record, s)
}

func TestReferenceRegclassDropColumnDependencies(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, views := range []int{1, 2} {
			for _, columnDefault := range []bool{false, true} {
				for _, check := range []bool{false, true} {
					t.Run(fmt.Sprintf("prepared=%v/views=%d/column_default=%v/check=%v", prepared, views, columnDefault, check), func(t *testing.T) {
						p := fixture.NewScenario(t)
						for _, b := range p.Backends() {
							t.Run(b.Name, func(t *testing.T) {
								var notices []fixture.Diagnostic
								_, c, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
								run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, record, s) }
								column := "v INT"
								if columnDefault {
									column += " DEFAULT 7"
								}
								if !check {
									column += ",r regclass"
								}
								run(fixture.Exec("CREATE TABLE rr_column_owner("+column+")", 0))
								run(fixture.Exec("CREATE VIEW rr_column_view AS SELECT v FROM rr_column_owner", 0))
								target := "rr_column_view"
								if views == 2 {
									run(fixture.Exec("CREATE VIEW rr_column_leaf AS SELECT v FROM rr_column_view", 0))
									target = "rr_column_leaf"
								}
								if check {
									run(fixture.Exec("ALTER TABLE rr_column_owner ADD COLUMN r regclass DEFAULT '"+target+"'::regclass CONSTRAINT rr_column_bound CHECK(r='"+target+"'::regclass)", 0))
								} else {
									run(fixture.Exec("ALTER TABLE rr_column_owner ALTER COLUMN r SET DEFAULT '"+target+"'::regclass", 0))
								}
								run(fixture.Exec("INSERT INTO rr_column_owner(v) VALUES(7)", 1))
								var oid int64
								if err := c.QueryRowContext(p.Ctx, "SELECT '"+target+"'::regclass::oid::bigint").Scan(&oid); err != nil {
									t.Fatal(err)
								}
								run(fixture.Select("SELECT v,r FROM rr_column_owner", []string{"v", "r"}, []string{"INT4", "REGCLASS"}, [][]any{{int64(7), target}}))
								if check {
									failure := fixture.ImplicitError("INSERT INTO rr_column_owner(v,r) VALUES(9,0::regclass)", fixture.CheckViolation("rr_column_owner", "rr_column_bound", "9, -"))
									failure.Prepared = prepared
									failure.ErrorStage = "execute"
									run(failure)
								}
								labels := []string{"view rr_column_view"}
								details := []string{"view rr_column_view depends on column v of table rr_column_owner"}
								if views == 2 {
									labels = append(labels, "view rr_column_leaf")
									details = append(details, "view rr_column_leaf depends on view rr_column_view")
								}
								labels = append(labels, "default value for column r of table rr_column_owner")
								details = append(details, "default value for column r of table rr_column_owner depends on view "+target)
								if check {
									labels = append(labels, "constraint rr_column_bound on table rr_column_owner")
									details = append(details, "constraint rr_column_bound on table rr_column_owner depends on view "+target)
								}
								restrict := fixture.ImplicitError("ALTER TABLE rr_column_owner DROP COLUMN v RESTRICT", fixture.Diagnostic{
									Code: "2BP01", Message: "cannot drop column v of table rr_column_owner because other objects depend on it",
									Detail: strings.Join(details, "\n"), Hint: "Use DROP ... CASCADE to drop the dependent objects too.",
								})
								restrict.Prepared = prepared
								restrict.ErrorStage = "execute"
								t.Run("restrict", func(t *testing.T) {
									fixture.RunStep(t, p.Ctx, c, record, restrict)
								})
								run(fixture.Select("SELECT v,r FROM rr_column_owner", []string{"v", "r"}, []string{"INT4", "REGCLASS"}, [][]any{{int64(7), target}}))
								defaults := int64(1)
								if columnDefault {
									defaults++
								}
								run(fixture.Select("SELECT count(*) AS n FROM pg_attrdef WHERE adrelid='rr_column_owner'::regclass::oid", []string{"n"}, []string{"INT8"}, [][]any{{defaults}}))
								run(fixture.Select("SELECT v FROM "+target, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
								notices = nil
								drop := fixture.Exec("ALTER TABLE rr_column_owner DROP COLUMN v CASCADE", 0)
								drop.Prepared = prepared
								run(drop)
								noticeDetails := make([]string, len(labels))
								for i, label := range labels {
									noticeDetails[i] = "drop cascades to " + label
								}
								fixture.DomainReferenceNotice(t, &notices, fmt.Sprintf("drop cascades to %d other objects", len(labels)), strings.Join(noticeDetails, "\n"))
								run(fixture.RegclassRows("SELECT r FROM rr_column_owner", "r", strconv.FormatInt(oid, 10)))
								for _, query := range []string{
									"SELECT count(*) AS n FROM pg_attrdef WHERE adrelid='rr_column_owner'::regclass::oid",
									"SELECT count(*) AS n FROM pg_constraint WHERE conname='rr_column_bound'",
									"SELECT count(*) AS n FROM pg_class WHERE relname='rr_column_view' OR relname='rr_column_leaf'",
								} {
									run(fixture.Select(query, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
								}
								run(fixture.Exec("INSERT INTO rr_column_owner DEFAULT VALUES", 1))
								run(fixture.Exec("INSERT INTO rr_column_owner(r) VALUES(0::regclass)", 1))
								run(fixture.RegclassRows("SELECT r FROM rr_column_owner WHERE r IS NULL", "r", nil))
								run(fixture.RegclassRows("SELECT r FROM rr_column_owner WHERE r::oid=0::oid", "r", "-"))
								run(fixture.RegclassRows(fmt.Sprintf("SELECT r FROM rr_column_owner WHERE r::oid=%d::oid", oid), "r", strconv.FormatInt(oid, 10)))
								run(fixture.Select("SELECT count(*) AS n FROM rr_column_owner", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
							})
						}
					})
				}
			}
		}
	}
	t.Run("replacement_order", func(t *testing.T) {
		for _, prepared := range []bool{false, true} {
			for _, replace := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared=%v/replace=%v", prepared, replace), func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							var notices []fixture.Diagnostic
							_, c, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
							run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, record, s) }
							for _, query := range []string{
								"CREATE TABLE rr_replaced_owner(v INT)",
								"CREATE TABLE rr_replaced_original(v INT)",
								"CREATE VIEW rr_replaced_view AS SELECT v FROM rr_replaced_original",
								"ALTER TABLE rr_replaced_owner ADD COLUMN r regclass DEFAULT 'rr_replaced_view' CONSTRAINT rr_replaced_bound CHECK(r='rr_replaced_view'::regclass)",
								"CREATE VIEW rr_replaced_bridge AS SELECT v FROM rr_replaced_owner",
							} {
								run(fixture.Exec(query, 0))
							}
							run(fixture.Exec("INSERT INTO rr_replaced_original VALUES(5)", 1))
							run(fixture.Exec("INSERT INTO rr_replaced_owner(v) VALUES(7)", 1))
							var oid int64
							if err := c.QueryRowContext(p.Ctx, "SELECT 'rr_replaced_view'::regclass::oid::bigint").Scan(&oid); err != nil {
								t.Fatal(err)
							}
							oldDetail := "view rr_replaced_view depends on column v of table rr_replaced_original\ndefault value for column r of table rr_replaced_owner depends on view rr_replaced_view\nconstraint rr_replaced_bound on table rr_replaced_owner depends on view rr_replaced_view"
							t.Run("old_restrict", func(t *testing.T) {
								regclassReplacementRestrict(t, p, c, record, prepared, "rr_replaced_original", oldDetail)
							})
							target := "rr_replaced_original"
							value := int64(5)
							if replace {
								run(fixture.Exec("CREATE OR REPLACE VIEW rr_replaced_view AS SELECT v FROM rr_replaced_bridge", 0))
								target, value = "rr_replaced_owner", 7
								run(fixture.Exec("ALTER TABLE rr_replaced_original DROP COLUMN v RESTRICT", 0))
							}
							run(fixture.Select("SELECT v FROM rr_replaced_view", []string{"v"}, []string{"INT4"}, [][]any{{value}}))
							run(fixture.Select("SELECT r::oid='rr_replaced_view'::regclass::oid AS same FROM rr_replaced_owner", []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
							detail := oldDetail
							if replace {
								detail = "view rr_replaced_bridge depends on column v of table rr_replaced_owner\nview rr_replaced_view depends on view rr_replaced_bridge\ndefault value for column r of table rr_replaced_owner depends on view rr_replaced_view\nconstraint rr_replaced_bound on table rr_replaced_owner depends on view rr_replaced_view"
							}
							t.Run("current_restrict", func(t *testing.T) {
								regclassReplacementRestrict(t, p, c, record, prepared, target, detail)
							})
							run(fixture.Select("SELECT v,r FROM rr_replaced_owner", []string{"v", "r"}, []string{"INT4", "REGCLASS"}, [][]any{{int64(7), "rr_replaced_view"}}))
							notices = nil
							drop := fixture.Exec("ALTER TABLE "+target+" DROP COLUMN v CASCADE", 0)
							drop.Prepared = prepared
							run(drop)
							noticeDetail := "drop cascades to view rr_replaced_view\ndrop cascades to default value for column r of table rr_replaced_owner\ndrop cascades to constraint rr_replaced_bound on table rr_replaced_owner"
							count := 3
							if replace {
								count = 4
								noticeDetail = "drop cascades to view rr_replaced_bridge\n" + noticeDetail
							}
							fixture.DomainReferenceNotice(t, &notices, fmt.Sprintf("drop cascades to %d other objects", count), noticeDetail)
							run(fixture.RegclassRows("SELECT r FROM rr_replaced_owner", "r", strconv.FormatInt(oid, 10)))
							for _, query := range []string{
								"SELECT count(*) AS n FROM pg_attrdef WHERE adrelid='rr_replaced_owner'::regclass::oid",
								"SELECT count(*) AS n FROM pg_constraint WHERE conname='rr_replaced_bound'",
								"SELECT count(*) AS n FROM pg_class WHERE relname='rr_replaced_view'",
							} {
								run(fixture.Select(query, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
							}
							if replace {
								run(fixture.Exec("INSERT INTO rr_replaced_owner DEFAULT VALUES", 1))
								run(fixture.Exec("INSERT INTO rr_replaced_owner(r) VALUES(0::regclass)", 1))
								run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='rr_replaced_bridge'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
							} else {
								run(fixture.Exec("INSERT INTO rr_replaced_owner(v) VALUES(9)", 1))
								run(fixture.Exec("INSERT INTO rr_replaced_owner(v,r) VALUES(11,0::regclass)", 1))
								run(fixture.Select("SELECT v FROM rr_replaced_bridge ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}, {int64(11)}}))
							}
							run(fixture.RegclassRows("SELECT r FROM rr_replaced_owner WHERE r IS NULL", "r", nil))
							run(fixture.RegclassRows("SELECT r FROM rr_replaced_owner WHERE r::oid=0::oid", "r", "-"))
							run(fixture.RegclassRows(fmt.Sprintf("SELECT r FROM rr_replaced_owner WHERE r::oid=%d::oid", oid), "r", strconv.FormatInt(oid, 10)))
						})
					}
				})
			}
		}
	})
}

func regclassReplacementRestrict(t *testing.T, p *fixture.Pair, c *sql.Conn, record *fixture.Recorder, prepared bool, target, detail string) {
	t.Helper()
	s := fixture.ImplicitError("ALTER TABLE "+target+" DROP COLUMN v RESTRICT", fixture.Diagnostic{
		Code: "2BP01", Message: "cannot drop column v of table " + target + " because other objects depend on it",
		Detail: detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too.",
	})
	s.Prepared = prepared
	s.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, c, record, s)
}

func TestReferenceRegclassOpaqueInlineDefaultReplay(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, mixed := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%v/mixed=%v", prepared, mixed), func(t *testing.T) {
				p := fixture.NewScenario(t)
				replayed := false
				check := fixture.Select("SELECT v FROM rr_inline_owner", []string{"v"}, []string{"BOOL"}, [][]any{{true}})
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						var notices []fixture.Diagnostic
						_, c, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
						run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, record, s) }
						if !b.PG || !replayed {
							for _, query := range []string{
								"CREATE TABLE rr_inline_owner(v BOOL)",
								"CREATE TABLE rr_inline_target(n INT)",
								"ALTER TABLE rr_inline_owner ALTER COLUMN v SET DEFAULT ('{rr_inline_target}'::regclass[] IS NOT NULL)",
							} {
								run(fixture.Exec(query, 0))
							}
							run(fixture.Exec("INSERT INTO rr_inline_owner DEFAULT VALUES", 1))
							if mixed {
								run(fixture.Exec("CREATE TABLE rr_inline_trigger(r regclass DEFAULT 'rr_inline_target')", 0))
								run(fixture.Exec("INSERT INTO rr_inline_trigger DEFAULT VALUES", 1))
							}
						}
						run(check)
						if mixed {
							run(fixture.RegclassRows("SELECT r FROM rr_inline_trigger", "r", "rr_inline_target"))
						}
						if !b.PG {
							var dump bytes.Buffer
							if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
								t.Fatal(err)
							}
							fixture.ArrayReferenceNativeRoundtrip(t, p, c, b, check)
							target := fixture.NewFixture(t, false, true)
							fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE rr_inline_allocator(v INT)", 0))
							if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
								t.Fatal("fresh inline opaque default Restore", err)
							}
							freshRun := func(s fixture.Step) {
								s.Prepared = prepared
								fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s)
							}
							freshRun(check)
							freshRun(fixture.Exec("INSERT INTO rr_inline_owner DEFAULT VALUES", 1))
							future := check
							future.Rows = [][]any{{true}, {true}}
							freshRun(future)
							if mixed {
								freshRun(fixture.Exec("DROP TABLE rr_inline_trigger", 0))
							}
							freshRun(fixture.Exec("DROP TABLE rr_inline_target RESTRICT", 0))
							freshRun(fixture.Exec("INSERT INTO rr_inline_owner DEFAULT VALUES", 1))
							future.Rows = append(future.Rows, []any{true})
							freshRun(future)
							if p.Postgres != nil {
								if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
									t.Fatal("PostgreSQL replay of actual emitted SQL", err, dump.String())
								}
								replayed = true
							}
						}
						run(fixture.Exec("INSERT INTO rr_inline_owner DEFAULT VALUES", 1))
						future := check
						future.Rows = [][]any{{true}, {true}}
						run(future)
						if mixed {
							run(fixture.Exec("INSERT INTO rr_inline_trigger DEFAULT VALUES", 1))
							run(fixture.RegclassRows("SELECT r FROM rr_inline_trigger", "r", "rr_inline_target", "rr_inline_target"))
							run(fixture.Exec("DROP TABLE rr_inline_trigger", 0))
						}
						notices = nil
						run(fixture.Exec("DROP TABLE rr_inline_target RESTRICT", 0))
						if len(notices) != 0 {
							t.Fatal("opaque default gained DROP dependency notices", notices)
						}
						run(fixture.Select("SELECT count(*) AS n FROM pg_attrdef WHERE adrelid='rr_inline_owner'::regclass::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
						run(fixture.Exec("INSERT INTO rr_inline_owner DEFAULT VALUES", 1))
						future.Rows = append(future.Rows, []any{true})
						run(future)
						run(fixture.Exec("DROP TABLE rr_inline_owner", 0))
					})
				}
			})
		}
	}
}
