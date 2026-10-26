package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Each serial family gets its own bounded scenario, with setup before event
// installation. Every checked command has independently specified canonical tags.
func TestReferenceEventTriggerEligibility(t *testing.T) {
	t.Run("drop_owned", eventTriggerOwnedEligibility)
	for _, family := range []struct {
		name     string
		setup    []string
		commands []struct {
			sql, tag string
			affected int64
		}
	}{
		{"relations", []string{"CREATE TABLE et_base(id integer)", "CREATE VIEW et_view AS SELECT id FROM et_base", "CREATE MATERIALIZED VIEW et_mat AS SELECT id FROM et_base", "CREATE UNIQUE INDEX et_mat_unique ON et_mat(id)"}, []struct {
			sql, tag string
			affected int64
		}{
			{"CREATE TABLE et_ctas AS SELECT id FROM et_base", "CREATE TABLE AS", 0},
			{"SELECT id INTO et_into FROM et_base", "SELECT INTO", 0},
			{"ALTER TABLE et_base ADD COLUMN extra text", "ALTER TABLE", 0},
			{"ALTER TABLE et_base RENAME COLUMN extra TO renamed", "ALTER TABLE", 0},
			{"ALTER TABLE et_base RENAME TO et_renamed", "ALTER TABLE", 0},
			{"ALTER VIEW et_view RENAME TO et_view_new", "ALTER VIEW", 0},
			{"ALTER MATERIALIZED VIEW et_mat RENAME TO et_mat_new", "ALTER MATERIALIZED VIEW", 0},
			{"REFRESH MATERIALIZED VIEW et_mat_new", "REFRESH MATERIALIZED VIEW", 0},
			{"REFRESH MATERIALIZED VIEW CONCURRENTLY et_mat_new", "REFRESH MATERIALIZED VIEW", 0},
			{"DROP VIEW et_view_new", "DROP VIEW", 0},
			{"DROP MATERIALIZED VIEW et_mat_new", "DROP MATERIALIZED VIEW", 0},
		}},
		{"schema_types", []string{"CREATE TYPE et_enum AS ENUM ('a')", "CREATE TYPE et_comp AS (id integer)", "CREATE DOMAIN et_domain AS integer"}, []struct {
			sql, tag string
			affected int64
		}{
			{"CREATE SCHEMA et_scope", "CREATE SCHEMA", 0},
			{"ALTER SCHEMA et_scope RENAME TO et_scope_new", "ALTER SCHEMA", 0},
			{"DROP SCHEMA et_scope_new", "DROP SCHEMA", 0},
			{"CREATE TYPE et_range AS RANGE(subtype=integer)", "CREATE TYPE", 0},
			{"ALTER TYPE et_enum ADD VALUE 'b'", "ALTER TYPE", 0},
			{"ALTER TYPE et_comp ADD ATTRIBUTE label text", "ALTER TYPE", 0},
			{"ALTER DOMAIN et_domain SET DEFAULT 7", "ALTER DOMAIN", 0},
			{"DROP DOMAIN et_domain", "DROP DOMAIN", 0},
			{"DROP TYPE et_comp", "DROP TYPE", 0},
		}},
		{"sequence_index", []string{"CREATE TABLE et_base(id integer)"}, []struct {
			sql, tag string
			affected int64
		}{
			{"CREATE SEQUENCE et_seq", "CREATE SEQUENCE", 0},
			{"ALTER SEQUENCE et_seq INCREMENT BY 2", "ALTER SEQUENCE", 0},
			{"COMMENT ON SEQUENCE et_seq IS 'event witness'", "COMMENT", 0},
			{"DROP SEQUENCE et_seq", "DROP SEQUENCE", 0},
			{"CREATE INDEX et_idx ON et_base(id)", "CREATE INDEX", 0},
			{"ALTER INDEX et_idx RENAME TO et_idx_new", "ALTER INDEX", 0},
			{"DROP INDEX et_idx_new", "DROP INDEX", 0},
		}},
		{"routine_operator", []string{"CREATE FUNCTION et_int(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$"}, []struct {
			sql, tag string
			affected int64
		}{
			{"CREATE FUNCTION et_sum(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$", "CREATE FUNCTION", 0},
			{"CREATE OR REPLACE FUNCTION et_int(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$", "CREATE FUNCTION", 0},
			{"ALTER FUNCTION et_int(integer) RENAME TO et_int_new", "ALTER FUNCTION", 0},
			{"CREATE AGGREGATE et_total(integer) (SFUNC=et_sum,STYPE=integer,INITCOND='0')", "CREATE AGGREGATE", 0},
			{"DROP AGGREGATE et_total(integer)", "DROP AGGREGATE", 0},
			{"CREATE OPERATOR public.#~# (LEFTARG=integer,RIGHTARG=integer,FUNCTION=pg_catalog.int4pl)", "CREATE OPERATOR", 0},
			{"DROP OPERATOR public.#~# (integer,integer)", "DROP OPERATOR", 0},
			{"CREATE OPERATOR FAMILY et_family USING btree", "CREATE OPERATOR FAMILY", 0},
			{"CREATE OPERATOR CLASS et_class FOR TYPE integer USING btree FAMILY et_family AS OPERATOR 1 < (integer,integer),OPERATOR 2 <= (integer,integer),OPERATOR 3 = (integer,integer),OPERATOR 4 >= (integer,integer),OPERATOR 5 > (integer,integer),FUNCTION 1 btint4cmp(integer,integer)", "CREATE OPERATOR CLASS", 0},
			{"DROP OPERATOR CLASS et_class USING btree", "DROP OPERATOR CLASS", 0},
			{"DROP OPERATOR FAMILY et_family USING btree", "DROP OPERATOR FAMILY", 0},
			{"DROP FUNCTION et_int_new(integer)", "DROP FUNCTION", 0},
		}},
		{"collation_conversion_cast", []string{"CREATE TYPE et_cast_src AS ENUM('red')", "CREATE TYPE et_cast_dst AS ENUM('red')"}, []struct {
			sql, tag string
			affected int64
		}{
			{`CREATE COLLATION et_coll FROM pg_catalog."C"`, "CREATE COLLATION", 0},
			{"ALTER COLLATION et_coll RENAME TO et_coll_new", "ALTER COLLATION", 0},
			{"DROP COLLATION et_coll_new", "DROP COLLATION", 0},
			{"CREATE CONVERSION et_conv FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1", "CREATE CONVERSION", 0},
			{"ALTER CONVERSION et_conv RENAME TO et_conv_new", "ALTER CONVERSION", 0},
			{"DROP CONVERSION et_conv_new", "DROP CONVERSION", 0},
			{"CREATE CAST (et_cast_src AS et_cast_dst) WITH INOUT", "CREATE CAST", 0},
			{"DROP CAST (et_cast_src AS et_cast_dst)", "DROP CAST", 0},
		}},
		{"trigger_rule_policy", []string{"CREATE TABLE et_base(id integer)", "CREATE FUNCTION et_row() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$"}, []struct {
			sql, tag string
			affected int64
		}{
			{"CREATE TRIGGER et_row BEFORE INSERT ON et_base FOR EACH ROW EXECUTE FUNCTION et_row()", "CREATE TRIGGER", 0},
			{"ALTER TRIGGER et_row ON et_base RENAME TO et_row_new", "ALTER TRIGGER", 0},
			{"DROP TRIGGER et_row_new ON et_base", "DROP TRIGGER", 0},
			{"CREATE RULE et_rule AS ON INSERT TO et_base DO ALSO NOTHING", "CREATE RULE", 0},
			{"ALTER RULE et_rule ON et_base RENAME TO et_rule_new", "ALTER RULE", 0},
			{"DROP RULE et_rule_new ON et_base", "DROP RULE", 0},
			{"CREATE POLICY et_policy ON et_base USING(true)", "CREATE POLICY", 0},
			{"ALTER POLICY et_policy ON et_base USING(id>0)", "ALTER POLICY", 0},
			{"DROP POLICY et_policy ON et_base", "DROP POLICY", 0},
		}},
		{"local_privileges", []string{"CREATE TABLE et_base(id integer)"}, []struct {
			sql, tag string
			affected int64
		}{
			{"COMMENT ON TABLE et_base IS 'local'", "COMMENT", 0},
			{"GRANT SELECT ON et_base TO PUBLIC", "GRANT", 0},
			{"REVOKE SELECT ON et_base FROM PUBLIC", "REVOKE", 0},
			{"ALTER TABLE et_base OWNER TO CURRENT_USER", "ALTER TABLE", 0},
			{"ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO PUBLIC", "ALTER DEFAULT PRIVILEGES", 0},
			{"ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM PUBLIC", "ALTER DEFAULT PRIVILEGES", 0},
		}},
	} {
		t.Run(family.name, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "et_scope")
			fixture.SchemaReferenceCleanup(t, "et_scope_new")
			eventTriggerScenario(t, []string{"et_eligible_start", "et_eligible_end"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for _, q := range family.setup {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
				}
				eventTriggerAuditSetup(t, p, b, c)
				eventTriggerFunction(t, p, b, c, "et_eligible_start", 1, "RETURN;")
				eventTriggerFunction(t, p, b, c, "et_eligible_end", 2, "RETURN;")
				eventTriggerCreate(t, p, b, c, "et_eligible_start", "ddl_command_start", "", "et_eligible_start")
				eventTriggerCreate(t, p, b, c, "et_eligible_end", "ddl_command_end", "", "et_eligible_end")
				resetFilter := func(tags string) {
					for _, name := range []string{"et_eligible_start", "et_eligible_end"} {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP EVENT TRIGGER "+name, 0))
					}
					eventTriggerCreate(t, p, b, c, "et_eligible_start", "ddl_command_start", tags, "et_eligible_start")
					eventTriggerCreate(t, p, b, c, "et_eligible_end", "ddl_command_end", tags, "et_eligible_end")
				}
				for _, command := range family.commands {
					if !t.Run(command.sql, func(t *testing.T) {
						concurrentRefresh := strings.HasPrefix(command.sql, "REFRESH MATERIALIZED VIEW CONCURRENTLY ")
						if concurrentRefresh {
							resetFilter("'REFRESH MATERIALIZED VIEW'")
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(command.sql, command.affected))
						fixture.RunStep(t, p.Ctx, c, b.Recorder, eventTriggerAudit(eventTriggerRow(1, "et_eligible_start", "ddl_command_start", command.tag), eventTriggerRow(2, "et_eligible_end", "ddl_command_end", command.tag)))
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM et_audit", 2))
						if concurrentRefresh {
							resetFilter("")
						}
					}) {
						return
					}
				}
			})
		})
	}
	t.Run("foreign_extension_access_method", eventTriggerForeignEligibility)
	t.Run("exclusions", eventTriggerExclusions)
}

