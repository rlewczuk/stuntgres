package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterTypeCollation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(c TEXT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(NULL),('a'),('Z'),('A'),('é')", 5))
			for _, tc := range []struct {
				name string
				oid  int64
			}{{`pg_catalog."C"`, 950}, {`pg_catalog."POSIX"`, 951}, {`pg_catalog."default"`, 100}} {
				run(fixture.Exec("ALTER TABLE t ALTER c TYPE TEXT COLLATE "+tc.name, 0))
				run(fixture.Select("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"TEXT"}, [][]any{{"A"}, {"Z"}, {"a"}, {"é"}, {nil}}))
				run(fixture.Select("SELECT c FROM t ORDER BY c DESC", []string{"c"}, []string{"TEXT"}, [][]any{{nil}, {"é"}, {"a"}, {"Z"}, {"A"}}))
				if b.Name == "postgres" {
					var id int64
					if err := b.DB.QueryRowContext(p.Ctx, "SELECT attcollation FROM pg_attribute WHERE attrelid='t'::regclass AND attname='c'").Scan(&id); err != nil || id != tc.oid {
						t.Fatal(id, tc.oid, err)
					}
				}
			}
			run(fixture.Exec(`ALTER TABLE t ALTER c TYPE TEXT COLLATE "C"`, 0))
			run(fixture.Exec("ALTER TABLE t ALTER c TYPE TEXT", 0))
			if b.Name == "postgres" {
				run(fixture.Select("SELECT attcollation::int FROM pg_attribute WHERE attrelid='t'::regclass AND attname='c'", []string{"attcollation"}, []string{"INT4"}, [][]any{{int64(100)}}))
			}
			run(fixture.Exec("CREATE TABLE nulls(c TEXT)", 0))
			run(fixture.Exec("INSERT INTO nulls VALUES(NULL),(NULL)", 2))
			run(fixture.Exec(`ALTER TABLE nulls ALTER c TYPE TEXT COLLATE "POSIX"`, 0))
			run(fixture.Select("SELECT c FROM nulls", []string{"c"}, []string{"TEXT"}, [][]any{{nil}, {nil}}))
			if b.Name == "postgres" {
				run(fixture.Select("SELECT attcollation::int FROM pg_attribute WHERE attrelid='nulls'::regclass AND attname='c'", []string{"attcollation"}, []string{"INT4"}, [][]any{{int64(951)}}))
			}
			run(fixture.Exec(`ALTER TABLE nulls ALTER c TYPE TEXT COLLATE "POSIX", ALTER c TYPE VARCHAR(5) COLLATE "C"`, 0))
			run(fixture.Select("SELECT c FROM nulls", []string{"c"}, []string{"VARCHAR"}, [][]any{{nil}, {nil}}))
			if b.Name == "postgres" {
				run(fixture.Select("SELECT attcollation::int FROM pg_attribute WHERE attrelid='nulls'::regclass AND attname='c'", []string{"attcollation"}, []string{"INT4"}, [][]any{{int64(950)}}))
			}
			for _, name := range []string{`public."C"`, `c`, `missing`} {
				run(fixture.ImplicitError("ALTER TABLE t ALTER c TYPE TEXT COLLATE "+name, fixture.Diagnostic{Code: "42704", Message: fmt.Sprintf(`collation "%s" for encoding "UTF8" does not exist`, map[string]string{`public."C"`: "public.C", "c": "c", "missing": "missing"}[name]), Position: "33"}))
			}
			for _, tc := range []struct{ query, code, message, position string }{
				{`ALTER TABLE t ALTER c TYPE INT COLLATE "C"`, "42804", "collations are not supported by type integer", "32"},
				{`ALTER TABLE t ALTER c TYPE TEXT COLLATE missing_schema."C"`, "3F000", `schema "missing_schema" does not exist`, "33"},
			} {
				failure := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
				failure.ErrorStage = "execute"
				run(failure)
				run(fixture.Select("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"TEXT"}, [][]any{{"A"}, {"Z"}, {"a"}, {"é"}, {nil}}))
			}
		})
	}
}

func TestReferenceAlterTypePhysicalSnapshotBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(c INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
			run(fixture.Exec("CREATE TABLE anchor(c INT)", 0))
			run(fixture.Exec("INSERT INTO anchor VALUES(1)", 1))
			tx, err := b.DB.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT c FROM anchor", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Exec("ALTER TABLE t ALTER c TYPE BIGINT", 0))
			if b.Name == "postgres" {
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT c FROM t", []string{"c"}, []string{"INT8"}, nil))
			} else {
				rows, err := tx.QueryContext(p.Ctx, "SELECT c FROM t")
				if rows != nil {
					rows.Close()
				}
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
			}
		})
	}
}
