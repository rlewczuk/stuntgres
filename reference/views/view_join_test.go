package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewJoinedSourcesAndFrozenLayouts(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE TABLE a(unused INT,id INT,x TEXT)", 0), fixture.Exec("CREATE TABLE b(unused INT,id INT,y TEXT)", 0), fixture.Exec("INSERT INTO a VALUES(0,1,'a'),(0,2,'b'),(0,3,'c')", 3), fixture.Exec("INSERT INTO b VALUES(0,1,'u'),(0,1,'v'),(0,3,NULL)", 3),
				fixture.Exec("CREATE VIEW v AS SELECT a.id,a.x,b.y FROM a JOIN b ON a.id=b.id", 0), fixture.Exec("CREATE VIEW nested AS SELECT id,x,y FROM v", 0), fixture.Exec("CREATE VIEW mixed AS SELECT a.*,DATE '2000-01-02' AS d,b.y FROM a JOIN b ON a.id=b.id", 0), fixture.Exec("CREATE VIEW hidden AS SELECT a.id FROM a JOIN b ON a.id=b.id", 0), fixture.Exec("CREATE TABLE copied(id INT,x TEXT,y TEXT)", 0), fixture.Exec("INSERT INTO copied SELECT id,x,y FROM nested", 3),
			} {
				run(s)
			}
			want := [][]any{{int64(1), "a", "u"}, {int64(1), "a", "v"}, {int64(3), "c", nil}}
			mixed := fixture.Select("SELECT unused,id,x,d=DATE '2000-01-02',y FROM mixed", []string{"unused", "id", "x", "?column?", "y"}, []string{"INT4", "INT4", "TEXT", "BOOL", "TEXT"}, [][]any{{int64(0), int64(1), "a", true, "u"}, {int64(0), int64(1), "a", true, "v"}, {int64(0), int64(3), "c", true, nil}})
			mixed.Bag = true
			for phase := range 2 {
				if phase == 1 {
					for _, s := range []fixture.Step{
						fixture.Exec("ALTER TABLE a RENAME TO aa", 0), fixture.Exec("ALTER TABLE b RENAME TO bb", 0), fixture.Exec("ALTER TABLE aa RENAME COLUMN id TO aid", 0), fixture.Exec("ALTER TABLE bb RENAME COLUMN id TO bid", 0), fixture.Exec("ALTER TABLE bb DROP COLUMN unused", 0), fixture.Exec("CREATE TABLE a(id INT,x TEXT)", 0), fixture.Exec("CREATE TABLE b(id INT,y TEXT)", 0), fixture.Exec("INSERT INTO a VALUES(99,'wrong')", 1), fixture.Exec("INSERT INTO b VALUES(99,'wrong')", 1),
					} {
						run(s)
					}
				}
				names := []string{"v", "nested"}
				if phase == 0 {
					names = append(names, "copied")
				}
				for _, name := range names {
					s := fixture.Select("SELECT id,x,y FROM "+name, []string{"id", "x", "y"}, []string{"INT4", "TEXT", "TEXT"}, want)
					s.Bag = true
					run(s)
					run(fixture.Prepared(s))
				}
				run(mixed)
			}
			query := "SELECT * FROM mixed"
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
			rows, err := b.DB.QueryContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			metadata, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			names, types := []string{"unused", "id", "x", "d", "y"}, []string{"INT4", "INT4", "TEXT", "DATE", "TEXT"}
			if len(metadata) != 5 {
				t.Fatal("frozen star width", metadata)
			}
			for i, c := range metadata {
				if c.Name() != names[i] || c.DatabaseTypeName() != types[i] {
					t.Fatal("frozen star metadata", i, c)
				}
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			fixture.ViewRecorded(t, b.Recorder, query, names, types, nil, false)
			_, err = b.DB.ExecContext(ctx, "ALTER TABLE bb DROP COLUMN bid")
			fixture.State(t, err, "2BP01")
		})
	}
}

func TestReferenceViewJoinedCompactLayouts(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE a(unused INT,id INT)", 0), fixture.Exec("CREATE TABLE b(unused INT,id INT)", 0), fixture.Exec("INSERT INTO a VALUES(0,7)", 1), fixture.Exec("INSERT INTO b VALUES(0,7)", 1), fixture.Exec("CREATE VIEW v AS SELECT a.id FROM a JOIN b ON a.id=b.id", 0), fixture.Exec("ALTER TABLE a DROP COLUMN unused", 0), fixture.Exec("ALTER TABLE b DROP COLUMN unused", 0)} {
				run(s)
			}
			run(fixture.Select("SELECT id FROM v", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			s := fixture.ImplicitError("ALTER TABLE b DROP COLUMN id", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop column id of table b because other objects depend on it", Detail: "view v depends on column id of table b", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
			s.ErrorStage = "execute"
			run(s)
		})
	}
}

func TestReferenceViewJoinedRepeatedOccurrences(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(unused INT,id INT)", 0), fixture.Exec("INSERT INTO t VALUES(0,1),(0,2),(0,3)", 3), fixture.Exec("CREATE VIEW pairs AS SELECT l.id AS left_id,r.id AS right_id FROM t AS l JOIN t AS r ON l.id<r.id", 0), fixture.Exec("ALTER TABLE t DROP COLUMN unused", 0)} {
				run(s)
			}
			s := fixture.Select("SELECT left_id,right_id FROM pairs", []string{"left_id", "right_id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(1), int64(3)}, {int64(2), int64(3)}})
			s.Bag = true
			run(s)
			run(fixture.Prepared(s))
		})
	}
}
