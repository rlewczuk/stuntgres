package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterDefaultInheritanceRecursion(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE p(id INT,i INT DEFAULT 1)", 0))
					run(fixture.Exec("CREATE TABLE c(i INT DEFAULT 9) INHERITS(p)", 0))
					run(fixture.Exec("INSERT INTO p VALUES(0,1)", 1))
					run(fixture.Exec("INSERT INTO c VALUES(10,9)", 1))
					run(fixture.Exec("ALTER TABLE p ALTER i SET DEFAULT 7", 0))
					run(fixture.Exec("INSERT INTO p(id) VALUES(1)", 1))
					run(fixture.Exec("INSERT INTO c(id) VALUES(11)", 1))
					run(fixture.Exec("ALTER TABLE ONLY p ALTER i SET DEFAULT 8", 0))
					run(fixture.Exec("INSERT INTO p(id) VALUES(2)", 1))
					run(fixture.Exec("INSERT INTO c(id) VALUES(12)", 1))
					run(fixture.Exec("ALTER TABLE p ALTER i DROP DEFAULT", 0))
					run(fixture.Exec("INSERT INTO p(id) VALUES(3)", 1))
					run(fixture.Exec("INSERT INTO c(id) VALUES(13)", 1))
					run(fixture.Exec("ALTER TABLE p ALTER i SET DEFAULT 6", 0))
					run(fixture.Exec("ALTER TABLE ONLY p ALTER i DROP DEFAULT", 0))
					run(fixture.Exec("INSERT INTO p(id) VALUES(4)", 1))
					run(fixture.Exec("INSERT INTO c(id) VALUES(14)", 1))
					run(fixture.Select("SELECT * FROM ONLY p ORDER BY id", []string{"id", "i"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(1)}, {int64(1), int64(7)}, {int64(2), int64(8)}, {int64(3), nil}, {int64(4), nil}}))
					run(fixture.Select("SELECT * FROM ONLY c ORDER BY id", []string{"id", "i"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(9)}, {int64(11), int64(7)}, {int64(12), int64(7)}, {int64(13), nil}, {int64(14), int64(6)}}))
					run(fixture.ImplicitError("ALTER TABLE p ALTER i SET DEFAULT (SELECT 1)", fixture.Diagnostic{Code: "0A000", Message: "cannot use subquery in DEFAULT expression"}))
				})
			}
		})
	}
}
