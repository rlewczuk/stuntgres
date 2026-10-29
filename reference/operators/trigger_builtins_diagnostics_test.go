package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerBuiltinsDiagnostics(t *testing.T) {
	// All primary/code expectations are independently specified from REL_18_6.
	// Empty optional fields are assertions, subject to pinned-oracle discovery.
	for _, tc := range []struct{ name, function, clause, args, query, code, message string }{
		{"suppress_direct", "suppress_redundant_updates_trigger", "", "", "SELECT pg_catalog.suppress_redundant_updates_trigger()", "39P01", "suppress_redundant_updates_trigger: must be called as trigger"},
		{"explicit_direct", "tsvector_update_trigger", "", "", "SELECT pg_catalog.tsvector_update_trigger()", "XX000", "tsvector_update_trigger: not fired by trigger manager"},
		{"row_direct", "tsvector_update_trigger_column", "", "", "SELECT pg_catalog.tsvector_update_trigger_column()", "XX000", "tsvector_update_trigger: not fired by trigger manager"},
		{"suppress_insert", "suppress_redundant_updates_trigger", "BEFORE INSERT FOR EACH ROW", "", "INSERT INTO tb_diag(id) VALUES(2)", "39P01", "suppress_redundant_updates_trigger: must be called on update"},
		{"suppress_delete", "suppress_redundant_updates_trigger", "BEFORE DELETE FOR EACH ROW", "", "DELETE FROM tb_diag", "39P01", "suppress_redundant_updates_trigger: must be called on update"},
		{"suppress_after", "suppress_redundant_updates_trigger", "AFTER UPDATE FOR EACH ROW", "", "UPDATE tb_diag SET id=id", "39P01", "suppress_redundant_updates_trigger: must be called before update"},
		{"suppress_statement", "suppress_redundant_updates_trigger", "AFTER UPDATE FOR EACH STATEMENT", "", "UPDATE tb_diag SET id=id", "39P01", "suppress_redundant_updates_trigger: must be called before update"},
		{"vector_statement", "tsvector_update_trigger", "AFTER UPDATE FOR EACH STATEMENT", "", "UPDATE tb_diag SET id=id", "XX000", "tsvector_update_trigger: must be fired for row"},
		{"vector_after", "tsvector_update_trigger", "AFTER UPDATE FOR EACH ROW", "", "UPDATE tb_diag SET id=id", "XX000", "tsvector_update_trigger: must be fired BEFORE event"},
		{"vector_delete", "tsvector_update_trigger", "BEFORE DELETE FOR EACH ROW", "", "DELETE FROM tb_diag", "XX000", "tsvector_update_trigger: must be fired for INSERT or UPDATE"},
		{"arguments", "tsvector_update_trigger", "BEFORE UPDATE FOR EACH ROW", "'missing','simple'", "UPDATE tb_diag SET id=id", "XX000", "tsvector_update_trigger: arguments must be tsvector_field, ts_config, text_field1, ...)"},
		{"destination_missing", "tsvector_update_trigger", "BEFORE UPDATE FOR EACH ROW", "'missing','simple','absent'", "UPDATE tb_diag SET id=id", "42703", `tsvector column "missing" does not exist`},
		{"destination_type", "tsvector_update_trigger", "BEFORE UPDATE FOR EACH ROW", "'id','simple','absent'", "UPDATE tb_diag SET id=id", "42804", `column "id" is not of tsvector type`},
		{"config_unqualified", "tsvector_update_trigger", "BEFORE UPDATE FOR EACH ROW", "'v','simple','absent'", "UPDATE tb_diag SET id=id", "22023", `text search configuration name "simple" must be schema-qualified`},
		{"config_schema", "tsvector_update_trigger", "BEFORE UPDATE FOR EACH ROW", "'v','tb_absent_schema.simple','title'", "UPDATE tb_diag SET id=id", "3F000", `schema "tb_absent_schema" does not exist`},
		{"config_missing", "tsvector_update_trigger", "BEFORE UPDATE FOR EACH ROW", "'v','public.tb_absent_config','title'", "UPDATE tb_diag SET id=id", "42704", `text search configuration "public.tb_absent_config" does not exist`},
		{"config_column_missing", "tsvector_update_trigger_column", "BEFORE UPDATE FOR EACH ROW", "'v','absent','missing'", "UPDATE tb_diag SET id=id", "42703", `configuration column "absent" does not exist`},
		{"config_column_type", "tsvector_update_trigger_column", "BEFORE UPDATE FOR EACH ROW", "'v','big','missing'", "UPDATE tb_diag SET id=id", "42804", `column "big" is not of regconfig type`},
		{"config_column_null", "tsvector_update_trigger_column", "BEFORE UPDATE FOR EACH ROW", "'v','cfg','missing'", "UPDATE tb_diag SET id=id", "22004", `configuration column "cfg" must not be null`},
		{"source_missing", "tsvector_update_trigger", "BEFORE UPDATE FOR EACH ROW", "'v','pg_catalog.simple','absent','id'", "UPDATE tb_diag SET id=id", "42703", `column "absent" does not exist`},
		{"source_type", "tsvector_update_trigger", "BEFORE UPDATE FOR EACH ROW", "'v','pg_catalog.simple','id','absent'", "UPDATE tb_diag SET id=id", "42804", `column "id" is not of a character type`},
		{"bpchar_source", "tsvector_update_trigger", "BEFORE UPDATE FOR EACH ROW", "'v','pg_catalog.simple','fixed'", "UPDATE tb_diag SET id=id", "42804", `column "fixed" is not of a character type`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				userOperatorExec(t, p, b, c, "CREATE TABLE tb_diag(id integer,title text,v tsvector,cfg regconfig,big bigint,fixed character(5))")
				run(fixture.Exec("INSERT INTO tb_diag(id,title) VALUES(1,'Rats')", 1))
				if tc.clause != "" {
					// CREATE is deliberately successful even for invalid argument strings.
					var timing, event, level string
					_, err := fmt.Sscanf(tc.clause, "%s %s FOR EACH %s", &timing, &event, &level)
					if err != nil {
						t.Fatal(err)
					}
					userOperatorExec(t, p, b, c, "CREATE TRIGGER a "+timing+" "+event+" ON tb_diag FOR EACH "+level+" EXECUTE FUNCTION pg_catalog."+tc.function+"("+tc.args+")")
					if level == "ROW" {
						run(fixture.Exec("UPDATE tb_diag SET id=id WHERE false", 0))
					}
				}
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(tc.query, d)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
				}
				run(fixture.Select("SELECT id,title,v::text AS vector FROM tb_diag", []string{"id", "title", "vector"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "Rats", nil}}))
			})
		})
	}
	t.Run("instead", func(t *testing.T) {
		for _, tc := range []struct{ function, code, message string }{
			{"suppress_redundant_updates_trigger", "39P01", "suppress_redundant_updates_trigger: must be called before update"},
			{"tsvector_update_trigger", "XX000", "tsvector_update_trigger: must be fired BEFORE event"},
			{"tsvector_update_trigger_column", "XX000", "tsvector_update_trigger: must be fired BEFORE event"},
		} {
			t.Run(tc.function, func(t *testing.T) {
				triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					userOperatorExec(t, p, b, c, "CREATE TABLE tb_context_base(id integer)", "CREATE VIEW tb_context AS SELECT id FROM tb_context_base")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tb_context_base VALUES(1)", 1))
					userOperatorExec(t, p, b, c, "CREATE TRIGGER a INSTEAD OF UPDATE ON tb_context FOR EACH ROW EXECUTE FUNCTION pg_catalog."+tc.function+"()")
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic("UPDATE tb_context SET id=id", fixture.ControlsReferenceDiagnostic(tc.code, tc.message))
						s.Prepared = prepared
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM tb_context_base", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				})
			})
		}
	})
	t.Run("qualified_names", func(t *testing.T) {
		for _, tc := range []struct{ name, code, message string }{
			{"pg_catalog..simple", "42602", "invalid name syntax"},
			{"tb_other_database.pg_catalog.simple", "0A000", `cross-database references are not implemented: tb_other_database.pg_catalog.simple`},
		} {
			t.Run(tc.code, func(t *testing.T) {
				triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					userOperatorExec(t, p, b, c, "CREATE TABLE tb_names(id integer,title text,v tsvector)", "CREATE TRIGGER a BEFORE INSERT ON tb_names FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger('v','"+tc.name+"','title')")
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic("INSERT INTO tb_names VALUES(1,NULL,NULL)", fixture.ControlsReferenceDiagnostic(tc.code, tc.message))
						s.Prepared = prepared
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM tb_names", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				})
			})
		}
	})
	t.Run("schema_permission", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"tb_config_reader"})
		fixture.SchemaReferenceCleanup(t, "tb_config_private")
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE ROLE tb_config_reader", "CREATE SCHEMA tb_config_private", "CREATE TEXT SEARCH CONFIGURATION tb_config_private.live (COPY=pg_catalog.simple)", "CREATE TABLE tb_denied(id integer,title text,v tsvector)", "GRANT USAGE ON SCHEMA public TO tb_config_reader", "GRANT INSERT ON tb_denied TO tb_config_reader", "CREATE TRIGGER a BEFORE INSERT ON tb_denied FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger('v','tb_config_private.live','title')", "SET ROLE tb_config_reader")
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("INSERT INTO tb_denied VALUES(1,NULL,NULL)", fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema tb_config_private"))
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			userOperatorExec(t, p, b, c, "RESET ROLE")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM tb_denied", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		})
	})
}

