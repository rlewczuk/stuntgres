package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSystemRowsViewWrites(t *testing.T) {
	srCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, view := range []struct {
			name  string
			count int
		}{{"sr_zero_write", 0}, {"sr_some_write", 3}} {
			srRun(t, p, b, c, fixture.Exec(fmt.Sprintf("CREATE VIEW %s AS SELECT id,payload FROM sr TABLESAMPLE system_rows(%d)", view.name, view.count), 0))
			srBoth(t, p, b, c, srCount("SELECT count(*) AS n FROM "+view.name, int64(view.count)))
			srViewWrites(t, p, b, c, view.name)
		}
		// The admission restriction is automatic only. Existing explicit rules
		// and INSTEAD OF triggers must still route writes to their own targets.
		for _, q := range []string{
			"CREATE TABLE sr_write_log(id integer,payload text)",
			"CREATE VIEW sr_rule_write AS SELECT id,payload FROM sr TABLESAMPLE system_rows(0)",
			"CREATE RULE sr_insert AS ON INSERT TO sr_rule_write DO INSTEAD INSERT INTO sr_write_log VALUES(NEW.id,NEW.payload)",
			"CREATE VIEW sr_trigger_write AS SELECT id,payload FROM sr TABLESAMPLE system_rows(0)",
			"CREATE FUNCTION sr_write_trigger() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO sr_write_log VALUES(NEW.id,NEW.payload); RETURN NEW; END$$",
			"CREATE TRIGGER sr_insert INSTEAD OF INSERT ON sr_trigger_write FOR EACH ROW EXECUTE FUNCTION sr_write_trigger()",
		} {
			srRun(t, p, b, c, fixture.Exec(q, 0))
		}
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{"INSERT INTO sr_rule_write VALUES(101,'rule')", "INSERT INTO sr_trigger_write VALUES(102,'trigger')"} {
				s := fixture.Exec(q, 1)
				s.Prepared = prepared
				srRun(t, p, b, c, s)
			}
			srBoth(t, p, b, c, fixture.Select("SELECT id,payload FROM sr_write_log ORDER BY id", srNames, srTypes, [][]any{{int64(101), "rule"}, {int64(102), "trigger"}}))
			srRun(t, p, b, c, fixture.Exec("DELETE FROM sr_write_log", 2))
			srBoth(t, p, b, c, fixture.Select("SELECT id,payload FROM sr ORDER BY id", srNames, srTypes, srRows()))
		}
	})
}

// Release-source reason from REL_18_6 rewriteHandler.c. This same public witness
// runs again after native Dump/Reset/Restore and independent-destination remap.
func srViewWrites(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, name string) {
	t.Helper()
	for _, operation := range []struct {
		command, verb, gerund, query string
	}{
		{"INSERT", "insert into", "inserting into", "INSERT INTO " + name + " VALUES(99,'unexpected')"},
		{"UPDATE", "update", "updating", "UPDATE " + name + " SET payload='unexpected'"},
		{"DELETE", "delete from", "deleting from", "DELETE FROM " + name},
	} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(operation.query, fixture.Diagnostic{
				Code: "55000", Message: fmt.Sprintf("cannot %s view %q", operation.verb, name),
				Detail: "Views containing TABLESAMPLE are not automatically updatable.",
				Hint:   fmt.Sprintf("To enable %s the view, provide an INSTEAD OF %s trigger or an unconditional ON %s DO INSTEAD rule.", operation.gerund, operation.command, operation.command),
			})
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			srRun(t, p, b, c, s)
			srBoth(t, p, b, c, srMetadata(fixture.Select("SELECT id,payload FROM sr ORDER BY id", srNames, srTypes, srRows()), b.PG))
		}
	}
	// This SQL builtin is not a supported native caller. Shared metadata
	// remains below; TestViewCapabilitiesSampledSource owns native mask bits.
	if b.PG {
		srBoth(t, p, b, c, fixture.Select("SELECT pg_relation_is_updatable($1::regclass,false) AS automatic,pg_relation_is_updatable($1::regclass,true) AS including_triggers", []string{"automatic", "including_triggers"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(0)}}, name))
	}
	srBoth(t, p, b, c, fixture.Select("SELECT is_updatable,is_insertable_into,is_trigger_updatable,is_trigger_deletable,is_trigger_insertable_into FROM information_schema.views WHERE table_schema='public' AND table_name=$1", []string{"is_updatable", "is_insertable_into", "is_trigger_updatable", "is_trigger_deletable", "is_trigger_insertable_into"}, []string{"VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR"}, [][]any{{"NO", "NO", "NO", "NO", "NO"}}, name))
	srBoth(t, p, b, c, fixture.Select("SELECT column_name,is_updatable FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 ORDER BY ordinal_position", []string{"column_name", "is_updatable"}, []string{"NAME", "VARCHAR"}, [][]any{{"id", "NO"}, {"payload", "NO"}}, name))
}
