package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSepgsqlSimulationDemands(t *testing.T) {
	t.Run("ordinary_acl_is_independent", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"sepgsql_demand_reader"})
		setup := func(run sepgsqlRun) {
			sepgsqlTable(run)
			run(fixture.Exec("CREATE ROLE sepgsql_demand_reader", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO sepgsql_demand_reader", 0))
		}
		sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			setup(run)
			run(fixture.Exec("SET ROLE sepgsql_demand_reader", 0))
			run(fixture.ImplicitError("SELECT id FROM sepgsql_items", fixture.Diagnostic{Code: "42501", Message: "permission denied for table sepgsql_items"}))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT SELECT ON sepgsql_items TO sepgsql_demand_reader", 0))
			run(fixture.Exec("SET ROLE sepgsql_demand_reader", 0))
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_items", 2))
			run(fixture.Exec("RESET ROLE", 0))
		}, nil, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			setup(run)
			run(fixture.Exec("SECURITY LABEL ON TABLE sepgsql_items IS '"+sepgsqlS+"'", 0))
			run(fixture.Exec("SET ROLE sepgsql_demand_reader", 0))
			run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlA))
			run(fixture.ImplicitError("SELECT id FROM sepgsql_items", fixture.Diagnostic{Code: "42501", Message: "permission denied for table sepgsql_items"}))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT SELECT ON sepgsql_items TO sepgsql_demand_reader", 0))
			run(fixture.Exec("SET ROLE sepgsql_demand_reader", 0))
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_items", 2))
			run(fixture.Exec("RESET ROLE", 0))
			run(sepgsqlSet(sepgsqlR))
			run(sepgsqlDenied("SELECT id FROM sepgsql_items"))
			run(sepgsqlText("SELECT current_user::text AS actor", "actor", "postgres"))
		})
	})

	t.Run("rowmark_column_acl_and_independent_mac", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"sepgsql_lock_reader"})
		setup := func(run sepgsqlRun) {
			sepgsqlTable(run)
			for _, query := range []string{
				"CREATE ROLE sepgsql_lock_reader",
				"GRANT USAGE ON SCHEMA public TO sepgsql_lock_reader",
				"GRANT SELECT(id),UPDATE(v) ON sepgsql_items TO sepgsql_lock_reader",
				"CREATE VIEW sepgsql_lock_owner AS SELECT id FROM sepgsql_items FOR UPDATE",
				"CREATE VIEW sepgsql_lock_invoker WITH(security_invoker=true) AS SELECT id FROM sepgsql_items FOR UPDATE",
				"CREATE VIEW sepgsql_lock_ctid AS SELECT ctid::text AS value FROM sepgsql_items FOR UPDATE",
				"GRANT SELECT,UPDATE ON sepgsql_lock_owner,sepgsql_lock_invoker,sepgsql_lock_ctid TO sepgsql_lock_reader",
				"CREATE TABLE sepgsql_lock_parent(id integer)",
				"CREATE TABLE sepgsql_lock_child(extra text) INHERITS (sepgsql_lock_parent)",
				"GRANT SELECT(id),UPDATE(id) ON sepgsql_lock_parent TO sepgsql_lock_reader",
			} {
				run(fixture.Exec(query, 0))
			}
		}
		reads := func(run sepgsqlRun) {
			for _, clause := range fixture.RowClauses {
				for _, prepared := range []bool{false, true} {
					for _, relation := range []string{"sepgsql_items", "sepgsql_lock_owner", "sepgsql_lock_invoker"} {
						step := fixture.Select("SELECT id FROM "+relation+" ORDER BY id "+clause, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
						step.Prepared = prepared
						run(step)
					}
					step := fixture.Select("SELECT 1 AS marker FROM sepgsql_items WHERE false "+clause, []string{"marker"}, []string{"INT4"}, nil)
					step.Prepared = prepared
					run(step)
					for _, selected := range []string{"v", "tableoid", "sepgsql_items"} {
						denied := fixture.ImplicitError("SELECT "+selected+" FROM sepgsql_items WHERE false "+clause, fixture.Diagnostic{Code: "42501", Message: "permission denied for table sepgsql_items"})
						denied.SelectRows, denied.ErrorStage, denied.Prepared = true, "execute", prepared
						run(denied)
					}
				}
			}
		}
		sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			setup(run)
			run(fixture.Exec("SET ROLE sepgsql_lock_reader", 0))
			reads(run)
			run(fixture.Exec("RESET ROLE", 0))
		}, func(config *stuntgres.SepgsqlSimulationConfig) {
			// A secret table can be read but not locked. No table/column
			// UPDATE MAC is granted: SQL rowmarks have empty updatedCols.
			config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlS, Class: "db_table", Allow: []string{"select"}})
		}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			setup(run)
			run(fixture.Exec("SECURITY LABEL ON COLUMN sepgsql_items.v IS '"+sepgsqlS+"'", 0))
			run(fixture.Exec("SECURITY LABEL ON COLUMN sepgsql_lock_child.extra IS '"+sepgsqlS+"'", 0))
			run(sepgsqlSet(sepgsqlR))
			run(fixture.Exec("SET ROLE sepgsql_lock_reader", 0))
			reads(run)
			run(fixture.Exec("RESET ROLE", 0))
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			run(fixture.Exec("GRANT SELECT ON sepgsql_items TO sepgsql_lock_reader", 0))
			run(fixture.Exec("SECURITY LABEL ON COLUMN sepgsql_items.ctid IS '"+sepgsqlS+"'", 0))
			run(sepgsqlSet(sepgsqlR))
			run(fixture.Exec("SET ROLE sepgsql_lock_reader", 0))
			for _, prepared := range []bool{false, true} {
				for _, query := range []string{
					"SELECT v FROM sepgsql_items WHERE false FOR UPDATE",
					"SELECT sepgsql_items FROM sepgsql_items WHERE false FOR UPDATE",
					"SELECT ctid::text AS value FROM sepgsql_items WHERE false FOR UPDATE",
					"SELECT value FROM sepgsql_lock_ctid WHERE false FOR UPDATE",
				} {
					step := sepgsqlDenied(query)
					step.SelectRows, step.ErrorStage, step.Prepared = true, "execute", prepared
					run(step)
				}
				// A different selected signed attribute and a no-column
				// rowmark do not acquire the denied ctid/v demands.
				for _, step := range []fixture.Step{
					fixture.Select("SELECT xmin::text AS value FROM sepgsql_items WHERE false FOR UPDATE", []string{"value"}, []string{"TEXT"}, nil),
					fixture.Select("SELECT 1 AS marker FROM sepgsql_items WHERE false FOR UPDATE", []string{"marker"}, []string{"INT4"}, nil),
				} {
					step.Prepared = prepared
					run(step)
				}
			}
			run(fixture.Exec("RESET ROLE", 0))
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			run(fixture.Exec("SECURITY LABEL ON TABLE sepgsql_items IS '"+sepgsqlS+"'", 0))
			run(fixture.Exec("SECURITY LABEL ON TABLE sepgsql_lock_child IS '"+sepgsqlS+"'", 0))
			run(sepgsqlSet(sepgsqlR))
			run(fixture.Exec("SET ROLE sepgsql_lock_reader", 0))
			run(fixture.Select("SELECT id FROM sepgsql_items ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			run(fixture.Select("SELECT id FROM sepgsql_lock_parent WHERE false", []string{"id"}, []string{"INT4"}, nil))
			for _, prepared := range []bool{false, true} {
				for _, query := range []string{
					"SELECT id FROM sepgsql_items WHERE false FOR UPDATE",
					"SELECT 1 FROM sepgsql_items WHERE false FOR UPDATE",
					"SELECT id FROM sepgsql_lock_owner WHERE false FOR UPDATE",
					"SELECT id FROM sepgsql_lock_invoker WHERE false FOR UPDATE",
					"SELECT id FROM sepgsql_lock_parent WHERE false FOR UPDATE",
				} {
					step := sepgsqlDenied(query)
					step.SelectRows, step.ErrorStage, step.Prepared = true, "execute", prepared
					run(step)
				}
				step := fixture.Select("SELECT id FROM ONLY sepgsql_lock_parent WHERE false FOR UPDATE", []string{"id"}, []string{"INT4"}, nil)
				step.Prepared = prepared
				run(step)
			}
			run(fixture.Exec("RESET ROLE", 0))
		})
	})

	t.Run("real_columns_and_empty_inheritance", func(t *testing.T) {
		setup := func(run sepgsqlRun) {
			sepgsqlTable(run)
			run(fixture.Exec("CREATE TABLE sepgsql_parent(id integer)", 0))
			run(fixture.Exec("CREATE TABLE sepgsql_child(lead text,id integer)", 0))
			run(fixture.Exec("ALTER TABLE sepgsql_child INHERIT sepgsql_parent", 0))
			run(fixture.Exec("CREATE VIEW sepgsql_whole_view AS SELECT sepgsql_parent AS value FROM sepgsql_parent", 0))
			run(fixture.Exec("CREATE VIEW sepgsql_column_view AS SELECT sepgsql_parent.* FROM sepgsql_parent", 0))
		}
		sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			setup(run)
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_items", 2))
			run(sepgsqlCount("SELECT count(v) AS count FROM sepgsql_items", 2))
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_parent WHERE false", 0))
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_whole_view", 0))
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_column_view", 0))
			run(fixture.Select("SELECT id FROM sepgsql_items ORDER BY id FOR UPDATE", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		}, nil, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			setup(run)
			run(fixture.Exec("SECURITY LABEL ON COLUMN sepgsql_items.v IS '"+sepgsqlS+"'", 0))
			run(fixture.Exec("SECURITY LABEL ON TABLE sepgsql_child IS '"+sepgsqlS+"'", 0))
			run(sepgsqlSet(sepgsqlR))
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_items", 2))
			for _, query := range []string{
				"SELECT count(v) AS count FROM sepgsql_items",
				"SELECT sepgsql_items FROM sepgsql_items",
				"SELECT id FROM sepgsql_items WHERE v='absent'",
				"SELECT count(*) FROM sepgsql_parent WHERE false",
			} {
				run(sepgsqlDenied(query))
			}
			run(sepgsqlCount("SELECT count(*) FROM ONLY sepgsql_parent WHERE false", 0))
			run(fixture.Select("SELECT id FROM sepgsql_items ORDER BY id FOR UPDATE", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			run(fixture.Exec("SECURITY LABEL ON TABLE sepgsql_child IS '"+sepgsqlP+"'", 0))
			run(fixture.Exec("SECURITY LABEL ON COLUMN sepgsql_child.lead IS '"+sepgsqlS+"'", 0))
			run(sepgsqlSet(sepgsqlR))
			run(fixture.Select("SELECT id FROM sepgsql_parent WHERE false", []string{"id"}, []string{"INT4"}, nil))
			run(fixture.Prepared(fixture.Select("SELECT sepgsql_parent.* FROM sepgsql_parent WHERE false", []string{"id"}, []string{"INT4"}, nil)))
			run(sepgsqlDenied("SELECT sepgsql_parent FROM sepgsql_parent WHERE false"))
			run(fixture.Prepared(sepgsqlDenied("SELECT sepgsql_parent FROM sepgsql_parent WHERE false")))
			run(sepgsqlDenied("SELECT value::text FROM sepgsql_whole_view WHERE false"))
			run(fixture.Select("SELECT id FROM sepgsql_column_view WHERE false", []string{"id"}, []string{"INT4"}, nil))
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			run(fixture.Exec("SECURITY LABEL ON COLUMN sepgsql_child.id IS '"+sepgsqlS+"'", 0))
			run(sepgsqlSet(sepgsqlR))
			run(sepgsqlDenied("SELECT id FROM sepgsql_parent WHERE false"))
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_parent", 0))
		})
	})

	t.Run("exact_system_columns_are_not_whole_row", func(t *testing.T) {
		attributes := []string{"ctid", "xmin", "xmax", "tableoid"}
		setup := func(run sepgsqlRun) {
			run(fixture.Exec("CREATE TABLE sepgsql_system_parent(id integer)", 0))
			run(fixture.Exec("CREATE TABLE sepgsql_system_child(extra text) INHERITS (sepgsql_system_parent)", 0))
			for _, attribute := range attributes {
				run(fixture.Exec("CREATE VIEW sepgsql_system_"+attribute+" AS SELECT "+attribute+"::text AS value FROM sepgsql_system_parent", 0))
			}
		}
		empty := func(query string) fixture.Step {
			return fixture.Select(query, []string{"value"}, []string{"TEXT"}, nil)
		}
		sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			setup(run)
			for _, attribute := range attributes {
				query := "SELECT " + attribute + "::text AS value FROM sepgsql_system_parent WHERE false"
				run(empty(query))
				run(fixture.Prepared(empty(query)))
				run(empty("SELECT value FROM sepgsql_system_" + attribute + " WHERE false"))
			}
		}, nil, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			setup(run)
			for _, table := range []string{"sepgsql_system_parent", "sepgsql_system_child"} {
				run(fixture.Exec("SECURITY LABEL ON TABLE "+table+" IS '"+sepgsqlP+"'", 0))
				run(fixture.Exec("SECURITY LABEL ON COLUMN "+table+".id IS '"+sepgsqlS+"'", 0))
				for _, attribute := range attributes {
					run(fixture.Exec("SECURITY LABEL ON COLUMN "+table+"."+attribute+" IS '"+sepgsqlS+"'", 0))
				}
			}
			run(fixture.Exec("SECURITY LABEL ON COLUMN sepgsql_system_child.extra IS '"+sepgsqlS+"'", 0))
			for _, attribute := range attributes {
				query := "SELECT " + attribute + "::text AS value FROM sepgsql_system_parent WHERE false"
				viewQuery := "SELECT value FROM sepgsql_system_" + attribute + " WHERE false"
				run(sepgsqlSet(sepgsqlR))
				run(sepgsqlDenied(query))
				run(fixture.Prepared(sepgsqlDenied(viewQuery)))
				run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
				run(fixture.Exec("SECURITY LABEL ON COLUMN sepgsql_system_parent."+attribute+" IS '"+sepgsqlP+"'", 0))
				run(sepgsqlSet(sepgsqlR))
				run(empty("SELECT " + attribute + "::text AS value FROM ONLY sepgsql_system_parent WHERE false"))
				run(sepgsqlDenied(query)) // The empty inherited child still denies its exact system column.
				run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
				run(fixture.Exec("SECURITY LABEL ON COLUMN sepgsql_system_child."+attribute+" IS '"+sepgsqlP+"'", 0))
				run(sepgsqlSet(sepgsqlR))
				run(empty(query))
				run(fixture.Prepared(empty(query)))
				run(empty(viewQuery)) // Definer expansion retains the invoker's MAC subject.
				run(fixture.Prepared(empty(viewQuery)))
				run(sepgsqlDenied("SELECT sepgsql_system_parent FROM sepgsql_system_parent WHERE false"))
				run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
				for _, table := range []string{"sepgsql_system_parent", "sepgsql_system_child"} {
					run(fixture.Exec("SECURITY LABEL ON COLUMN "+table+"."+attribute+" IS '"+sepgsqlS+"'", 0))
				}
			}
		})
	})

	t.Run("truncate_cascade_checks_empty_dependents", func(t *testing.T) {
		setup := func(run sepgsqlRun) {
			run(fixture.Exec("CREATE TABLE sepgsql_truncate_parent(id integer PRIMARY KEY)", 0))
			run(fixture.Exec("CREATE TABLE sepgsql_truncate_child(id integer REFERENCES sepgsql_truncate_parent)", 0))
			run(fixture.Exec("INSERT INTO sepgsql_truncate_parent VALUES(7)", 1))
		}
		sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			setup(run)
			run(fixture.Exec("TRUNCATE sepgsql_truncate_parent CASCADE", 0))
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_truncate_parent", 0))
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_truncate_child", 0))
		}, func(config *stuntgres.SepgsqlSimulationConfig) {
			config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_table", Allow: []string{"truncate"}})
		}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			setup(run)
			run(fixture.Exec("SECURITY LABEL ON TABLE sepgsql_truncate_child IS '"+sepgsqlS+"'", 0))
			run(sepgsqlSet(sepgsqlR))
			run(sepgsqlDenied("TRUNCATE sepgsql_truncate_parent CASCADE"))
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_truncate_parent", 1))
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_truncate_child", 0))
			run(fixture.Exec("SECURITY LABEL ON TABLE sepgsql_truncate_child IS '"+sepgsqlP+"'", 0))
			run(sepgsqlSet(sepgsqlR))
			run(fixture.Exec("TRUNCATE sepgsql_truncate_parent CASCADE", 0))
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_truncate_parent", 0))
		})
	})
}
