package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMoneyIntervalSumMetadata(t *testing.T) {
	moneyIntervalSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE mis_metadata(g INTEGER,m MONEY NOT NULL,iv INTERVAL SECOND(2) NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO mis_metadata VALUES (1,'1','0.994 seconds'),(1,'2','0.994 seconds')", 2))
		for _, c := range []struct {
			q    string
			rows [][]any
		}{
			{"SELECT sum(m),sum(iv) FROM mis_metadata", [][]any{{"$3.00", "00:00:01.98"}}},
			{"SELECT sum(m) AS total_money,sum(iv) AS total_interval FROM mis_metadata", [][]any{{"$3.00", "00:00:01.98"}}},
			{"SELECT sum(m),sum(iv) FROM mis_metadata WHERE FALSE", [][]any{{nil, nil}}},
			{"SELECT sum(m),sum(iv) FROM mis_metadata WHERE FALSE GROUP BY g", [][]any{}},
			{"SELECT sum(m) FILTER(WHERE FALSE),sum(iv) FILTER(WHERE FALSE) FROM mis_metadata", [][]any{{nil, nil}}},
		} {
			columns := []string{"sum", "sum"}
			if c.q == "SELECT sum(m) AS total_money,sum(iv) AS total_interval FROM mis_metadata" {
				columns = []string{"total_money", "total_interval"}
			}
			s := fixture.Select(c.q, columns, []string{"MONEY", "INTERVAL"}, c.rows)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				if len(types) != 2 {
					t.Fatalf("types %#v", types)
				}
				for i, typ := range types {
					want := reflect.TypeOf("")
					if b.PG {
						want = reflect.TypeOf((*any)(nil)).Elem()
					}
					if typ.Name() != columns[i] || typ.DatabaseTypeName() != []string{"MONEY", "INTERVAL"}[i] || typ.ScanType() != want {
						t.Fatalf("column %d: %s %s %v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType())
					}
					if _, ok := typ.Length(); ok {
						t.Fatal("unexpected length")
					}
					if _, _, ok := typ.DecimalSize(); ok {
						t.Fatal("unexpected precision")
					}
					nullable, known := typ.Nullable()
					if b.PG {
						if known {
							t.Fatal("pq nullable must be unknown")
						}
					} else if !known || !nullable {
						t.Fatal("native sum must be known nullable")
					}
				}
			}
			s.CheckScan = func(t *testing.T, rows *sql.Rows, row int) {
				var m, iv any
				if err := rows.Scan(&m, &iv); err != nil {
					t.Fatal(err)
				}
				for _, value := range []any{m, iv} {
					if value == nil {
						continue
					}
					if b.PG {
						if _, ok := value.([]byte); !ok {
							t.Fatalf("pq carrier %T", value)
						}
					} else if _, ok := value.(string); !ok {
						t.Fatalf("native carrier %T", value)
					}
				}
			}
			moneyIntervalSumBoth(t, p, conn, b, s)
		}
		run(fixture.Exec("CREATE DOMAIN mis_money_domain AS MONEY", 0))
		run(fixture.Exec("CREATE DOMAIN mis_interval_domain AS INTERVAL", 0))
		moneyIntervalSumBoth(t, p, conn, b, moneyIntervalSumPair("SELECT sum('1'::mis_money_domain) AS m,sum('1 day'::mis_interval_domain) AS iv", [][]any{{"$1.00", "1 day"}}))
	})
}
