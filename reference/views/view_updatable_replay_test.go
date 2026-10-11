package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewUpdatableReplay(t *testing.T) {
	p := fixture.NewScenario(t)
	var emitted []byte
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE TABLE replay_base(id INT DEFAULT 9,a INT,b INT,hidden TEXT DEFAULT 'base')", 0), fixture.Exec("CREATE VIEW replay_view WITH(security_barrier=true) AS SELECT id AS key,a AS x,b AS y FROM replay_base WHERE id>0", 0), fixture.Exec("CREATE VIEW replay_nested AS SELECT key,x,y FROM replay_view WHERE x>0", 0), fixture.Exec("INSERT INTO replay_nested(x,y) VALUES(7,8)", 1), fixture.Exec("UPDATE replay_nested SET x=11", 1),
			} {
				run(s)
			}
			if !b.PG {
				emitted = fixture.ViewDump(t, p)
			} else if len(emitted) != 0 {
				run(fixture.Exec("DROP TABLE replay_base CASCADE", 0))
				// This is the genuine emitted stream, not a test-authored substitute.
				if _, err := b.DB.ExecContext(p.Ctx, string(emitted)); err != nil {
					t.Fatal("PostgreSQL replay of actual native Dump", err)
				}
			}
			run(fixture.Select("SELECT id,a,b,hidden FROM replay_base", []string{"id", "a", "b", "hidden"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(9), int64(11), int64(8), "base"}}))
			h := fixture.ViewHold(t, p, b, "UPDATE replay_nested SET x=$1 WHERE key=9 RETURNING key,x,y")
			fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(h.SQL, []string{"key", "x", "y"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(9), int64(15), int64(8)}}, 15))
			run(fixture.Exec("DELETE FROM replay_nested WHERE key=9", 1))
			run(fixture.Select("SELECT id,a,b,hidden FROM replay_base", []string{"id", "a", "b", "hidden"}, []string{"INT4", "INT4", "INT4", "TEXT"}, nil))
		})
	}
}
