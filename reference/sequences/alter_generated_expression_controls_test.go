package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterGeneratedExpressionControls(t *testing.T) {
	var nativeDump string
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		for _, name := range []string{"s", "v", "d"} {
			kind := "STORED"
			if name == "v" {
				kind = "VIRTUAL"
			}
			constraint := ""
			if name == "s" {
				constraint = ",CONSTRAINT positive CHECK(b>0)"
			}
			run(fixture.Exec(`CREATE TABLE `+name+`(a INT,b INT GENERATED ALWAYS AS(a+1) `+kind+constraint+`)`, 0))
			run(fixture.Exec(`INSERT INTO `+name+`(a) VALUES(2)`, 1))
		}
		run(fixture.Exec(`COMMENT ON COLUMN s.b IS 'replacement persists'`, 0))
		var heldBefore func()
		if !b.PG {
			held, err := p.Native.QueryContext(p.Ctx, `SELECT * FROM s`)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			before, err := held.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			heldBefore = func() {
				t.Helper()
				after, err := held.ColumnTypes()
				if err != nil || len(after) != len(before) {
					t.Fatal(after, err)
				}
				for i := range before {
					if before[i].Name() != after[i].Name() || before[i].DatabaseTypeName() != after[i].DatabaseTypeName() || before[i].ScanType() != after[i].ScanType() {
						t.Fatal("held metadata changed")
					}
					n, ok := before[i].Nullable()
					a, aok := after[i].Nullable()
					if n != a || ok != aok {
						t.Fatal("held nullability changed")
					}
				}
				if !held.Next() {
					t.Fatal("held old row lost", held.Err())
				}
				var a, v int64
				if err := held.Scan(&a, &v); err != nil || a != 2 || v != 3 {
					t.Fatal(a, v, err)
				}
				if held.Next() || held.Err() != nil {
					t.Fatal("held old snapshot changed", held.Err())
				}
				held.Close()
			}
		}
		run(fixture.Exec(`ALTER TABLE s ALTER b SET EXPRESSION AS(a+2)`, 0))
		run(fixture.Exec(`ALTER TABLE v ALTER b SET EXPRESSION AS(a+3)`, 0))
		run(fixture.Exec(`ALTER TABLE d ALTER b DROP EXPRESSION`, 0))
		if heldBefore != nil {
			heldBefore()
		}
		assertState := func() {
			t.Helper()
			run(alterGeneratedRows(`SELECT * FROM s`, []any{int64(2), int64(4)}))
			run(alterGeneratedRows(`SELECT * FROM v`, []any{int64(2), int64(5)}))
			run(alterGeneratedRows(`SELECT * FROM d`, []any{int64(2), int64(3)}))
			fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, false, `SELECT * FROM s`, []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{true, true}, 1)
			run(fixture.Select(`SELECT col_description('s'::regclass,2) AS comment`, []string{"comment"}, []string{"TEXT"}, [][]any{{"replacement persists"}}))
		}
		assertState()
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			nativeDump = dump.String()
			if !strings.Contains(nativeDump, "STORED") || !strings.Contains(nativeDump, "VIRTUAL") {
				t.Fatal("missing current kinds", nativeDump)
			}
			failure := errors.New("alter generated controls I/O")
			if err := stuntgres.Dump(p.Ctx, p.Native, fixture.CatalogControlFailingWriter{failure}); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{failure}); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(`CREATE TABLE bad(a INT,b INT GENERATED ALWAYS AS(a+1) STORED); INSERT INTO bad VALUES(1,2);`)); err == nil {
				t.Fatal("invalid restore accepted")
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, p.Native, strings.NewReader(nativeDump)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			assertState()
			for range 2 {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(nativeDump)); err != nil {
					t.Fatal(err)
				}
				assertState()
			}
		} else if nativeDump != "" {
			// This is the actual native stream, never a hand-written substitute.
			run(fixture.Exec(`DROP TABLE s,v,d`, 0))
			if _, err := b.DB.ExecContext(p.Ctx, nativeDump); err != nil {
				t.Fatal("PostgreSQL replay of native dump", err)
			}
			assertState()
		}
		run(fixture.Exec(`INSERT INTO s(a) VALUES(4)`, 1))
		run(fixture.Exec(`INSERT INTO v(a) VALUES(4)`, 1))
		run(fixture.Exec(`INSERT INTO d(a) VALUES(4)`, 1))
		run(alterGeneratedRows(`SELECT * FROM s WHERE a=4`, []any{int64(4), int64(6)}))
		run(alterGeneratedRows(`SELECT * FROM v WHERE a=4`, []any{int64(4), int64(7)}))
		run(alterGeneratedRows(`SELECT * FROM d WHERE a=4`, []any{int64(4), nil}))
		run(fixture.Exec(`UPDATE d SET b=66 WHERE a=2`, 1))
		run(alterGeneratedRows(`SELECT * FROM d WHERE a=2`, []any{int64(2), int64(66)}))
		run(fixture.ImplicitError(`INSERT INTO s(a) VALUES(-3)`, fixture.Diagnostic{Code: "23514", Message: `new row for relation "s" violates check constraint "positive"`, Detail: "Failing row contains (-3, -1).", Schema: "public", Table: "s", Constraint: "positive"}))
	})
}

func TestReferenceAlterGeneratedExpressionFeatureSmoke(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(2)`, 1))
		stmt, err := b.DB.PrepareContext(p.Ctx, `SELECT b FROM g WHERE a=2`)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(fixture.Exec(`ALTER TABLE g ALTER b SET EXPRESSION AS(a+2)`, 0))
		var v int64
		if err := stmt.QueryRowContext(p.Ctx).Scan(&v); err != nil || v != 4 {
			t.Fatal(v, err)
		}
		fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, false, `SELECT * FROM g`, []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{true, true}, 1)
		run(fixture.Exec(`ALTER TABLE g ALTER b DROP EXPRESSION`, 0))
		run(fixture.Exec(`UPDATE g SET a=9`, 1))
		run(alterGeneratedRows(`SELECT * FROM g`, []any{int64(9), int64(4)}))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(5)`, 1))
		run(alterGeneratedRows(`SELECT * FROM g WHERE a=5`, []any{int64(5), nil}))
		run(fixture.Exec(`DROP TABLE g`, 0))
		run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(2)`, 1))
		run(fixture.ImplicitError(`ALTER TABLE g ALTER b DROP EXPRESSION IF EXISTS`, alterGeneratedVirtualDiagnostic("DROP EXPRESSION")))
		run(alterGeneratedRows(`SELECT * FROM g`, []any{int64(2), int64(3)}))
	})
}
