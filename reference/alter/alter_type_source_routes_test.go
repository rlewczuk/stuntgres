package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceAlterTypeConversionsSourceConsumerRoutes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range alterTypeConversionCases {
			t.Run(tc.name+fmt.Sprint(prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						run := func(s fixture.Step) { s.NonContext = true; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
						run(fixture.Exec("CREATE TABLE t(id INT,c "+tc.declaration+")", 0))
						if tc.seed != "" {
							// Build the ordered row source without depending on backend row order.
							seeds := alterTypeSeeds(tc.seed)
							for i, v := range seeds {
								run(fixture.Exec(fmt.Sprintf("INSERT INTO t VALUES(%d,%s)", i+1, v), 1))
							}
						}
						q := "ALTER TABLE t ALTER COLUMN c " + tc.action
						s := fixture.Exec(q, 0)
						if tc.want.Code != "" {
							s = fixture.ImplicitError(q, tc.want)
							s.ErrorStage = "execute"
						}
						s.Prepared = prepared
						run(s)
						run(fixture.Select("SELECT c FROM t ORDER BY id", []string{"c"}, []string{tc.typ}, tc.rows))
						if tc.name == "numeric-overflow" {
							rows, err := b.DB.Query("SELECT c FROM t")
							if err != nil {
								t.Fatal(err)
							}
							metadata, err := rows.ColumnTypes()
							rows.Close()
							if err != nil {
								t.Fatal(err)
							}
							if precision, scale, ok := metadata[0].DecimalSize(); !ok || precision != 8 || scale != 3 {
								t.Fatal("failed TYPE changed numeric usage", precision, scale, ok)
							}
						}
						if tc.name == "default" {
							run(fixture.Exec("INSERT INTO t(id) VALUES(2)", 1))
							run(fixture.Select("SELECT c FROM t ORDER BY id", []string{"c"}, []string{"INT8"}, [][]any{{int64(8)}, {int64(7)}}))
						}
					})
				}
			})
		}
	}
}

func TestReferenceAlterTypeOrderedTransformsSourceConsumerRoutes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			name, declaration, seed, action string
			names, types                    []string
			rows                            [][]any
			want                            fixture.Diagnostic
		}{
			{
				name: "identity-first-division", declaration: "c INT", seed: "(7)",
				action: "ALTER c TYPE INT USING 10/(c-c), ALTER c TYPE BIGINT USING c",
				names:  []string{"c"}, types: []string{"INT4"}, rows: [][]any{{int64(7)}},
				want: fixture.Diagnostic{Code: "22012", Message: "division by zero"},
			},
			{
				name: "collation-first-division", declaration: "c TEXT", seed: "('7')",
				action: `ALTER c TYPE TEXT COLLATE "C" USING (10/(c::int-c::int))::text, ALTER c TYPE VARCHAR(5) COLLATE "POSIX" USING c`,
				names:  []string{"c"}, types: []string{"TEXT"}, rows: [][]any{{"7"}},
				want: fixture.Diagnostic{Code: "22012", Message: "division by zero"},
			},
			{
				name: "action-order", declaration: "a TEXT,b TEXT", seed: "('badA','badB')",
				action: "ALTER b TYPE INT USING b::int, ALTER a TYPE INT USING a::int",
				names:  []string{"a", "b"}, types: []string{"TEXT", "TEXT"}, rows: [][]any{{"badA", "badB"}},
				want: fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "badB"`},
			},
		} {
			t.Run(tc.name+fmt.Sprint(prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						run := func(s fixture.Step) { s.NonContext = true; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
						run(fixture.Exec("CREATE TABLE t("+tc.declaration+")", 0))
						run(fixture.Exec("INSERT INTO t VALUES"+tc.seed, 1))
						s := fixture.ImplicitError("ALTER TABLE t "+tc.action, tc.want)
						s.ErrorStage = "execute"
						s.Prepared = prepared
						run(s)
						run(fixture.Select("SELECT * FROM t", tc.names, tc.types, tc.rows))
					})
				}
			})
		}
	}
}
