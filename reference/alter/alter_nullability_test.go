package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterNullabilityActionLists(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			name, seed, actions string
			want                fixture.Diagnostic
			columns             []string
			rows                [][]any
		}{
			{name: "semantic-before-scan", seed: "NULL,NULL", actions: "ALTER a SET NOT NULL, ALTER missing SET NOT NULL", want: fixture.Diagnostic{Code: "42703", Message: `column "missing" of relation "t" does not exist`}},
			{name: "drop-before-set", seed: "7,9", actions: "ALTER a SET NOT NULL, ALTER a DROP NOT NULL"},
			{name: "reverse-drop-before-set", seed: "7,9", actions: "ALTER a DROP NOT NULL, ALTER a SET NOT NULL"},
			{name: "drop-column-before-set", seed: "7,9", actions: "ALTER a SET NOT NULL, DROP COLUMN a", want: fixture.Diagnostic{Code: "42703", Message: `column "a" of relation "t" does not exist`}},
			{name: "physical-scan-order", seed: "NULL,NULL", actions: "ADD CONSTRAINT b_nn NOT NULL b, ADD CONSTRAINT a_nn NOT NULL a", want: fixture.Diagnostic{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"}},
			{name: "drop-before-add", seed: "7,9", actions: "ADD CONSTRAINT n NOT NULL a, DROP CONSTRAINT n", want: fixture.Diagnostic{Code: "42704", Message: `constraint "n" of relation "t" does not exist`}},
			{name: "projection", seed: "7,9", actions: "DROP COLUMN b, ALTER a SET NOT NULL", columns: []string{"a"}, rows: [][]any{{int64(7)}}},
			{name: "projection-rollback", seed: "NULL,9", actions: "DROP COLUMN b, ALTER a SET NOT NULL", want: fixture.Diagnostic{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"}},
			{name: "set-before-transformed-add", seed: "7,9", actions: "ADD CONSTRAINT requested NOT NULL a, ALTER a SET NOT NULL"},
		} {
			t.Run(tc.name+fmt.Sprint(prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				// Separate backend subtests allow the pinned oracle to run even while native fails.
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
						run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
						run(fixture.Exec("INSERT INTO t VALUES("+tc.seed+")", 1))
						q := "ALTER TABLE t " + tc.actions
						s := fixture.Exec(q, 0)
						if tc.want.Code != "" {
							s = fixture.ImplicitError(q, tc.want)
							s.ErrorStage = "execute"
						}
						s.Prepared = prepared
						run(s)
						columns := tc.columns
						types := []string{"INT4"}
						if columns == nil {
							columns = []string{"a", "b"}
							types = append(types, "INT4")
						}
						rows := tc.rows
						if rows == nil {
							if tc.seed == "7,9" {
								rows = [][]any{{int64(7), int64(9)}}
							} else if tc.seed == "NULL,9" {
								rows = [][]any{{nil, int64(9)}}
							} else {
								rows = [][]any{{nil, nil}}
							}
						}
						run(fixture.Select("SELECT * FROM t", columns, types, rows))
						nullable := []bool{tc.want.Code != ""}
						if len(columns) == 2 {
							nullable = append(nullable, true)
						}
						fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, prepared, "SELECT * FROM t", columns, types, nullable, 1)
						if tc.want.Code == "" && tc.name != "projection" {
							if tc.name == "set-before-transformed-add" {
								run(fixture.ImplicitError("ALTER TABLE t DROP CONSTRAINT requested", fixture.Diagnostic{Code: "42704", Message: `constraint "requested" of relation "t" does not exist`}))
							}
							run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT t_a_not_null", 0))
						}
					})
				}
			})
		}
	}
}

func TestReferenceAlterNullabilityFailingActionNotice(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, record, s) }
			run(fixture.Exec("CREATE TABLE t(a INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(NULL)", 1))
			run(fixture.ImplicitError("ALTER TABLE t DROP CONSTRAINT IF EXISTS missing, ALTER a SET NOT NULL", fixture.Diagnostic{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"}))
			want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `constraint "missing" of relation "t" does not exist, skipping`}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("notices %#v want %#v", notices, want)
			}
			run(fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
		})
	}
}

func TestReferenceAlterNullabilityTransactions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(NULL,9)", 1))
			tx, err := b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			tr := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
			tr(fixture.Exec("UPDATE t SET a=7", 1))
			tr(fixture.Exec("SAVEPOINT s", 0))
			tr(fixture.Exec("ALTER TABLE t DROP COLUMN b, ALTER a SET NOT NULL", 0))
			tr(fixture.Exec("INSERT INTO t VALUES(8)", 1))
			tr(fixture.Select("SELECT * FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
			tr(fixture.Exec("ROLLBACK TO s", 0))
			tr(fixture.Exec("ROLLBACK TO s", 0))
			tr(fixture.Exec("INSERT INTO t VALUES(NULL,10)", 1))
			tr(fixture.Exec("DELETE FROM t WHERE b=10", 1))
			tr(fixture.Exec("ALTER TABLE t ALTER a SET NOT NULL", 0))
			tr(fixture.Exec("INSERT INTO t VALUES(8,11)", 1))
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT * FROM t ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}, {int64(8), int64(11)}}))
			run(fixture.ImplicitError("INSERT INTO t VALUES(NULL,12)", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, 12).", Schema: "public", Table: "t", Column: "a"}))
		})
	}
}

func TestReferenceAlterNullabilityFreshValidation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT)", 0))
			run(fixture.Exec("CREATE TABLE u(a INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
			run(fixture.Exec("INSERT INTO u VALUES(1)", 1))
			tx, err := b.DB.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT a FROM u", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Exec("INSERT INTO t VALUES(NULL)", 1))
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ImplicitError("ALTER TABLE t ALTER a SET NOT NULL", fixture.Diagnostic{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"}))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT * FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}}))
		})
	}
}
