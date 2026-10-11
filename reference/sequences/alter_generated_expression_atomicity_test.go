package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterGeneratedExpressionAtomicity(t *testing.T) {
	cases := []struct {
		name, columns, insert, expr string
		want                        fixture.Diagnostic
		rows                        [][]any
	}{
		{"overflow", `a INT,b INT GENERATED ALWAYS AS(a+1) STORED`, `INSERT INTO g(a) VALUES(1),(2147483646)`, `a+2`, fixture.Diagnostic{Code: "22003", Message: "integer out of range"}, [][]any{{int64(1), int64(2)}, {int64(2147483646), int64(2147483647)}}},
		{"division", `a INT,b INT GENERATED ALWAYS AS(a+1) STORED`, `INSERT INTO g(a) VALUES(1),(2)`, `a/(2-a)`, fixture.Diagnostic{Code: "22012", Message: "division by zero"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(3)}}},
		{"check", `a INT,b INT GENERATED ALWAYS AS(a+1) STORED,CONSTRAINT ck CHECK(b<5)`, `INSERT INTO g(a) VALUES(2)`, `a+3`, fixture.Diagnostic{Code: "23514", Message: `check constraint "ck" of relation "g" is violated by some row`, Schema: "public", Table: "g", Constraint: "ck"}, [][]any{{int64(2), int64(3)}}},
		{"not-null", `a INT,b INT GENERATED ALWAYS AS(a+1) STORED NOT NULL`, `INSERT INTO g(a) VALUES(2)`, `NULL::INT`, fixture.Diagnostic{Code: "23502", Message: `column "b" of relation "g" contains null values`, Schema: "public", Table: "g", Column: "b"}, [][]any{{int64(2), int64(3)}}},
		{"unique", `a INT,b INT GENERATED ALWAYS AS(a+1) STORED,CONSTRAINT uq UNIQUE(b)`, `INSERT INTO g(a) VALUES(1),(2)`, `1`, fixture.Diagnostic{Code: "23505", Message: `could not create unique index "uq"`, Detail: "Key (b)=(1) is duplicated.", Schema: "public", Table: "g", Constraint: "uq"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(3)}}},
		{"exclusion", `a INT,b INT GENERATED ALWAYS AS(a+1) STORED,CONSTRAINT ex EXCLUDE USING btree(b WITH =)`, `INSERT INTO g(a) VALUES(1),(2)`, `1`, fixture.Diagnostic{Code: "23P01", Message: `could not create exclusion constraint "ex"`, Detail: "Key (b)=(1) conflicts with key (b)=(1).", Schema: "public", Table: "g", Constraint: "ex"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(3)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec(`CREATE TABLE g(`+tc.columns+`)`, 0))
				run(fixture.Exec(tc.insert, int64(len(tc.rows))))
				run(fixture.ImplicitError(`ALTER TABLE g ALTER b SET EXPRESSION AS(`+tc.expr+`)`, tc.want))
				run(alterGeneratedRows(`SELECT a,b FROM g ORDER BY a`, tc.rows...))
				run(fixture.Exec(`INSERT INTO g(a) VALUES(0)`, 1))
				run(alterGeneratedRows(`SELECT a,b FROM g WHERE a=0`, []any{int64(0), int64(1)}))
			})
		})
	}
	for _, actions := range []string{`ALTER b SET EXPRESSION AS(a+2), ALTER c SET NOT NULL`, `ALTER b SET EXPRESSION AS(a+2), ALTER b DROP EXPRESSION`, `ALTER b DROP EXPRESSION, ALTER b SET EXPRESSION AS(a+2)`} {
		t.Run(actions, func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec(`CREATE TABLE g(a INT,c INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
				run(fixture.Exec(`INSERT INTO g(a) VALUES(2)`, 1))
				d := fixture.Diagnostic{Code: "55000", Message: `column "b" of relation "g" is not a generated column`}
				if actions == `ALTER b SET EXPRESSION AS(a+2), ALTER c SET NOT NULL` {
					d = fixture.Diagnostic{Code: "23502", Message: `column "c" of relation "g" contains null values`, Schema: "public", Table: "g", Column: "c"}
				}
				run(fixture.ImplicitError(`ALTER TABLE g `+actions, d))
				run(alterGeneratedRows(`SELECT a,b FROM g`, []any{int64(2), int64(3)}))
				run(fixture.Exec(`INSERT INTO g(a) VALUES(4)`, 1))
				run(alterGeneratedRows(`SELECT a,b FROM g WHERE a=4`, []any{int64(4), int64(5)}))
			})
		})
	}
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(z INT,a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(2)`, 1))
		run(fixture.Exec(`ALTER TABLE g ALTER b SET EXPRESSION AS(a+c), ADD c INT DEFAULT 5, DROP z`, 0))
		run(fixture.Select(`SELECT * FROM g`, []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(7), int64(5)}}))
		run(fixture.Exec(`CREATE TABLE completed_type(a INT,c INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
		run(fixture.Exec(`INSERT INTO completed_type(a,c) VALUES(2,3)`, 1))
		run(fixture.Exec(`ALTER TABLE completed_type ALTER b SET EXPRESSION AS(a+c::INT), ALTER c TYPE BIGINT USING c+5`, 0))
		run(fixture.Select(`SELECT * FROM completed_type`, []string{"a", "c", "b"}, []string{"INT4", "INT8", "INT4"}, [][]any{{int64(2), int64(8), int64(10)}}))
	})
	for _, side := range []string{"parent", "child"} {
		t.Run("foreign-key/"+side, func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec(`CREATE TABLE parent(a INT,b INT GENERATED ALWAYS AS(a+1) STORED UNIQUE)`, 0))
				run(fixture.Exec(`CREATE TABLE child(a INT,b INT GENERATED ALWAYS AS(a+1) STORED,CONSTRAINT fk FOREIGN KEY(b) REFERENCES parent(b))`, 0))
				run(fixture.Exec(`INSERT INTO parent(a) VALUES(2)`, 1))
				run(fixture.Exec(`INSERT INTO child(a) VALUES(2)`, 1))
				d := fixture.Diagnostic{Code: "23503", Message: `insert or update on table "child" violates foreign key constraint "fk"`, Detail: `Key (b)=(3) is not present in table "parent".`, Schema: "public", Table: "child", Constraint: "fk"}
				if side == "child" {
					d.Detail = `Key (b)=(4) is not present in table "parent".`
				}
				run(fixture.ImplicitError(`ALTER TABLE `+side+` ALTER b SET EXPRESSION AS(a+2)`, d))
				run(alterGeneratedRows(`SELECT * FROM parent`, []any{int64(2), int64(3)}))
				run(alterGeneratedRows(`SELECT * FROM child`, []any{int64(2), int64(3)}))
				run(fixture.Exec(`INSERT INTO parent(a) VALUES(4)`, 1))
				run(fixture.Exec(`INSERT INTO child(a) VALUES(4)`, 1))
			})
		})
	}
	for _, predicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "expression-index", true: "predicate-index"}[predicate], func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
				run(fixture.Exec(`INSERT INTO g(a) VALUES(1),(2)`, 2))
				index := `CREATE UNIQUE INDEX ix ON g((b%2))`
				expr := `a*2`
				detail := `Key ((b % 2))=(0) is duplicated.`
				if predicate {
					index = `CREATE UNIQUE INDEX ix ON g((b%2)) WHERE b>2`
					expr = `a*2+2`
				}
				if b.PG {
					run(fixture.Exec(index, 0))
					run(fixture.ImplicitError(`ALTER TABLE g ALTER b SET EXPRESSION AS(`+expr+`)`, fixture.Diagnostic{Code: "23505", Message: `could not create unique index "ix"`, Detail: detail, Schema: "public", Table: "g", Constraint: "ix"}))
				} else {
					run(fixture.ImplicitError(index, fixture.Diagnostic{Code: "0A000", Message: "unique expression indexes are unsupported", Position: fixture.Position(strings.Index(index, "(b%2)") + 1)}))
					run(fixture.Select(`SELECT indexname FROM pg_indexes WHERE indexname='ix'`, []string{"indexname"}, []string{"NAME"}, nil))
				}
				run(alterGeneratedRows(`SELECT * FROM g ORDER BY a`, []any{int64(1), int64(2)}, []any{int64(2), int64(3)}))
			})
		})
	}
	for _, predicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonunique-expression-root", true: "column-key-predicate-root"}[predicate], func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED,k INT DEFAULT 0)`, 0))
				run(fixture.Exec(`INSERT INTO g(a) VALUES(1),(2)`, 2))
				index := `CREATE INDEX ix ON g((b/(b-5)))`
				expr := `5`
				want := fixture.Diagnostic{Code: "22012", Message: "division by zero"}
				if predicate {
					index = `CREATE UNIQUE INDEX ix ON g(k) WHERE b<3`
					expr = `2`
					want = fixture.Diagnostic{Code: "23505", Message: `could not create unique index "ix"`, Detail: `Key (k)=(0) is duplicated.`, Schema: "public", Table: "g", Constraint: "ix"}
				}
				run(fixture.Exec(index, 0))
				run(fixture.ImplicitError(`ALTER TABLE g ALTER b SET EXPRESSION AS(`+expr+`)`, want))
				run(alterGeneratedRows(`SELECT a,b FROM g ORDER BY a`, []any{int64(1), int64(2)}, []any{int64(2), int64(3)}))
				run(fixture.Exec(`INSERT INTO g(a) VALUES(3)`, 1))
				run(alterGeneratedRows(`SELECT a,b FROM g ORDER BY a`, []any{int64(1), int64(2)}, []any{int64(2), int64(3)}, []any{int64(3), int64(4)}))
			})
		})
	}
}
