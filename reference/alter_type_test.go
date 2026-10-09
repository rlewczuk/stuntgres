package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceAlterTypeConversions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range alterTypeConversionCases {
			t.Run(tc.name+fmt.Sprint(prepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
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
							rows, err := b.db.QueryContext(p.ctx, "SELECT c FROM t")
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

func alterTypeSeeds(s string) []string {
	// All seeds above are scalar literals without embedded commas.
	var out []string
	start := 0
	for i, c := range s {
		if c == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func TestReferenceAlterTypeActionListsAndViews(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec("CREATE TABLE t(a INT,b INT,c INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(2,7,9)", 1))
					s := referenceExec("ALTER TABLE t ALTER a TYPE text USING b, ALTER b TYPE bigint USING a*10, DROP c", 0)
					s.prepared = prepared
					run(s)
					run(referenceSelect("SELECT * FROM t", []string{"a", "b"}, []string{"TEXT", "INT8"}, [][]any{{"7", int64(20)}}))
					run(referenceExec("CREATE TABLE u(a INT,b INT,c INT)", 0))
					run(referenceExec("INSERT INTO u VALUES(2,7,9)", 1))
					run(referenceExec("ALTER TABLE u ALTER a TYPE text USING c, DROP c", 0))
					run(referenceSelect("SELECT * FROM u", []string{"a", "b"}, []string{"TEXT", "INT4"}, [][]any{{"9", int64(7)}}))
					run(referenceExec("CREATE VIEW v AS SELECT b FROM u", 0))
					for _, target := range []string{"INT", "BIGINT"} {
						run(referenceImplicitError("ALTER TABLE u ALTER b TYPE "+target, referenceDiagnostic{Code: "0A000", Message: "cannot alter type of a column used by a view or rule", Detail: `rule _RETURN on view v depends on column "b"`}))
					}
					run(referenceExec("ALTER TABLE u ALTER a TYPE varchar(4)", 0))
					run(referenceSelect("SELECT * FROM v", []string{"b"}, []string{"INT4"}, [][]any{{int64(7)}}))
					run(referenceExec("CREATE TABLE n(c INT NOT NULL)", 0))
					run(referenceExec("INSERT INTO n VALUES(7)", 1))
					run(referenceExec("ALTER TABLE n ALTER c TYPE INT USING NULL, ALTER c DROP NOT NULL", 0))
					run(referenceSelect("SELECT * FROM n", []string{"c"}, []string{"INT4"}, [][]any{{nil}}))
				})
			}
		})
	}
}

func TestReferenceAlterTypeOrderedTransforms(t *testing.T) {
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
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
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
