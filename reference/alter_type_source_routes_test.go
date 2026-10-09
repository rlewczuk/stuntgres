package reference_test

import (
	"fmt"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceAlterTypeConversionsSourceConsumerRoutes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range alterTypeConversionCases {
			t.Run(tc.name+fmt.Sprint(prepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						run := func(s referenceStep) { s.nonContext = true; referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
						run(referenceExec("CREATE TABLE t(id INT,c "+tc.declaration+")", 0))
						if tc.seed != "" {
							// Build the ordered row source without depending on backend row order.
							seeds := alterTypeSeeds(tc.seed)
							for i, v := range seeds {
								run(referenceExec(fmt.Sprintf("INSERT INTO t VALUES(%d,%s)", i+1, v), 1))
							}
						}
						q := "ALTER TABLE t ALTER COLUMN c " + tc.action
						s := referenceExec(q, 0)
						if tc.want.Code != "" {
							s = referenceImplicitError(q, tc.want)
							s.errorStage = "execute"
						}
						s.prepared = prepared
						run(s)
						run(referenceSelect("SELECT c FROM t ORDER BY id", []string{"c"}, []string{tc.typ}, tc.rows))
						if tc.name == "numeric-overflow" {
							rows, err := b.db.Query("SELECT c FROM t")
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
							run(referenceExec("INSERT INTO t(id) VALUES(2)", 1))
							run(referenceSelect("SELECT c FROM t ORDER BY id", []string{"c"}, []string{"INT8"}, [][]any{{int64(8)}, {int64(7)}}))
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
			want                            referenceDiagnostic
		}{
			{
				name: "identity-first-division", declaration: "c INT", seed: "(7)",
				action: "ALTER c TYPE INT USING 10/(c-c), ALTER c TYPE BIGINT USING c",
				names:  []string{"c"}, types: []string{"INT4"}, rows: [][]any{{int64(7)}},
				want: referenceDiagnostic{Code: "22012", Message: "division by zero"},
			},
			{
				name: "collation-first-division", declaration: "c TEXT", seed: "('7')",
				action: `ALTER c TYPE TEXT COLLATE "C" USING (10/(c::int-c::int))::text, ALTER c TYPE VARCHAR(5) COLLATE "POSIX" USING c`,
				names:  []string{"c"}, types: []string{"TEXT"}, rows: [][]any{{"7"}},
				want: referenceDiagnostic{Code: "22012", Message: "division by zero"},
			},
			{
				name: "action-order", declaration: "a TEXT,b TEXT", seed: "('badA','badB')",
				action: "ALTER b TYPE INT USING b::int, ALTER a TYPE INT USING a::int",
				names:  []string{"a", "b"}, types: []string{"TEXT", "TEXT"}, rows: [][]any{{"badA", "badB"}},
				want: referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "badB"`},
			},
		} {
			t.Run(tc.name+fmt.Sprint(prepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						run := func(s referenceStep) { s.nonContext = true; referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
						run(referenceExec("CREATE TABLE t("+tc.declaration+")", 0))
						run(referenceExec("INSERT INTO t VALUES"+tc.seed, 1))
						s := referenceImplicitError("ALTER TABLE t "+tc.action, tc.want)
						s.errorStage = "execute"
						s.prepared = prepared
						run(s)
						run(referenceSelect("SELECT * FROM t", tc.names, tc.types, tc.rows))
					})
				}
			})
		}
	}
}
