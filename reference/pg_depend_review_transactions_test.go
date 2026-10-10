package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func pgDependReviewBackends(t *testing.T, p *referencePair, fn func(*testing.T, referenceBackend)) {
	t.Helper()
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) { fn(t, b) })
	}
}

func TestReferencePGDependReviewNestedOwnedCascade(t *testing.T) {
	for _, mode := range []string{"Autocommit", "ReadCommitted", "RepeatableRead", "OverlapAutocommit", "OverlapReadCommitted", "OverlapRepeatableRead"} {
		t.Run(mode, func(t *testing.T) {
			overlap := strings.HasPrefix(mode, "Overlap")
			mode = strings.TrimPrefix(mode, "Overlap")
			p := newReferenceScenario(t)
			pgDependReviewBackends(t, p, func(t *testing.T, b referenceBackend) {
				nestedExpression := "'nested_owned'::regclass IS NOT NULL"
				if overlap {
					nestedExpression = "('nested_root'::regclass IS NOT NULL) AND (" + nestedExpression + ")"
				}
				pgDependExec(t, p, b, "CREATE SEQUENCE nested_root", "CREATE SEQUENCE nested_owned",
					"CREATE TABLE nested_first(id INT,payload INT,g BOOL GENERATED ALWAYS AS('nested_root'::regclass IS NOT NULL) STORED,h BOOL GENERATED ALWAYS AS("+nestedExpression+") STORED)",
					"ALTER SEQUENCE nested_owned OWNED BY nested_first.g",
					"CREATE TABLE nested_second(id INT,payload INT,g BOOL GENERATED ALWAYS AS("+nestedExpression+") STORED)",
					"CREATE INDEX nested_first_keep ON nested_first(id)", "CREATE INDEX nested_second_keep ON nested_second(id)",
					"CREATE INDEX nested_generated_index ON nested_first(g)", "CREATE VIEW nested_generated_view AS SELECT g FROM nested_second")
				for _, name := range []string{"nested_first", "nested_second"} {
					referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO "+name+"(id,payload) VALUES(1,10),(2,20)", 2))
				}
				first := pgDependRelation(t, b, "nested_first")
				second := pgDependRelation(t, b, "nested_second")
				root := pgDependRelation(t, b, "nested_root")
				owned := pgDependRelation(t, b, "nested_owned")
				ns := pgDependNamespace(t, b, root)
				fg := pgDependDefinition(t, b, first, 3)
				fh := pgDependDefinition(t, b, first, 4)
				sg := pgDependDefinition(t, b, second, 3)
				ix := pgDependRelation(t, b, "nested_generated_index")
				view := pgDependRelation(t, b, "nested_generated_view")
				rule := pgDependRule(t, b, view)
				keepFirst := pgDependRelation(t, b, "nested_first_keep")
				keepSecond := pgDependRelation(t, b, "nested_second_keep")
				bag := func(x referenceExecutor, class, id int64, want ...[]any) {
					t.Helper()
					pgDescriptionExecutorRows(t, p, b, x, pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=$1 AND objid=$2", want, class, id))
				}
				graph := func(x referenceExecutor, present bool) {
					if present {
						bag(x, 1259, root, pgDependEdge(1259, root, 0, 2615, ns, 0, "n"))
						bag(x, 1259, owned, pgDependEdge(1259, owned, 0, 2615, ns, 0, "n"), pgDependEdge(1259, owned, 0, 1259, first, 3, "a"))
						bag(x, 2604, fg, pgDependEdge(2604, fg, 0, 1259, first, 3, "i"), pgDependEdge(2604, fg, 0, 1259, root, 0, "n"))
						firstNested := [][]any{pgDependEdge(2604, fh, 0, 1259, first, 4, "i"), pgDependEdge(2604, fh, 0, 1259, owned, 0, "n")}
						secondNested := [][]any{pgDependEdge(2604, sg, 0, 1259, second, 3, "i"), pgDependEdge(2604, sg, 0, 1259, owned, 0, "n")}
						if overlap {
							firstNested = append(firstNested, pgDependEdge(2604, fh, 0, 1259, root, 0, "n"))
							secondNested = append(secondNested, pgDependEdge(2604, sg, 0, 1259, root, 0, "n"))
						}
						bag(x, 2604, fh, firstNested...)
						bag(x, 2604, sg, secondNested...)
						bag(x, 1259, ix, pgDependEdge(1259, ix, 0, 1259, first, 3, "a"))
						bag(x, 1259, view, pgDependEdge(1259, view, 0, 2615, ns, 0, "n"))
						bag(x, 2618, rule, pgDependEdge(2618, rule, 0, 1259, view, 0, "i"), pgDependEdge(2618, rule, 0, 1259, second, 3, "n"))
					} else {
						for _, item := range [][2]int64{{1259, root}, {1259, owned}, {2604, fg}, {2604, fh}, {2604, sg}, {1259, ix}, {1259, view}, {2618, rule}} {
							bag(x, item[0], item[1])
						}
					}
					bag(x, 1259, first, pgDependEdge(1259, first, 0, 2615, ns, 0, "n"))
					bag(x, 1259, second, pgDependEdge(1259, second, 0, 2615, ns, 0, "n"))
					bag(x, 1259, keepFirst, pgDependEdge(1259, keepFirst, 0, 1259, first, 1, "a"))
					bag(x, 1259, keepSecond, pgDependEdge(1259, keepSecond, 0, 1259, second, 1, "a"))
				}
				rows := func(x referenceExecutor, generated bool, want [][]any) {
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
						referenceRunStep(t, p.ctx, x, b.recorder, referenceSelect(q+" FROM "+name+" ORDER BY id", columns, types, expected))
					}
				}
				initial := [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}
				private := [][]any{{int64(2), int64(20)}, {int64(3), int64(30)}}
				graph(b.db, true)
				rows(b.db, true, initial)
				if mode == "Autocommit" {
					pgDependExec(t, p, b, "DROP SEQUENCE nested_root CASCADE")
					graph(b.db, false)
					rows(b.db, false, initial)
					return
				}
				_, owner, peer := isolationReferenceConnections(t, p, b.pg)
				defer owner.Close()
				defer peer.Close()
				options := &sql.TxOptions{Isolation: sql.LevelReadCommitted}
				if mode == "RepeatableRead" {
					options.Isolation = sql.LevelRepeatableRead
				}
				tx, err := owner.BeginTx(p.ctx, options)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				graph(tx, true)
				rows(tx, true, initial)
				peerRows := initial
				if mode == "RepeatableRead" {
					for _, name := range []string{"nested_first", "nested_second"} {
						referenceRunStep(t, p.ctx, peer, b.recorder, referenceExec("INSERT INTO "+name+"(id,payload) VALUES(4,40)", 1))
					}
					peerRows = append(append([][]any(nil), initial...), []any{int64(4), int64(40)})
				}
				for _, name := range []string{"nested_first", "nested_second"} {
					referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("INSERT INTO "+name+"(id,payload) VALUES(3,30)", 1))
					referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("DELETE FROM "+name+" WHERE id=1", 1))
				}
				rows(peer, true, peerRows)
				rows(tx, true, private)
				for attempt := 0; attempt < 2; attempt++ {
					referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("SAVEPOINT nested_before", 0))
					referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("DROP SEQUENCE nested_root CASCADE", 0))
					graph(tx, false)
					graph(peer, true)
					rows(tx, false, private)
					if attempt == 0 {
						referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("ROLLBACK TO nested_before", 0))
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
