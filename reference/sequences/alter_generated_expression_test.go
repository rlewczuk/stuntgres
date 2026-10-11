package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func alterGeneratedRows(q string, rows ...[]any) fixture.Step {
	return fixture.Select(q, []string{"a", "b"}, []string{"INT4", "INT4"}, rows)
}

func TestReferenceAlterGeneratedExpressionStored(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED,c INT GENERATED ALWAYS AS(a*2) STORED)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(2),(NULL)`, 2))
		run(fixture.Exec(`ALTER TABLE public.g * ALTER b SET EXPRESSION AS(a+2)`, 0))
		run(fixture.Select(`SELECT * FROM g ORDER BY a NULLS LAST`, []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(4), int64(4)}, {nil, nil, nil}}))
		run(fixture.Prepared(fixture.Exec(`UPDATE g SET a=8 WHERE a=2`, 1)))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(4)`, 1))
		run(alterGeneratedRows(`SELECT a,b FROM g ORDER BY a NULLS LAST`, []any{int64(4), int64(6)}, []any{int64(8), int64(10)}, []any{nil, nil}))
		run(fixture.Exec(`ALTER TABLE IF EXISTS g ALTER COLUMN b SET EXPRESSION AS(7), ALTER c SET EXPRESSION AS(a+3)`, 0))
		run(fixture.Select(`SELECT * FROM g ORDER BY a NULLS LAST`, []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(4), int64(7), int64(7)}, {int64(8), int64(7), int64(11)}, {nil, int64(7), nil}}))
	})
}

