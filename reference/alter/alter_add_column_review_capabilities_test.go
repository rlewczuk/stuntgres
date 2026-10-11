package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddColumnReviewDefaultLists(t *testing.T) {
	cases := []struct {
		name, query string
		drop        bool
	}{
		{"add_then_set", "ALTER TABLE t ADD c INT DEFAULT 7, ALTER b SET DEFAULT 9", false},
		{"set_then_add", "ALTER TABLE t ALTER b SET DEFAULT 9, ADD c INT DEFAULT 7", false},
		{"add_then_drop", "ALTER TABLE t ADD c INT DEFAULT 7, ALTER b DROP DEFAULT", true},
		{"drop_then_add", "ALTER TABLE t ALTER b DROP DEFAULT, ADD c INT DEFAULT 7", true},
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", tc.name, prepared), func(t *testing.T) {
				alterAddReferenceScenario(t, func(p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(id INT,b INT DEFAULT 3)", 0))
					run(fixture.Exec("INSERT INTO t(id) VALUES(1)", 1))
					if b.PG {
						s := fixture.Exec(tc.query, 0)
						s.Prepared = prepared
						run(s)
						run(fixture.Exec("INSERT INTO t(id) VALUES(2)", 1))
						var future any = int64(9)
						if tc.drop {
							future = nil
						}
						run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(3), int64(7)}, {int64(2), future, int64(7)}}))
					} else {
						index := strings.Index(tc.query, ",")
						if strings.Contains(tc.name, "add_then") {
							index = strings.LastIndex(tc.query, "ALTER ")
						}
						failure := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "0A000", Message: "ALTER TABLE action lists are unsupported", Position: fixture.Position(len([]rune(tc.query[:index])) + 1)})
						failure.Prepared = prepared
						failure.ErrorStage = "execute"
						if prepared {
							failure.ErrorStage = "prepare"
						}
						run(failure)
						run(fixture.Select("SELECT * FROM t", []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(3)}}))
						run(fixture.Exec("INSERT INTO t(id) VALUES(2)", 1))
						run(fixture.Select("SELECT b FROM t ORDER BY id", []string{"b"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(3)}}))
					}
				})
			})
		}
	}
}

func TestReferenceAlterAddColumnReviewSerialIdentityBoundary(t *testing.T) {
	for _, identity := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("identity=%v/prepared=%v", identity, prepared), func(t *testing.T) {
				alterAddReferenceScenario(t, func(p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(id INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
					query := "ALTER TABLE t ADD b SERIAL"
					message := "ADD COLUMN serial declarations are unsupported"
					token := "b SERIAL"
					if identity {
						query = "ALTER TABLE t ADD b INT GENERATED ALWAYS AS IDENTITY"
						message = "ADD COLUMN identity declarations are unsupported"
						token = "GENERATED"
					}
					if b.PG {
						s := fixture.Exec(query, 0)
						s.Prepared = prepared
						run(s)
						bag := fixture.Select("SELECT b FROM t", []string{"b"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
						bag.Bag = true
						run(bag)
						run(fixture.Exec("INSERT INTO t(id) VALUES(3)", 1))
						run(fixture.Select("SELECT b FROM t WHERE id=3", []string{"b"}, []string{"INT4"}, [][]any{{int64(3)}}))
						fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, prepared, "SELECT * FROM t", []string{"id", "b"}, []string{"INT4", "INT4"}, []bool{true, false}, 3)
					} else {
						failure := fixture.ImplicitError(query, fixture.Diagnostic{Code: "0A000", Message: message, Position: fixture.Position(len([]rune(query[:strings.Index(query, token)])) + 1)})
						failure.Prepared = prepared
						failure.ErrorStage = "execute"
						run(failure)
						run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
						run(fixture.Exec("ALTER TABLE t ADD b INT DEFAULT 7", 0))
						run(fixture.Select("SELECT b FROM t ORDER BY id", []string{"b"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}))
					}
				})
			})
		}
	}
}
