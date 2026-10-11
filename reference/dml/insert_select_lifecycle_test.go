package reference_test

import (
	"context"
	"database/sql/driver"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertSelectPreparedRebind(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			run := func(s fixture.Step) { fixture.InsertSelectRun(t, ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE s(i INT)", 0))
			run(fixture.Exec("CREATE TABLE t(i BIGINT,n NUMERIC(3,1),m NUMERIC(5,2),b BOOL)", 0))
			query := "INSERT INTO t(i) SELECT $1 FROM s"
			stmt, err := backend.DB.PrepareContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{backend.DB, stmt, query}
			for _, tc := range []struct {
				before               string
				inserted, count, arg int64
			}{
				{"", 0, 0, 7}, {"INSERT INTO s VALUES(1),(2)", 2, 2, 8}, {"INSERT INTO s VALUES(3)", 1, 3, 9},
			} {
				if tc.before != "" {
					run(fixture.Exec(tc.before, tc.inserted))
				}
				fixture.InsertSelectRun(t, ctx, held, backend.Recorder, fixture.Exec(query, tc.count, tc.arg))
			}
			run(fixture.Select("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT8"}, [][]any{{int64(8)}, {int64(8)}, {int64(9)}, {int64(9)}, {int64(9)}}))
			run(fixture.Prepared(fixture.Exec("INSERT INTO t(n,m) SELECT $1,$1", 1, "1.234")))
			run(fixture.Select("SELECT n,m FROM t ORDER BY n", []string{"n", "m"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{"1.2", "1.23"}, {nil, nil}, {nil, nil}, {nil, nil}, {nil, nil}, {nil, nil}}))
			run(fixture.Prepared(fixture.Exec("INSERT INTO t(b,i) SELECT $1=1,$1 FROM s", 3, int64(1))))
			failure := fixture.ImplicitError("INSERT INTO t(i) SELECT $1 FROM s WHERE i=$1", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus bigint", Position: "25"}, int64(1))
			failure.ErrorStage = "execute"
			run(failure)
			failure.Prepared, failure.ErrorStage = true, "prepare"
			run(failure)
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("INSERT INTO t(i) SELECT i FROM s", []string{}, []string{}, [][]any{})
				s.Prepared = prepared
				run(s)
			}
			run(fixture.Select("SELECT i FROM t WHERE i=2", []string{"i"}, []string{"INT8"}, [][]any{{int64(2)}, {int64(2)}}))
		})
	}
}

func TestReferenceInsertSelectCommandMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE s(i INT)", 0), fixture.Exec("CREATE TABLE t(i INT)", 0)} {
		p.Step(t, s)
	}
	// Raw native statement metadata is a public driver contract, before wrapping.
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		stmt, err := raw.(driver.Conn).Prepare("INSERT INTO t SELECT $1 FROM s")
		if err != nil {
			return err
		}
		defer stmt.Close()
		if stmt.NumInput() != 1 {
			t.Fatalf("NumInput = %d", stmt.NumInput())
		}
		for _, tc := range []struct {
			before               string
			inserted, count, arg int64
		}{
			{"", 0, 0, 7}, {"INSERT INTO s VALUES(1),(2)", 2, 2, 8}, {"INSERT INTO s VALUES(3)", 1, 3, 9},
		} {
			if tc.before != "" {
				fixture.InsertSelectRun(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec(tc.before, tc.inserted))
			}
			result, err := stmt.Exec([]driver.Value{tc.arg})
			fixture.InsertSelectAffected(t, result, err, tc.count)
		}
		for _, backend := range p.Backends() {
			t.Run(backend.Name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.InsertSelectRun(t, p.Ctx, backend.DB, backend.Recorder, s) }
				if backend.PG {
					run(fixture.Exec("INSERT INTO s VALUES(1),(2),(3)", 3))
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("INSERT INTO t SELECT i FROM s", []string{}, []string{}, [][]any{})
					s.Prepared = prepared
					run(s)
				}
				run(fixture.Select("SELECT i FROM t WHERE i=2", []string{"i"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}}))
			})
		}
		fixture.InsertSelectRun(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec("ALTER TABLE s RENAME COLUMN i TO renamed", 0))
		result, err := stmt.Exec([]driver.Value{int64(10)})
		fixture.InsertSelectAffected(t, result, err, 3)
		named, err := raw.(driver.Conn).Prepare("INSERT INTO t SELECT renamed FROM s")
		if err != nil {
			return err
		}
		defer named.Close()
		fixture.InsertSelectRun(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec("ALTER TABLE s RENAME COLUMN renamed TO changed", 0))
		result, err = named.Exec(nil)
		if result != nil {
			t.Fatal("failed raw Exec exposed a result")
		}
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "renamed" does not exist`, Position: "22"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReferenceInsertSelectSourceRename(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			run := func(s fixture.Step) { fixture.InsertSelectRun(t, ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE s(i INT)", 0))
			run(fixture.Exec("CREATE TABLE t(i INT)", 0))
			query := "INSERT INTO t SELECT $1 FROM s"
			stmt, err := backend.DB.PrepareContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{backend.DB, stmt, query}
			for _, tc := range []struct {
				before               string
				inserted, count, arg int64
			}{
				{"", 0, 0, 7}, {"INSERT INTO s VALUES(1),(2)", 2, 2, 8}, {"INSERT INTO s VALUES(3)", 1, 3, 9},
			} {
				if tc.before != "" {
					run(fixture.Exec(tc.before, tc.inserted))
				}
				fixture.InsertSelectRun(t, ctx, held, backend.Recorder, fixture.Exec(query, tc.count, tc.arg))
			}
			run(fixture.Exec("ALTER TABLE s RENAME COLUMN i TO renamed", 0))
			fixture.InsertSelectRun(t, ctx, fixture.HeldStatement{backend.DB, stmt, query}, backend.Recorder, fixture.Exec(query, 3, int64(10)))
			query = "INSERT INTO t SELECT renamed FROM s"
			named, err := backend.DB.PrepareContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer named.Close()
			run(fixture.Exec("ALTER TABLE s RENAME COLUMN renamed TO changed", 0))
			failure := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42703", Message: `column "renamed" does not exist`, Position: "22"})
			failure.ErrorStage = "execute"
			fixture.InsertSelectRun(t, ctx, fixture.HeldStatement{backend.DB, named, query}, backend.Recorder, failure)
			run(fixture.Select("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(8)}, {int64(8)}, {int64(9)}, {int64(9)}, {int64(9)}, {int64(10)}, {int64(10)}, {int64(10)}}))
		})
	}
}

func TestReferenceInsertSelectTransactions(t *testing.T) {
	for _, mode := range []string{"commit", "rollback", "failure"} {
		t.Run(mode, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.InsertSelectRun(t, p.Ctx, backend.DB, backend.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(i INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1),(1),(2)", 3))
					tx, err := backend.DB.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					own := func(s fixture.Step) { fixture.InsertSelectRun(t, p.Ctx, tx, backend.Recorder, s) }
					own(fixture.Exec("INSERT INTO t VALUES(3)", 1))
					own(fixture.Prepared(fixture.Exec("INSERT INTO t SELECT * FROM t", 4)))
					want := [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(3)}}
					own(fixture.Select("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT4"}, want))
					committed := [][]any{{int64(1)}, {int64(1)}, {int64(2)}}
					run(fixture.Select("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT4"}, committed))
					if mode == "commit" {
						if err := tx.Commit(); err != nil {
							t.Fatal(err)
						}
					} else {
						if mode == "failure" {
							s := fixture.ImplicitError("INSERT INTO t SELECT 'bad'", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "22"})
							s.ErrorStage = "execute"
							own(s)
							s = fixture.ImplicitError("SELECT i FROM t", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
							s.ErrorStage = "execute"
							own(s)
						}
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						want = committed
					}
					run(fixture.Select("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT4"}, want))
				})
			}
		})
	}
}