func TestReferenceAlterGeneratedExpressionDrop(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(z INT,a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(2)`, 1))
		run(fixture.Exec(`ALTER TABLE g DROP COLUMN z, ALTER b SET EXPRESSION AS(a+2)`, 0))
		run(fixture.Exec(`ALTER TABLE g RENAME COLUMN b TO "β"`, 0))
		run(fixture.Exec(`ALTER TABLE g ALTER "β" DROP EXPRESSION`, 0))
		run(fixture.Select(`SELECT * FROM g`, []string{"a", "β"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(4)}}))
		run(fixture.Exec(`ALTER TABLE g RENAME COLUMN "β" TO b`, 0))
		run(fixture.Exec(`UPDATE g SET a=9`, 1))
		run(alterGeneratedRows(`SELECT * FROM g`, []any{int64(9), int64(4)}))
		run(fixture.Prepared(fixture.Exec(`UPDATE g SET b=66`, 1)))
		run(alterGeneratedRows(`SELECT * FROM g`, []any{int64(9), int64(66)}))
		if b.PG {
			run(fixture.Exec(`UPDATE g SET b=DEFAULT`, 1))
		} else {
			q := `UPDATE g SET b=DEFAULT`
			run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "UPDATE DEFAULT is unsupported", Position: fixture.Position(strings.Index(q, "DEFAULT") + 1)}))
			run(alterGeneratedRows(`SELECT * FROM g`, []any{int64(9), int64(66)}))
		}
		run(fixture.Prepared(fixture.Exec(`UPDATE g SET b=NULL`, 1)))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(5)`, 1))
		run(alterGeneratedRows(`SELECT * FROM g ORDER BY a`, []any{int64(5), nil}, []any{int64(9), nil}))
		run(fixture.Exec(`ALTER TABLE g ALTER b SET DEFAULT 7`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(6)`, 1))
		run(alterGeneratedRows(`SELECT * FROM g WHERE a=6`, []any{int64(6), int64(7)}))
		run(fixture.Exec(`ALTER TABLE g DROP COLUMN a`, 0))
		run(fixture.Select(`SELECT b FROM g ORDER BY b NULLS LAST`, []string{"b"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}, {nil}}))
	})
}

func TestReferenceAlterGeneratedExpressionPrepared(t *testing.T) {
	for _, kind := range []string{"STORED", "VIRTUAL"} {
		t.Run(kind, func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec(`CREATE TABLE g(a INT NOT NULL,b INT GENERATED ALWAYS AS(a+1) `+kind+`)`, 0))
				run(fixture.Exec(`CREATE VIEW gv AS SELECT a,b FROM g`, 0))
				viewSQL := `SELECT a,b FROM gv ORDER BY a`
				viewRead, err := b.DB.PrepareContext(p.Ctx, viewSQL)
				if err != nil {
					t.Fatal(err)
				}
				defer viewRead.Close()
				viewRows := func(rows ...[]any) {
					t.Helper()
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: viewRead, SQL: viewSQL}, b.Recorder, alterGeneratedRows(viewSQL, rows...))
				}
				read, err := b.DB.PrepareContext(p.Ctx, `SELECT a,b FROM g ORDER BY a`)
				if err != nil {
					t.Fatal(err)
				}
				defer read.Close()
				insert, err := b.DB.PrepareContext(p.Ctx, `INSERT INTO g(a) VALUES($1)`)
				if err != nil {
					t.Fatal(err)
				}
				defer insert.Close()
				update, err := b.DB.PrepareContext(p.Ctx, `UPDATE g SET a=$1 WHERE a=$2`)
				if err != nil {
					t.Fatal(err)
				}
				defer update.Close()
				defaultWrite, err := b.DB.PrepareContext(p.Ctx, `UPDATE g SET b=DEFAULT WHERE a=$1`)
				if err != nil {
					t.Fatal(err)
				}
				defer defaultWrite.Close()
				check := func(want [][]any) {
					t.Helper()
					rows, err := read.QueryContext(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					types, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					fixture.TableDDLColumnTypes(t, b.PG, types, []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{false, true})
					got := make([][]any, 0)
					for rows.Next() {
						var a, v any
						if err := rows.Scan(&a, &v); err != nil {
							t.Fatal(err)
						}
						got = append(got, []any{a, v})
					}
					if rows.Err() != nil {
						t.Fatal(rows.Err())
					}
					if _, ok := fixture.CompareRows(got, want, false); !ok {
						t.Fatalf("held prepared rows=%v want=%v", got, want)
					}
				}
				check(nil)
				fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, false, `SELECT * FROM g WHERE FALSE`, []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{false, true}, 0)
				if _, err := insert.ExecContext(p.Ctx, 2); err != nil {
					t.Fatal(err)
				}
				check([][]any{{int64(2), int64(3)}})
				var oldViewRows *sql.Rows
				if kind == "VIRTUAL" && !b.PG {
					oldViewRows, err = b.DB.QueryContext(p.Ctx, viewSQL)
					if err != nil {
						t.Fatal(err)
					}
					defer oldViewRows.Close()
				}
				run(fixture.Exec(`ALTER TABLE g ALTER b SET EXPRESSION AS(a+2)`, 0))
				check([][]any{{int64(2), int64(4)}})
				if _, err := defaultWrite.ExecContext(p.Ctx, 2); err != nil {
					t.Fatal(err)
				}
				check([][]any{{int64(2), int64(4)}})
				if _, err := insert.ExecContext(p.Ctx, 4); err != nil {
					t.Fatal(err)
				}
				if _, err := update.ExecContext(p.Ctx, 8, 2); err != nil {
					t.Fatal(err)
				}
				check([][]any{{int64(4), int64(6)}, {int64(8), int64(10)}})
				run(alterGeneratedRows(`SELECT * FROM gv ORDER BY a`, []any{int64(4), int64(6)}, []any{int64(8), int64(10)}))
				viewRows([]any{int64(4), int64(6)}, []any{int64(8), int64(10)})
				if kind == "VIRTUAL" {
					run(fixture.Exec(`ALTER TABLE g ADD c INT DEFAULT 5, ALTER b SET EXPRESSION AS(a+c)`, 0))
					check([][]any{{int64(4), int64(9)}, {int64(8), int64(13)}})
					run(alterGeneratedRows(viewSQL, []any{int64(4), int64(9)}, []any{int64(8), int64(13)}))
					viewRows([]any{int64(4), int64(9)}, []any{int64(8), int64(13)})
					if oldViewRows != nil {
						fixture.OuterJoinsHeldRows(t, oldViewRows, alterGeneratedRows(viewSQL, []any{int64(2), int64(3)}))
					}
					fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, true, `SELECT * FROM g WHERE FALSE`, []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, []bool{false, true, true}, 0)
				}
				fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, true, `SELECT a,b FROM g WHERE FALSE`, []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{false, true}, 0)
				if kind == "STORED" {
					run(fixture.Exec(`ALTER TABLE g ALTER b DROP EXPRESSION`, 0))
					if _, err := update.ExecContext(p.Ctx, 9, 8); err != nil {
						t.Fatal(err)
					}
					if _, err := insert.ExecContext(p.Ctx, 5); err != nil {
						t.Fatal(err)
					}
					check([][]any{{int64(4), int64(6)}, {int64(5), nil}, {int64(9), int64(10)}})
					_, err := defaultWrite.ExecContext(p.Ctx, 9)
					if b.PG {
						if err != nil {
							t.Fatal(err)
						}
						check([][]any{{int64(4), int64(6)}, {int64(5), nil}, {int64(9), nil}})
					} else {
						want := fixture.ControlsReferenceDiagnostic("0A000", "UPDATE DEFAULT is unsupported")
						want.Position = fixture.Position(strings.Index(`UPDATE g SET b=DEFAULT WHERE a=$1`, "DEFAULT") + 1)
						fixture.AssertDiagnostic(t, err, &want)
						check([][]any{{int64(4), int64(6)}, {int64(5), nil}, {int64(9), int64(10)}})
					}
					run(fixture.Prepared(fixture.Exec(`UPDATE g SET b=66 WHERE a=9`, 1)))
					check([][]any{{int64(4), int64(6)}, {int64(5), nil}, {int64(9), int64(66)}})
				}
			})
		})
	}
}

func TestReferenceAlterGeneratedExpressionCatalog(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(a INT,c INT,b INT GENERATED ALWAYS AS(a+1) STORED,d INT GENERATED ALWAYS AS(c+1) STORED)`, 0))
		definitions := func() map[int64]int64 {
			t.Helper()
			rows, err := b.DB.QueryContext(p.Ctx, `SELECT adnum,oid FROM pg_attrdef WHERE adrelid='g'::regclass::oid ORDER BY adnum`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			m := map[int64]int64{}
			for rows.Next() {
				var n, oid int64
				if err := rows.Scan(&n, &oid); err != nil {
					t.Fatal(err)
				}
				if oid == 0 {
					t.Fatal("zero definition")
				}
				m[n] = oid
			}
			if rows.Err() != nil {
				t.Fatal(rows.Err())
			}
			return m
		}
		old := definitions()
		if len(old) != 2 || old[3] == old[4] {
			t.Fatal(old)
		}
		run(fixture.Exec(`ALTER TABLE g ALTER a DROP EXPRESSION IF EXISTS`, 0))
		noOp := definitions()
		if len(noOp) != 2 || noOp[3] != old[3] || noOp[4] != old[4] {
			t.Fatal("no-op changed definitions", old, noOp)
		}
		run(fixture.Exec(`ALTER TABLE g ALTER b SET EXPRESSION AS(c+2)`, 0))
		newIDs := definitions()
		if len(newIDs) != 2 || newIDs[3] == old[3] || newIDs[4] != old[4] {
			t.Fatal(old, newIDs)
		}
		var expr string
		if err := b.DB.QueryRowContext(p.Ctx, `SELECT pg_get_expr(adbin,adrelid) FROM pg_attrdef WHERE adrelid='g'::regclass::oid AND adnum=3`).Scan(&expr); err != nil {
			t.Fatal(err)
		}
		// Replaying the deparsed recipe establishes meaning without matching printer spacing.
		run(fixture.Exec(fmt.Sprintf(`CREATE TABLE replay(c INT,b INT GENERATED ALWAYS AS(%s) STORED)`, expr), 0))
		run(fixture.Exec(`INSERT INTO replay(c) VALUES(5)`, 1))
		run(fixture.Select(`SELECT * FROM replay`, []string{"c", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(7)}}))
		run(fixture.Exec(`ALTER TABLE g DROP a`, 0))
		run(fixture.Exec(`ALTER TABLE g ALTER b DROP EXPRESSION`, 0))
		after := definitions()
		if len(after) != 1 || after[4] != old[4] {
			t.Fatal(after)
		}
		run(fixture.Exec(`ALTER TABLE g ALTER d DROP EXPRESSION`, 0))
		run(fixture.Exec(`ALTER TABLE g DROP c`, 0))
		if len(definitions()) != 0 {
			t.Fatal("stale generation dependencies")
		}
		run(fixture.Exec(`CREATE TABLE dependency(a INT,c INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
		run(fixture.Exec(`ALTER TABLE dependency ALTER b SET EXPRESSION AS(c+2)`, 0))
		run(fixture.Exec(`ALTER TABLE dependency DROP a`, 0))
		run(fixture.ImplicitError(`ALTER TABLE dependency DROP c`, fixture.Diagnostic{Code: "2BP01", Message: "cannot drop column c of table dependency because other objects depend on it", Detail: "column b of table dependency depends on column c of table dependency", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
		run(fixture.Exec(`ALTER TABLE dependency ALTER b DROP EXPRESSION`, 0))
		run(fixture.Exec(`ALTER TABLE dependency DROP c`, 0))
	})
}
