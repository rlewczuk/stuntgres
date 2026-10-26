package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterValidateConstraintPartitions(t *testing.T) {
	for _, nn := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "check", true: "not_null"}[nn]+map[bool]string{false: "/direct", true: "/held"}[prepared], func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					for _, q := range []string{"CREATE TABLE root(k INT,a INT) PARTITION BY RANGE(k)", "CREATE TABLE mid PARTITION OF root DEFAULT PARTITION BY RANGE(k)", "CREATE TABLE early PARTITION OF mid FOR VALUES FROM(0) TO(10)", "CREATE TABLE late PARTITION OF mid DEFAULT"} {
						run(fixture.Exec(q, 0))
					}
					bad := any(int64(0))
					clause := "CONSTRAINT fact CHECK(a>0) NOT VALID"
					if nn {
						bad = nil
						clause = "CONSTRAINT fact NOT NULL a NOT VALID"
					}
					run(fixture.Exec("INSERT INTO root VALUES(1,1),(20,$1)", 2, bad))
					run(fixture.Exec("ALTER TABLE root ADD "+clause, 0))
					before := validateConstraintSnapshot(t, p, c)
					held := addConstraintHeld(t, p, b, c, "ALTER TABLE root VALIDATE CONSTRAINT fact")
					d := fixture.ControlsReferenceDiagnostic("23514", `check constraint "fact" of relation "late" is violated by some row`)
					d.Schema, d.Table, d.Constraint = "public", "late", "fact"
					if nn {
						d = fixture.ControlsReferenceDiagnostic("23502", `column "a" of relation "late" contains null values`)
						d.Schema, d.Table, d.Column = "public", "late", "a"
					}
					run(fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE root ADD CONSTRAINT companion CHECK(a>=0) NOT VALID, VALIDATE CONSTRAINT fact", d)))
					if !reflect.DeepEqual(before, validateConstraintSnapshot(t, p, c)) {
						t.Fatal("late partition failure retained a companion definition or promotion")
					}
					if prepared {
						validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, d))
					} else {
						run(fixture.ErrorDiagnostic(held.SQL, d))
					}
					if !reflect.DeepEqual(before, validateConstraintSnapshot(t, p, c)) {
						t.Fatal("partition failure changed any flag/origin")
					}
					run(fixture.ErrorDiagnostic("ALTER TABLE ONLY root VALIDATE CONSTRAINT fact", fixture.ControlsReferenceDiagnostic("42P16", "constraint must be validated on child tables too")))
					run(fixture.Exec("UPDATE root SET a=2 WHERE k=20", 1))
					validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
					validateConstraintOrigins(t, p, c, before)
					for _, table := range []string{"root", "mid", "early", "late"} {
						run(validateConstraintFlag(table, "fact", true))
					}
					run(fixture.Exec("ALTER TABLE ONLY root VALIDATE CONSTRAINT fact", 0))
					run(fixture.Select("SELECT k,a FROM root ORDER BY k", []string{"k", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(20), int64(2)}}))
					fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, true, "SELECT k,a FROM late", []string{"k", "a"}, []string{"INT4", "INT4"}, []bool{true, !nn}, 1)
				})
			})
		}
	}
	t.Run("not_null_existing_leaf_name_and_order", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE root(id INT,note TEXT) PARTITION BY RANGE(id)", 0))
			run(fixture.Exec("ALTER TABLE root ADD CONSTRAINT parent_nn NOT NULL id NOT VALID", 0))
			run(fixture.Exec("CREATE TABLE leaf(note TEXT,id INT CONSTRAINT child_nn NOT NULL)", 0))
			run(fixture.Exec("INSERT INTO leaf VALUES('keep',1)", 1))
			run(fixture.Exec("ALTER TABLE root ATTACH PARTITION leaf FOR VALUES FROM(1) TO(2)", 0))
			before := validateConstraintSnapshot(t, p, c)
			run(fixture.Prepared(fixture.Exec("ALTER TABLE root VALIDATE CONSTRAINT parent_nn", 0)))
			run(validateConstraintFlag("root", "parent_nn", true))
			run(validateConstraintFlag("leaf", "child_nn", true))
			validateConstraintOrigins(t, p, c, before)
			run(fixture.Select("SELECT note,id FROM leaf", []string{"note", "id"}, []string{"TEXT", "INT4"}, [][]any{{"keep", int64(1)}}))
		})
	})
	for _, nn := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			t.Run(map[bool]string{false: "check", true: "nn"}[nn]+map[bool]string{false: "/add_validate", true: "/validate_add"}[reverse], func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE root(k INT,a INT) PARTITION BY RANGE(k)", 0))
					run(fixture.Exec("CREATE TABLE leaf PARTITION OF root DEFAULT", 0))
					run(fixture.Exec("INSERT INTO root VALUES(1,1)", 1))
					add := "ADD CONSTRAINT fact CHECK(a>0) NOT VALID"
					if nn {
						add = "ADD CONSTRAINT fact NOT NULL a NOT VALID"
					}
					first, second := add, "VALIDATE CONSTRAINT fact"
					if reverse {
						first, second = second, first
					}
					run(fixture.Prepared(fixture.Exec("ALTER TABLE root "+first+", "+second, 0)))
					run(validateConstraintFlag("root", "fact", true))
					run(validateConstraintFlag("leaf", "fact", true))
					run(fixture.Select("SELECT k,a FROM root", []string{"k", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
				})
			})
		}
	}
}