func eventTriggerForeignEligibility(t *testing.T) {
	eventTriggerScenario(t, []string{"et_foreign"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		t.Cleanup(func() {
			eventTriggerCleanup(t, b.DB, []string{"et_foreign"})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, q := range []string{"DROP FOREIGN DATA WRAPPER IF EXISTS et_fdw CASCADE", "DROP ACCESS METHOD IF EXISTS et_am CASCADE"} {
				if _, err := b.DB.ExecContext(ctx, q); err != nil {
					t.Error(q, err)
				}
			}
		})
		eventTriggerAuditSetup(t, p, b, c)
		eventTriggerFunction(t, p, b, c, "et_foreign", 1, "RETURN;")
		eventTriggerCreate(t, p, b, c, "et_foreign", "ddl_command_start", "'CREATE FOREIGN DATA WRAPPER','CREATE SERVER','CREATE USER MAPPING','CREATE FOREIGN TABLE','ALTER FOREIGN TABLE','DROP FOREIGN TABLE','DROP USER MAPPING','DROP SERVER','DROP FOREIGN DATA WRAPPER','CREATE EXTENSION','DROP EXTENSION','CREATE ACCESS METHOD','DROP ACCESS METHOD'", "et_foreign")
		for _, tc := range []struct{ q, tag string }{
			{"CREATE FOREIGN DATA WRAPPER et_fdw NO HANDLER NO VALIDATOR", "CREATE FOREIGN DATA WRAPPER"},
			{"CREATE SERVER et_server FOREIGN DATA WRAPPER et_fdw", "CREATE SERVER"},
			{"CREATE USER MAPPING FOR CURRENT_USER SERVER et_server", "CREATE USER MAPPING"},
			{"CREATE FOREIGN TABLE et_ft(id integer) SERVER et_server", "CREATE FOREIGN TABLE"},
			{"ALTER FOREIGN TABLE et_ft ADD COLUMN v text", "ALTER FOREIGN TABLE"},
			{"DROP FOREIGN TABLE et_ft", "DROP FOREIGN TABLE"},
			{"DROP USER MAPPING FOR CURRENT_USER SERVER et_server", "DROP USER MAPPING"},
			{"DROP SERVER et_server", "DROP SERVER"},
			{"DROP FOREIGN DATA WRAPPER et_fdw", "DROP FOREIGN DATA WRAPPER"},
			{"CREATE EXTENSION unaccent", "CREATE EXTENSION"},
			{"DROP EXTENSION unaccent", "DROP EXTENSION"},
			{"CREATE ACCESS METHOD et_am TYPE TABLE HANDLER heap_tableam_handler", "CREATE ACCESS METHOD"},
			{"DROP ACCESS METHOD et_am", "DROP ACCESS METHOD"},
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(tc.q, 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, eventTriggerAudit(eventTriggerRow(1, "et_foreign", "ddl_command_start", tc.tag)))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM et_audit", 1))
		}
	})
}

