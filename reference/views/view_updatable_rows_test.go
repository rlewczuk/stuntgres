package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Each route has isolated state. Prepared writes use an actual retained handle.
func viewWriteRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool)) {
	t.Helper()
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) { body(t, p, b, held) })
			}
		})
	}
}
func viewWriteRun(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool, s fixture.Step) {
	t.Helper()
	if held && s.State == "" && (len(s.Query) >= 6 && (s.Query[:6] == "INSERT" || s.Query[:6] == "UPDATE" || s.Query[:6] == "DELETE")) {
		h := fixture.ViewHold(t, p, b, s.Query)
		fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
	} else {
		s.Prepared = held && s.State != ""
		if s.State != "" && s.ErrorStage == "" {
			s.ErrorStage = "execute"
			// Analysis/rewrite failures occur in Parse for a prepared consumer.
			// Constraint, arithmetic and executor authorization failures remain Execute.
			if held && s.State != "42501" && s.State[:2] != "22" && s.State[:2] != "23" {
				s.ErrorStage = "prepare"
			}
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}
func viewWriteBag(q string, names, types []string, rows [][]any) fixture.Step {
	s := fixture.Select(q, names, types, rows)
	s.Bag = true
	return s
}
func TestReferenceViewUpdatableRows(t *testing.T) {
	viewWriteRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s fixture.Step) { viewWriteRun(t, p, b, held, s) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE t(id INT DEFAULT 9,a INT,b INT,txt TEXT DEFAULT 'base',hidden INT DEFAULT 42)", 0),
			fixture.Exec("CREATE VIEW v AS SELECT b AS right_value,id AS key,a AS left_value FROM t WHERE id>0", 0),
			fixture.Exec("INSERT INTO t VALUES(1,10,20,'keep',5),(1,10,20,'keep',5),(0,30,40,'hidden',6)", 3),
			fixture.Exec("INSERT INTO v(left_value,right_value) VALUES(7,8)", 1),
			fixture.Exec("INSERT INTO v VALUES(60)", 1),
			fixture.Exec("INSERT INTO v DEFAULT VALUES", 1),
			fixture.Exec("INSERT INTO v(key,left_value) VALUES(NULL,11),(DEFAULT,12)", 2),
			fixture.Exec("INSERT INTO v(left_value,right_value) SELECT 13,14", 1),
			viewWriteBag("SELECT id,a,b,txt,hidden FROM t", []string{"id", "a", "b", "txt", "hidden"}, []string{"INT4", "INT4", "INT4", "TEXT", "INT4"}, [][]any{{int64(1), int64(10), int64(20), "keep", int64(5)}, {int64(1), int64(10), int64(20), "keep", int64(5)}, {int64(0), int64(30), int64(40), "hidden", int64(6)}, {int64(9), int64(7), int64(8), "base", int64(42)}, {int64(9), nil, int64(60), "base", int64(42)}, {int64(9), nil, nil, "base", int64(42)}, {nil, int64(11), nil, "base", int64(42)}, {int64(9), int64(12), nil, "base", int64(42)}, {int64(9), int64(13), int64(14), "base", int64(42)}}),
			viewWriteBag("UPDATE v SET left_value=right_value,right_value=left_value WHERE key=1 RETURNING key,left_value,right_value", []string{"key", "left_value", "right_value"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(20), int64(10)}, {int64(1), int64(20), int64(10)}}),
			fixture.Exec("UPDATE v SET left_value=left_value WHERE key=1", 2),
			fixture.Exec("UPDATE v SET left_value=99 WHERE key=99", 0),
			fixture.Select("UPDATE v SET left_value=99 WHERE key=99 RETURNING *", []string{"right_value", "key", "left_value"}, []string{"INT4", "INT4", "INT4"}, nil),
			fixture.Exec("DELETE FROM v WHERE key=1", 2),
			fixture.Select("SELECT id,a,b FROM t WHERE id=0", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(0), int64(30), int64(40)}}),
			fixture.Select("UPDATE v SET left_value=left_value WHERE key=99", []string{}, []string{}, nil),
		} {
			run(s)
		}
	})
}
func TestReferenceViewUpdatableVisibility(t *testing.T) {
	viewWriteRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s fixture.Step) { viewWriteRun(t, p, b, held, s) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE t(id INT,a INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10),(2,2),(0,20),(3,NULL)", 4),
			fixture.Exec("CREATE VIEW v AS SELECT id,a FROM t WHERE id>0", 0), fixture.Exec("CREATE VIEW w AS SELECT id,a FROM v WHERE a>5", 0),
			fixture.Exec("INSERT INTO w VALUES(-1,30)", 1),
			fixture.Select("UPDATE w SET id=-2 RETURNING id,a", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(-2), int64(10)}}),
			fixture.Exec("DELETE FROM w", 0),
			viewWriteBag("SELECT id,a FROM t", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(-2), int64(10)}, {int64(2), int64(2)}, {int64(0), int64(20)}, {int64(3), nil}, {int64(-1), int64(30)}}),
			fixture.Exec("CREATE TABLE danger(txt TEXT,visible BOOL)", 0), fixture.Exec("INSERT INTO danger VALUES('1',true),('oops',false)", 2), fixture.Exec("CREATE VIEW safe WITH(security_barrier=true) AS SELECT txt FROM danger WHERE visible", 0),
			fixture.Exec("UPDATE safe SET txt='2' WHERE CAST(txt AS INT)=1", 1), fixture.Exec("DELETE FROM safe WHERE CAST(txt AS INT)=2", 1),
			fixture.Select("SELECT txt FROM danger", []string{"txt"}, []string{"TEXT"}, [][]any{{"oops"}}),
		} {
			run(s)
		}
	})
}
