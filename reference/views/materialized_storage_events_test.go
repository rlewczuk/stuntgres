package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMaterializedStorageEvents(t *testing.T) {
	materializedStorageRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		materializedStorageSeed(run)
		run(fixture.Exec("CREATE TABLE ms_events(event text,tag text,identity text)", 0))
		run(fixture.Exec(`CREATE FUNCTION ms_event_start() RETURNS event_trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO ms_events VALUES(TG_EVENT,TG_TAG,NULL); END $$`, 0))
		run(fixture.Exec(`CREATE FUNCTION ms_event_end() RETURNS event_trigger LANGUAGE plpgsql AS $$ DECLARE r record; BEGIN INSERT INTO ms_events VALUES(TG_EVENT,TG_TAG,NULL); FOR r IN SELECT * FROM pg_event_trigger_ddl_commands() LOOP INSERT INTO ms_events VALUES('collected',r.command_tag,r.object_identity); END LOOP; END $$`, 0))
		run(fixture.Exec("CREATE EVENT TRIGGER ms_event_start ON ddl_command_start WHEN TAG IN ('ALTER MATERIALIZED VIEW','ALTER TABLE') EXECUTE FUNCTION ms_event_start()", 0))
		run(fixture.Exec("CREATE EVENT TRIGGER ms_event_end ON ddl_command_end WHEN TAG IN ('ALTER MATERIALIZED VIEW','ALTER TABLE') EXECUTE FUNCTION ms_event_end()", 0))
		defer c.ExecContext(p.Ctx, "DROP EVENT TRIGGER IF EXISTS ms_event_start")
		defer c.ExecContext(p.Ctx, "DROP EVENT TRIGGER IF EXISTS ms_event_end")
		for _, tc := range []struct {
			query, tag string
			collected  bool
		}{
			{"ALTER MATERIALIZED VIEW ms_mv ALTER id SET STATISTICS 7", "ALTER MATERIALIZED VIEW", true},
			{"ALTER MATERIALIZED VIEW ms_mv SET (fillfactor=70)", "ALTER MATERIALIZED VIEW", true},
			{"ALTER MATERIALIZED VIEW ms_mv CLUSTER ON ms_a", "ALTER MATERIALIZED VIEW", true},
			{"ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_default SET TABLESPACE pg_default NOWAIT", "ALTER MATERIALIZED VIEW", false},
			{"ALTER TABLE ms_mv SET (fillfactor=80)", "ALTER TABLE", true},
		} {
			run(fixture.Exec("DELETE FROM ms_events", 0))
			run(fixture.Exec(tc.query, 0))
			rows := [][]any{{"ddl_command_end", tc.tag, nil}, {"ddl_command_start", tc.tag, nil}}
			if tc.collected {
				rows = append([][]any{{"collected", tc.tag, "public.ms_mv"}}, rows...)
			}
			run(fixture.Select("SELECT event,tag,identity FROM ms_events ORDER BY event,tag,identity", []string{"event", "tag", "identity"}, []string{"TEXT", "TEXT", "TEXT"}, rows))
			// Clear with a checked count: both event phases and, where present,
			// the collected command are real transactional writes.
			n := int64(2)
			if tc.collected {
				n = 3
			}
			run(fixture.Exec("DELETE FROM ms_events", n))
		}
		unchanged := func() {
			run(materializedStorageColumns(int64(7), nil, "x", ""))
			run(materializedStorageOptions("{fillfactor=80}"))
			run(materializedStorageCluster(true, false))
			run(materializedStorageMethod("heap"))
			run(materializedStorageRows())
		}
		unchanged()
		bounds := fixture.ControlsReferenceDiagnostic("22023", `value 9 out of bounds for option "fillfactor"`)
		bounds.Detail = `Valid values are between "10" and "100".`
		run(fixture.ErrorDiagnostic("ALTER MATERIALIZED VIEW ms_mv ALTER id SET STATISTICS 9, CLUSTER ON ms_b, SET (fillfactor=9)", bounds))
		// The admitted prefix and transactional start-trigger insert must both
		// roll back; no successful end/collected command may remain.
		run(fixture.Select("SELECT event,tag,identity FROM ms_events ORDER BY event,tag,identity", []string{"event", "tag", "identity"}, []string{"TEXT", "TEXT", "TEXT"}, nil))
		unchanged()
		// The held route reuses this exact successful statement from the loop.
		run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv ALTER id SET STATISTICS 7", 0))
		run(fixture.Select("SELECT event,tag,identity FROM ms_events ORDER BY event,tag,identity", []string{"event", "tag", "identity"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"collected", "ALTER MATERIALIZED VIEW", "public.ms_mv"}, {"ddl_command_end", "ALTER MATERIALIZED VIEW", nil}, {"ddl_command_start", "ALTER MATERIALIZED VIEW", nil}}))
		run(fixture.Exec("DELETE FROM ms_events", 3))
		unchanged()
	})
}
