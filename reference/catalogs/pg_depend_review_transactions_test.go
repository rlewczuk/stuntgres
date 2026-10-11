package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func pgDependReviewBackends(t *testing.T, p *fixture.Pair, fn func(*testing.T, fixture.Backend)) {
	t.Helper()
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { fn(t, b) })
	}
}

func TestReferencePGDependReviewNestedOwnedCascade(t *testing.T) {
	for _, mode := range []string{"Autocommit", "ReadCommitted", "RepeatableRead", "OverlapAutocommit", "OverlapReadCommitted", "OverlapRepeatableRead"} {
		t.Run(mode, func(t *testing.T) {
			overlap := strings.HasPrefix(mode, "Overlap")
			mode = strings.TrimPrefix(mode, "Overlap")
			p := fixture.NewScenario(t)
			pgDependReviewBackends(t, p, func(t *testing.T, b fixture.Backend) {
				nestedExpression := "'nested_owned'::regclass IS NOT NULL"
				if overlap {
					nestedExpression = "('nested_root'::regclass IS NOT NULL) AND (" + nestedExpression + ")"
				}
				fixture.PgDependExec(t, p, b, "CREATE SEQUENCE nested_root", "CREATE SEQUENCE nested_owned",
					"CREATE TABLE nested_first(id INT,payload INT,g BOOL GENERATED ALWAYS AS('nested_root'::regclass IS NOT NULL) STORED,h BOOL GENERATED ALWAYS AS("+nestedExpression+") STORED)",
					"ALTER SEQUENCE nested_owned OWNED BY nested_first.g",
					"CREATE TABLE nested_second(id INT,payload INT,g BOOL GENERATED ALWAYS AS("+nestedExpression+") STORED)",
					"CREATE INDEX nested_first_keep ON nested_first(id)", "CREATE INDEX nested_second_keep ON nested_second(id)",
					"CREATE INDEX nested_generated_index ON nested_first(g)", "CREATE VIEW nested_generated_view AS SELECT g FROM nested_second")
				for _, name := range []string{"nested_first", "nested_second"} {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO "+name+"(id,payload) VALUES(1,10),(2,20)", 2))
				}
				first := fixture.PgDependRelation(t, b, "nested_first")
				second := fixture.PgDependRelation(t, b, "nested_second")
				root := fixture.PgDependRelation(t, b, "nested_root")
				owned := fixture.PgDependRelation(t, b, "nested_owned")
				ns := fixture.PgDependNamespace(t, b, root)
				fg := fixture.PgDependDefinition(t, b, first, 3)
				fh := fixture.PgDependDefinition(t, b, first, 4)
				sg := fixture.PgDependDefinition(t, b, second, 3)
				ix := fixture.PgDependRelation(t, b, "nested_generated_index")
				view := fixture.PgDependRelation(t, b, "nested_generated_view")
				rule := fixture.PgDependRule(t, b, view)
				keepFirst := fixture.PgDependRelation(t, b, "nested_first_keep")
				keepSecond := fixture.PgDependRelation(t, b, "nested_second_keep")
				bag := func(x fixture.Executor, class, id int64, want ...[]any) {
					t.Helper()
					fixture.PgDescriptionExecutorRows(t, p, b, x, fixture.PgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=$1 AND objid=$2", want, class, id))
				}
				graph := func(x fixture.Executor, present bool) {
					if present {
						bag(x, 1259, root, fixture.PgDependEdge(1259, root, 0, 2615, ns, 0, "n"))
						bag(x, 1259, owned, fixture.PgDependEdge(1259, owned, 0, 2615, ns, 0, "n"), fixture.PgDependEdge(1259, owned, 0, 1259, first, 3, "a"))
						bag(x, 2604, fg, fixture.PgDependEdge(2604, fg, 0, 1259, first, 3, "i"), fixture.PgDependEdge(2604, fg, 0, 1259, root, 0, "n"))
						firstNested := [][]any{fixture.PgDependEdge(2604, fh, 0, 1259, first, 4, "i"), fixture.PgDependEdge(2604, fh, 0, 1259, owned, 0, "n")}
						secondNested := [][]any{fixture.PgDependEdge(2604, sg, 0, 1259, second, 3, "i"), fixture.PgDependEdge(2604, sg, 0, 1259, owned, 0, "n")}
						if overlap {
							firstNested = append(firstNested, fixture.PgDependEdge(2604, fh, 0, 1259, root, 0, "n"))
							secondNested = append(secondNested, fixture.PgDependEdge(2604, sg, 0, 1259, root, 0, "n"))
						}
						bag(x, 2604, fh, firstNested...)
						bag(x, 2604, sg, secondNested...)
						bag(x, 1259, ix, fixture.PgDependEdge(1259, ix, 0, 1259, first, 3, "a"))
						bag(x, 1259, view, fixture.PgDependEdge(1259, view, 0, 2615, ns, 0, "n"))
						bag(x, 2618, rule, fixture.PgDependEdge(2618, rule, 0, 1259, view, 0, "i"), fixture.PgDependEdge(2618, rule, 0, 1259, second, 3, "n"))
					} else {
						for _, item := range [][2]int64{{1259, root}, {1259, owned}, {2604, fg}, {2604, fh}, {2604, sg}, {1259, ix}, {1259, view}, {2618, rule}} {
							bag(x, item[0], item[1])
						}
					}
					bag(x, 1259, first, fixture.PgDependEdge(1259, first, 0, 2615, ns, 0, "n"))
					bag(x, 1259, second, fixture.PgDependEdge(1259, second, 0, 2615, ns, 0, "n"))
					bag(x, 1259, keepFirst, fixture.PgDependEdge(1259, keepFirst, 0, 1259, first, 1, "a"))
					bag(x, 1259, keepSecond, fixture.PgDependEdge(1259, keepSecond, 0, 1259, second, 1, "a"))
				}
				rows := func(x fixture.Executor, generated bool, want [][]any) {
					for _, name := range []string{"nested_first", "nested_second"} {
						columns := []string{"id", "payload"}
						types := []string{"INT4", "INT4"}
						q := "SELECT id,payload"
						expected := want
						if generated {
							q += ",g"
							columns = append(columns, "g")
							types = append(types, "BOOL")
							expected = nil
							for _, row := range want {
								r := append([]any(nil), row...)
								r = append(r, true)
								expected = append(expected, r)
							}
							if name == "nested_first" {
								q += ",h"
								columns = append(columns, "h")
								types = append(types, "BOOL")
								for i := range expected {
									expected[i] = append(expected[i], true)
								}
							}
						}
						fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(q+" FROM "+name+" ORDER BY id", columns, types, expected))
					}
				}
				initial := [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}
				private := [][]any{{int64(2), int64(20)}, {int64(3), int64(30)}}
				graph(b.DB, true)
				rows(b.DB, true, initial)
				if mode == "Autocommit" {
					fixture.PgDependExec(t, p, b, "DROP SEQUENCE nested_root CASCADE")
					graph(b.DB, false)
					rows(b.DB, false, initial)
					return
				}
				_, owner, peer := fixture.IsolationReferenceConnections(t, p, b.PG)
				defer owner.Close()
				defer peer.Close()
				options := &sql.TxOptions{Isolation: sql.LevelReadCommitted}
				if mode == "RepeatableRead" {
					options.Isolation = sql.LevelRepeatableRead
				}
				tx, err := owner.BeginTx(p.Ctx, options)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				graph(tx, true)
				rows(tx, true, initial)
				peerRows := initial
				if mode == "RepeatableRead" {
					for _, name := range []string{"nested_first", "nested_second"} {
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("INSERT INTO "+name+"(id,payload) VALUES(4,40)", 1))
					}
					peerRows = append(append([][]any(nil), initial...), []any{int64(4), int64(40)})
				}
				for _, name := range []string{"nested_first", "nested_second"} {
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("INSERT INTO "+name+"(id,payload) VALUES(3,30)", 1))
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("DELETE FROM "+name+" WHERE id=1", 1))
				}
				rows(peer, true, peerRows)
				rows(tx, true, private)
				for attempt := 0; attempt < 2; attempt++ {
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SAVEPOINT nested_before", 0))
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("DROP SEQUENCE nested_root CASCADE", 0))
					graph(tx, false)
					graph(peer, true)
					rows(tx, false, private)
					if attempt == 0 {
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("ROLLBACK TO nested_before", 0))
						graph(tx, true)
						rows(tx, true, private)
					}
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				graph(peer, false)
				final := private
				if mode == "RepeatableRead" {
					final = append(append([][]any(nil), private...), []any{int64(4), int64(40)})
				}
				rows(peer, false, final)
			})
		})
	}
}