func TestReferenceTriggerBuiltinsConfigTiming(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		userOperatorExec(t, p, b, c, "CREATE TABLE tb_lazy(id integer,cfg oid,title text,v tsvector)", "CREATE TRIGGER a BEFORE INSERT OR UPDATE ON tb_lazy FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger_column('v','cfg','title')")
		// OID 4294967294 is independently chosen and outside the fixture catalog.
		run(builtinVectorRows("INSERT INTO tb_lazy VALUES(1,4294967294,NULL,NULL) RETURNING id,v::text AS vector", []any{int64(1), ""}))
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO tb_lazy VALUES(2,4294967294,'',NULL)", fixture.ControlsReferenceDiagnostic("XX000", "cache lookup failed for text search configuration 4294967294"))
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
		}
		// Parsing remains mandatory on an UPDATE with no lexical source target.
		run(fixture.Exec("UPDATE tb_lazy SET v='manual:9' WHERE id=1", 1))
		run(fixture.ErrorDiagnostic("UPDATE tb_lazy SET title='' WHERE id=1", fixture.ControlsReferenceDiagnostic("XX000", "cache lookup failed for text search configuration 4294967294")))
		run(builtinVectorRows("INSERT INTO tb_lazy VALUES(2,'pg_catalog.simple'::regconfig::oid,'Rats',NULL) RETURNING id,v::text AS vector", []any{int64(2), "'rats':1"}))
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("UPDATE tb_lazy SET cfg=4294967294 WHERE id=2", fixture.ControlsReferenceDiagnostic("XX000", "cache lookup failed for text search configuration 4294967294"))
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
		}
		run(builtinVectorRows("SELECT id,v::text AS vector FROM tb_lazy WHERE id=2", []any{int64(2), "'rats':1"}))
		run(fixture.Exec("DELETE FROM tb_lazy WHERE id=2", 1))
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRIGGER a BEFORE INSERT OR UPDATE ON tb_lazy FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger('v','public.tb_no_config','title')")
		run(fixture.ErrorDiagnostic("INSERT INTO tb_lazy VALUES(3,NULL,NULL,NULL)", fixture.ControlsReferenceDiagnostic("42704", `text search configuration "public.tb_no_config" does not exist`)))
		run(builtinVectorRows("SELECT id,v::text AS vector FROM tb_lazy", []any{int64(1), "'manual':9"}))
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRIGGER a BEFORE INSERT OR UPDATE ON tb_lazy FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger_column('v','cfg','title')")
		// A valid prefix row must not survive the second row's NULL configuration.
		run(fixture.ErrorDiagnostic("INSERT INTO tb_lazy VALUES(4,'pg_catalog.simple'::regconfig::oid,'good',NULL),(5,NULL,'bad',NULL)", fixture.ControlsReferenceDiagnostic("22004", `configuration column "cfg" must not be null`)))
		run(builtinVectorRows("SELECT id,v::text AS vector FROM tb_lazy", []any{int64(1), "'manual':9"}))
	})
}

