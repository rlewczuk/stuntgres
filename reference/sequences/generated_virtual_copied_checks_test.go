package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedVirtualCopiedChecks(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, unused func(fixture.Step)) {
		var notices []fixture.Diagnostic
		_, conn, recorder := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec(`CREATE TABLE p(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL,CONSTRAINT positive CHECK(b>0))`, 0))
		run(fixture.Exec(`CREATE TABLE ordinary(LIKE p INCLUDING CONSTRAINTS EXCLUDING GENERATED)`, 0))
		run(fixture.Exec(`INSERT INTO ordinary VALUES(1,9),(-20,7)`, 2))
		ordinaryRows := fixture.Select(`SELECT a,b FROM ordinary ORDER BY a`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(-20), int64(7)}, {int64(1), int64(9)}})
		ordinaryError := fixture.Diagnostic{Code: "23514", Message: `new row for relation "ordinary" violates check constraint "positive"`, Detail: "Failing row contains (1, -9).", Schema: "public", Table: "ordinary", Constraint: "positive"}
		for _, prepared := range []bool{false, true} {
			for _, sql := range []string{`INSERT INTO ordinary VALUES(4,8),(1,-9)`, `UPDATE ordinary SET b=-9 WHERE a=1`} {
				s := fixture.ImplicitError(sql, ordinaryError)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				run(ordinaryRows)
			}
		}
		// The inherited CHECK must use the explicit child's generation, not p's.
		notices = nil
		run(fixture.Exec(`CREATE TABLE overridden(b INT GENERATED ALWAYS AS(a-10) VIRTUAL) INHERITS(p)`, 0))
		wantNotices := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `moving and merging column "b" with inherited definition`, Detail: "User-specified column moved to the position of the inherited column."}}
		if !reflect.DeepEqual(notices, wantNotices) {
			t.Fatalf("notices %#v want %#v", notices, wantNotices)
		}
		run(fixture.Exec(`INSERT INTO overridden(a) VALUES(12)`, 1))
		childRows := fixture.Select(`SELECT a,b FROM overridden`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(12), int64(2)}})
		childError := fixture.Diagnostic{Code: "23514", Message: `new row for relation "overridden" violates check constraint "positive"`, Detail: "Failing row contains (1, virtual).", Schema: "public", Table: "overridden", Constraint: "positive"}
		for _, prepared := range []bool{false, true} {
			for _, sql := range []string{`INSERT INTO overridden(a) VALUES(14),(1)`, `UPDATE overridden SET a=1`} {
				s := fixture.ImplicitError(sql, childError)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				run(childRows)
			}
		}
		// Conversely a valid child value must not be rejected by a negative parent recipe.
		notices = nil
		run(fixture.Exec(`CREATE TABLE reverse_override(b INT GENERATED ALWAYS AS(10-a) VIRTUAL) INHERITS(p)`, 0))
		if !reflect.DeepEqual(notices, wantNotices) {
			t.Fatalf("notices %#v want %#v", notices, wantNotices)
		}
		run(fixture.Exec(`INSERT INTO reverse_override(a) VALUES(-2)`, 1))
		run(fixture.Select(`SELECT a,b FROM reverse_override`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(-2), int64(12)}}))
	})
}

func TestReferenceGeneratedVirtualCopyGeometry(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE p(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL,CONSTRAINT positive CHECK(b>0))`, 0))
		run(fixture.Exec(`CREATE TABLE q(d INT,e INT GENERATED ALWAYS AS(d+2) VIRTUAL,CONSTRAINT q_positive CHECK(e>0))`, 0))
		for _, layout := range []struct {
			name      string
			ddl       string
			insert    string
			badInsert string
			detail    string
		}{
			{"virtual_leading", `CREATE TABLE virtual_leading(x INT,y INT,LIKE p INCLUDING GENERATED INCLUDING CONSTRAINTS)`, `INSERT INTO virtual_leading(x,y,a) VALUES(10,20,1)`, `INSERT INTO virtual_leading(x,y,a) VALUES(30,40,4),(10,20,-2)`, "Failing row contains (10, 20, -2, virtual)."},
			{"virtual_trailing", `CREATE TABLE virtual_trailing(LIKE p INCLUDING GENERATED INCLUDING CONSTRAINTS,x INT,y INT)`, `INSERT INTO virtual_trailing(x,y,a) VALUES(10,20,1)`, `INSERT INTO virtual_trailing(x,y,a) VALUES(30,40,4),(10,20,-2)`, "Failing row contains (-2, virtual, 10, 20)."},
			{"multiple", `CREATE TABLE multiple(x INT,LIKE p INCLUDING GENERATED INCLUDING CONSTRAINTS,y INT,LIKE q INCLUDING GENERATED INCLUDING CONSTRAINTS,z INT)`, `INSERT INTO multiple(x,y,z,a,d) VALUES(10,20,30,1,3)`, `INSERT INTO multiple(x,y,z,a,d) VALUES(40,50,60,4,6),(10,20,30,-2,3)`, "Failing row contains (10, -2, virtual, 20, 3, virtual, 30)."},
			{"extra_child", `CREATE TABLE extra_child(x INT,y INT) INHERITS(p)`, `INSERT INTO extra_child(x,y,a) VALUES(10,20,1)`, `INSERT INTO extra_child(x,y,a) VALUES(30,40,4),(10,20,-2)`, "Failing row contains (-2, virtual, 10, 20)."},
		} {
			run(fixture.Exec(layout.ddl, 0))
			run(fixture.Exec(layout.insert, 1))
			rows := fixture.Select(`SELECT a,b,x,y FROM `+layout.name, []string{"a", "b", "x", "y"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(10), int64(20)}})
			run(rows)
			if layout.name == "multiple" {
				run(fixture.Select(`SELECT d,e,z FROM multiple`, []string{"d", "e", "z"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(3), int64(5), int64(30)}}))
			}
			ck := fixture.Diagnostic{Code: "23514", Message: `new row for relation "` + layout.name + `" violates check constraint "positive"`, Detail: layout.detail, Schema: "public", Table: layout.name, Constraint: "positive"}
			for _, prepared := range []bool{false, true} {
				for _, sql := range []string{layout.badInsert, `UPDATE ` + layout.name + ` SET a=-2`} {
					s := fixture.ImplicitError(sql, ck)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
					run(rows)
				}
			}
		}
		// The second LIKE input's own copied CHECK must use its later slots too.
		ck := fixture.Diagnostic{Code: "23514", Message: `new row for relation "multiple" violates check constraint "q_positive"`, Detail: "Failing row contains (10, 1, virtual, 20, -3, virtual, 30).", Schema: "public", Table: "multiple", Constraint: "q_positive"}
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(`UPDATE multiple SET d=-3`, ck)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Select(`SELECT a,b,d,e,x,y,z FROM multiple`, []string{"a", "b", "d", "e", "x", "y", "z"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(3), int64(5), int64(10), int64(20), int64(30)}}))
		}
	})
}
