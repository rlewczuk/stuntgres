package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkBuildSQLDeleteBoundaries(t *testing.T) {
	deleteCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE delete_keys(id text,k2 text,payload text)", 0))
		run(fixture.Exec("INSERT INTO delete_keys VALUES('a','b','kept')", 1))
		held := deleteHeld(t, p, c, deleteCall)
		for _, tc := range []struct{ vector string; count int64; array, predicate string }{
			{"1 2", 1, "{a}", "id = 'a'"},
			{"1 2", 99, "{a,b}", "id = 'a' AND k2 = 'b'"},
			{"1 999", 1, "{a}", "id = 'a'"},
			{"2 1", 2, "{b,a}", "k2 = 'b' AND id = 'a'"},
			{"1 1", 2, "{a,b}", "id = 'a' AND id = 'b'"},
			{"1 2", 2, "[0:1]={a,b}", "id = 'a' AND k2 = 'b'"},
			{"1 2", 2, "[-3:-2]={NULL,b}", "id IS NULL AND k2 = 'b'"},
			{"1 2 1 2", 4, "{{a,b},{c,d}}", "id = 'a' AND k2 = 'b' AND id = 'c' AND k2 = 'd'"},
			{"1 2 1 2", 4, "{{NULL,b},{c,NULL}}", "id IS NULL AND k2 = 'b' AND id = 'c' AND k2 IS NULL"},
		} {
			s := deleteSelect(deleteCall, "DELETE FROM delete_keys WHERE "+tc.predicate, "delete_keys", tc.vector, tc.count, tc.array)
			deleteBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			if tc.vector == "1 1" {
				run(fixture.Exec(deleteRead(t, p, b, held, s), 0))
			}
		}
		run(fixture.Select("SELECT id,k2,payload FROM delete_keys ORDER BY payload", []string{"id", "k2", "payload"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"a", "b", "kept"}}))
		run(fixture.Exec("CREATE TABLE delete_dropped(gone text,id text,k2 text)", 0))
		run(fixture.Exec("ALTER TABLE delete_dropped DROP COLUMN gone", 0))
		s := deleteSelect(deleteCall, "DELETE FROM delete_dropped WHERE id = 'a' AND k2 = 'b'", "delete_dropped", "1 2", int64(2), "{a,b}")
		deleteBoth(t, p, b, c, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		deleteBoth(t, p, b, c, deleteError(deleteCall, "22023", "invalid attribute number 3", "delete_dropped", "3", int64(1), "{b}"))
		run(fixture.Exec("ALTER TABLE delete_dropped RENAME COLUMN id TO renamed", 0))
		s = deleteSelect(deleteCall, "DELETE FROM delete_dropped WHERE renamed = 'new' AND k2 IS NULL", "delete_dropped", "1 2", int64(2), "{new,NULL}")
		deleteBoth(t, p, b, c, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		run(fixture.Exec("INSERT INTO delete_dropped VALUES('new',NULL),('new','decoy')", 2))
		run(fixture.Exec(deleteRead(t, p, b, held, s), 1))
		run(fixture.Select("SELECT renamed,k2 FROM delete_dropped ORDER BY k2", []string{"renamed", "k2"}, []string{"TEXT", "TEXT"}, [][]any{{"new", "decoy"}}))
	})
}
