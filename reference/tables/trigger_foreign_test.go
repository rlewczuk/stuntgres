package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerCoreForeign(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		run(fixture.Exec("CREATE FUNCTION tr_foreign_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.label := NEW.label||':trigger'; RETURN CASE WHEN NEW.id=5 THEN NULL ELSE NEW END; END$$", 0))
		run(fixture.Exec("CREATE TRIGGER a BEFORE INSERT OR UPDATE ON fdw_data FOR EACH ROW EXECUTE FUNCTION tr_foreign_fn()", 0))
		run(fixture.Exec("INSERT INTO fdw_data VALUES(4,'four',true),(5,'five',false)", 1))
		run(fixture.Prepared(fixture.Exec("UPDATE fdw_data SET label='changed' WHERE id=4", 1)))
		want := [][]any{{int64(1), "one", true}, {int64(2), nil, false}, {int64(3), "three", nil}, {int64(4), "changed:trigger", true}}
		run(fdwRows("SELECT id,label,flag FROM fdw_data ORDER BY id", want))
		fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", want))
		run(fixture.Exec("CREATE FUNCTION tr_foreign_visible() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.label := (SELECT count(*)::text FROM fdw_data WHERE id>3); RETURN NEW; END$$", 0))
		run(fixture.Exec("CREATE OR REPLACE TRIGGER a BEFORE INSERT ON fdw_data FOR EACH ROW EXECUTE FUNCTION tr_foreign_visible()", 0))
		run(fixture.Exec("INSERT INTO fdw_data VALUES(6,NULL,false),(7,NULL,false)", 2))
		run(fixture.Exec("INSERT INTO fdw_data SELECT x,NULL,false FROM generate_series(8,9) AS s(x)", 2))
		tail := [][]any{{int64(4), "changed:trigger", true}, {int64(6), "1", false}, {int64(7), "2", false}, {int64(8), "3", false}, {int64(9), "4", false}}
		run(fdwRows("SELECT id,label,flag FROM fdw_data WHERE id>3 ORDER BY id", tail))
		fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id>3 ORDER BY id", tail))
	})
}