func TestReferenceTriggerBuiltinsCoercibility(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		userOperatorExec(t, p, b, c, "CREATE DOMAIN tb_text AS text CHECK(VALUE<>'forbidden')", "CREATE DOMAIN tb_vector AS tsvector", "CREATE DOMAIN tb_config AS regconfig",
			"CREATE TABLE tb_cast(id integer,cfg tb_config,title tb_text,body varchar,v tb_vector)",
			"CREATE TRIGGER a BEFORE INSERT OR UPDATE ON tb_cast FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger_column('v','cfg','title','body')")
		run(builtinVectorRows("INSERT INTO tb_cast VALUES(1,'pg_catalog.simple','The Fat','Rats',NULL) RETURNING id,v::text AS vector", []any{int64(1), "'fat':2 'rats':3 'the':1"}))
		userOperatorExec(t, p, b, c, "CREATE TABLE tb_intcfg(id integer,cfg integer,title varchar,v tsvector)", "CREATE TRIGGER a BEFORE INSERT ON tb_intcfg FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger_column('v','cfg','title')")
		run(fixture.Prepared(builtinVectorRows("INSERT INTO tb_intcfg VALUES(1,'pg_catalog.simple'::regconfig::oid::integer,'Rats',NULL) RETURNING id,v::text AS vector", []any{int64(1), "'rats':1"})))
	})
}
