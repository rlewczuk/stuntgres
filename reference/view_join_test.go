package reference_test

import (
	"context"
	"testing"
)

func TestReferenceViewJoinedSourcesAndFrozenLayouts(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			for _, s := range []referenceStep{
				referenceExec("CREATE TABLE a(unused INT,id INT,x TEXT)", 0), referenceExec("CREATE TABLE b(unused INT,id INT,y TEXT)", 0), referenceExec("INSERT INTO a VALUES(0,1,'a'),(0,2,'b'),(0,3,'c')", 3), referenceExec("INSERT INTO b VALUES(0,1,'u'),(0,1,'v'),(0,3,NULL)", 3),
				referenceExec("CREATE VIEW v AS SELECT a.id,a.x,b.y FROM a JOIN b ON a.id=b.id", 0), referenceExec("CREATE VIEW nested AS SELECT id,x,y FROM v", 0), referenceExec("CREATE VIEW mixed AS SELECT a.*,DATE '2000-01-02' AS d,b.y FROM a JOIN b ON a.id=b.id", 0), referenceExec("CREATE VIEW hidden AS SELECT a.id FROM a JOIN b ON a.id=b.id", 0), referenceExec("CREATE TABLE copied(id INT,x TEXT,y TEXT)", 0), referenceExec("INSERT INTO copied SELECT id,x,y FROM nested", 3),
			} {
				run(s)
			}
			want := [][]any{{int64(1), "a", "u"}, {int64(1), "a", "v"}, {int64(3), "c", nil}}
			mixed := referenceSelect("SELECT unused,id,x,d=DATE '2000-01-02',y FROM mixed", []string{"unused", "id", "x", "?column?", "y"}, []string{"INT4", "INT4", "TEXT", "BOOL", "TEXT"}, [][]any{{int64(0), int64(1), "a", true, "u"}, {int64(0), int64(1), "a", true, "v"}, {int64(0), int64(3), "c", true, nil}})
			mixed.bag = true
			for phase := range 2 {
				if phase == 1 {
					for _, s := range []referenceStep{
						referenceExec("ALTER TABLE a RENAME TO aa", 0), referenceExec("ALTER TABLE b RENAME TO bb", 0), referenceExec("ALTER TABLE aa RENAME COLUMN id TO aid", 0), referenceExec("ALTER TABLE bb RENAME COLUMN id TO bid", 0), referenceExec("ALTER TABLE bb DROP COLUMN unused", 0), referenceExec("CREATE TABLE a(id INT,x TEXT)", 0), referenceExec("CREATE TABLE b(id INT,y TEXT)", 0), referenceExec("INSERT INTO a VALUES(99,'wrong')", 1), referenceExec("INSERT INTO b VALUES(99,'wrong')", 1),
					} {
						run(s)
					}
				}
				names := []string{"v", "nested"}
				if phase == 0 {
					names = append(names, "copied")
				}
				for _, name := range names {
					s := referenceSelect("SELECT id,x,y FROM "+name, []string{"id", "x", "y"}, []string{"INT4", "TEXT", "TEXT"}, want)
					s.bag = true
					run(s)
					run(referencePrepared(s))
				}
				run(mixed)
			}
			query := "SELECT * FROM mixed"
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder)
			rows, err := b.db.QueryContext(ctx, query)
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
			referenceViewRecorded(t, b.recorder, query, names, types, nil, false)
			_, err = b.db.ExecContext(ctx, "ALTER TABLE bb DROP COLUMN bid")
			referenceState(t, err, "2BP01")
		})
	}
}

func TestReferenceViewJoinedCompactLayouts(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			for _, s := range []referenceStep{referenceExec("CREATE TABLE a(unused INT,id INT)", 0), referenceExec("CREATE TABLE b(unused INT,id INT)", 0), referenceExec("INSERT INTO a VALUES(0,7)", 1), referenceExec("INSERT INTO b VALUES(0,7)", 1), referenceExec("CREATE VIEW v AS SELECT a.id FROM a JOIN b ON a.id=b.id", 0), referenceExec("ALTER TABLE a DROP COLUMN unused", 0), referenceExec("ALTER TABLE b DROP COLUMN unused", 0)} {
				run(s)
			}
			run(referenceSelect("SELECT id FROM v", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			s := referenceImplicitError("ALTER TABLE b DROP COLUMN id", referenceDiagnostic{Code: "2BP01", Message: "cannot drop column id of table b because other objects depend on it", Detail: "view v depends on column id of table b", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
			s.errorStage = "execute"
			run(s)
		})
	}
}

func TestReferenceViewJoinedRepeatedOccurrences(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			for _, s := range []referenceStep{referenceExec("CREATE TABLE t(unused INT,id INT)", 0), referenceExec("INSERT INTO t VALUES(0,1),(0,2),(0,3)", 3), referenceExec("CREATE VIEW pairs AS SELECT l.id AS left_id,r.id AS right_id FROM t AS l JOIN t AS r ON l.id<r.id", 0), referenceExec("ALTER TABLE t DROP COLUMN unused", 0)} {
				run(s)
			}
			s := referenceSelect("SELECT left_id,right_id FROM pairs", []string{"left_id", "right_id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(1), int64(3)}, {int64(2), int64(3)}})
			s.bag = true
			run(s)
			run(referencePrepared(s))
		})
	}
}