func eventTriggerExclusions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"et_shared"})
	eventTriggerScenario(t, []string{"et_excluded", "et_excluded_other"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		eventTriggerAuditSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE et_plain(id integer)", 0))
		eventTriggerFunction(t, p, b, c, "et_excluded_fn", 1, "RETURN;")
		eventTriggerCreate(t, p, b, c, "et_excluded", "ddl_command_start", "", "et_excluded_fn")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE ROLE et_shared", "ALTER ROLE et_shared NOLOGIN", "COMMENT ON ROLE et_shared IS 'shared'",
			"GRANT et_shared TO CURRENT_USER", "REVOKE et_shared FROM CURRENT_USER",
			"GRANT SET ON PARAMETER application_name TO et_shared", "REVOKE SET ON PARAMETER application_name FROM et_shared",
			"DROP ROLE et_shared",
			"CREATE EVENT TRIGGER et_excluded_other ON ddl_command_end EXECUTE FUNCTION et_excluded_fn()",
			"ALTER EVENT TRIGGER et_excluded_other DISABLE", "ALTER EVENT TRIGGER et_excluded_other ENABLE", "DROP EVENT TRIGGER et_excluded_other",
			"BEGIN", "SAVEPOINT et_save", "ROLLBACK TO et_save", "RELEASE et_save", "LOCK TABLE et_plain IN ACCESS SHARE MODE", "COMMIT",
			"DO $$BEGIN END$$", "SET application_name='et'", "RESET application_name", "LISTEN et_channel", "NOTIFY et_channel", "UNLISTEN et_channel",
			"TRUNCATE et_plain", "VACUUM et_plain", "PREPARE et_read AS SELECT id FROM et_plain", "DEALLOCATE et_read",
		} {
			run(fixture.Exec(q, 0))
			run(eventTriggerAudit())
		}
		run(fixture.Exec("INSERT INTO et_plain VALUES(1)", 1))
		run(fixture.Exec("UPDATE et_plain SET id=2", 1))
		run(fixture.Select("SELECT id FROM et_plain", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
		run(fixture.Exec("DELETE FROM et_plain", 1))
		run(eventTriggerAudit())
	})
}
