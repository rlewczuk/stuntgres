package reference_test

import (
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSchemaPreparedFrozenContracts(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "s1", "s2", "text_shape", "label_shape", "count_shape")
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
			for _, q := range []string{"CREATE SCHEMA s1 CREATE TABLE t(id INT)", "CREATE SCHEMA s2 CREATE TABLE t(id INT)", "CREATE SCHEMA text_shape CREATE TABLE t(id TEXT)", "CREATE SCHEMA label_shape CREATE TABLE t(other INT)", "CREATE SCHEMA count_shape CREATE TABLE t(id INT,note TEXT)"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO s1.t VALUES(7)", 1))
			run(fixture.Exec("INSERT INTO s2.t VALUES(9)", 1))
			run(fixture.Exec("SET search_path TO s1", 0))
			selectStmt, err := c.PrepareContext(p.Ctx, "SELECT * FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer selectStmt.Close()
			insertStmt, err := c.PrepareContext(p.Ctx, "INSERT INTO t VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer insertStmt.Close()
			read := func(want int64) {
				t.Helper()
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: selectStmt, SQL: "SELECT * FROM t"}, backend.Recorder, fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{want}}))
			}
			read(7)
			run(fixture.Exec("SET search_path TO s2", 0))
			read(9)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: insertStmt, SQL: "INSERT INTO t VALUES($1)"}, backend.Recorder, fixture.Exec("INSERT INTO t VALUES($1)", 1, 12))
			var inserted int64
			if err := c.QueryRowContext(p.Ctx, "SELECT id FROM s2.t WHERE id=12").Scan(&inserted); err != nil || inserted != 12 {
				t.Fatal(inserted, err)
			}
			run(fixture.Select("SELECT id FROM s1.t WHERE id=12", []string{"id"}, []string{"INT4"}, nil))
			for _, schema := range []string{"text_shape", "label_shape", "count_shape"} {
				run(fixture.Exec("SET search_path TO "+schema, 0))
				rows, err := selectStmt.QueryContext(p.Ctx)
				if rows != nil {
					rows.Close()
					t.Fatal("incompatible cursor")
				}
				want := fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type")
				fixture.AssertDiagnostic(t, err, &want)
			}
			run(fixture.Exec("SET search_path TO text_shape", 0))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: insertStmt, SQL: "INSERT INTO t VALUES($1)"}, backend.Recorder, fixture.Exec("INSERT INTO t VALUES($1)", 1, 7))
			var text string
			if err := c.QueryRowContext(p.Ctx, "SELECT id FROM text_shape.t").Scan(&text); err != nil || text != "7" {
				t.Fatal(text, err)
			}
			run(fixture.Exec("SET search_path TO s1", 0))
			if !backend.PG {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				run(fixture.Exec("CREATE SCHEMA s1 CREATE TABLE t(id INT)", 0))
				run(fixture.Exec("INSERT INTO s1.t VALUES(11)", 1))
				read(11)
			} else {
				read(7)
			}
		})
	}
}

func TestReferenceSchemaTransactionBoundary(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "transaction_schema")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			for _, q := range []string{"CREATE SCHEMA transaction_schema", "ALTER SCHEMA public RENAME TO transaction_schema", "DROP SCHEMA public"} {
				for _, nonContext := range []bool{false, true} {
					tx, err := backend.DB.BeginTx(p.Ctx, &sql.TxOptions{})
					if err != nil {
						t.Fatal(err)
					}
					var result sql.Result
					if nonContext {
						result, err = tx.Exec(q)
					} else {
						result, err = tx.ExecContext(p.Ctx, q)
					}
					fixture.DeleteReferenceCount(t, result, err, 0)
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				}
			}
			result, err := backend.DB.ExecContext(p.Ctx, "CREATE TABLE transaction_schema.t(id INT)")
			if err == nil || result != nil {
				t.Fatal("transaction CREATE leaked", result, err)
			}
			result, err = backend.DB.Exec("CREATE TABLE transaction_schema.t(id INT)")
			if err == nil || result != nil {
				t.Fatal("transaction CREATE leaked through Exec", result, err)
			}
		})
	}
}

func TestReferenceSchemaScalarAndLockIntegration(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "scalar_first", "scalar_second")
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
			run(fixture.Exec("CREATE SCHEMA scalar_first CREATE TABLE t(id INT,flag BOOL)", 0))
			run(fixture.Exec("CREATE SCHEMA scalar_second CREATE TABLE t(id INT,flag BOOL)", 0))
			run(fixture.Exec("INSERT INTO scalar_first.t VALUES(7,TRUE)", 1))
			run(fixture.Exec("INSERT INTO scalar_second.t VALUES(9,TRUE)", 1))
			run(fixture.Exec("SET search_path TO scalar_first", 0))
			held, err := c.PrepareContext(p.Ctx, "SELECT id,11,$1 FROM t WHERE flag")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, tc := range []struct {
				path string
				want int64
			}{{"scalar_first", 7}, {"scalar_second", 9}} {
				run(fixture.Exec("SET search_path TO "+tc.path, 0))
				var id, literal int64
				var bound string
				if err := held.QueryRowContext(p.Ctx, "bound").Scan(&id, &literal, &bound); err != nil || id != tc.want || literal != 11 || bound != "bound" {
					t.Fatal(id, literal, bound, err)
				}
			}
			missing, err := c.PrepareContext(p.Ctx, "LOCK TABLE missing_schema.t IN ACCESS EXCLUSIVE MODE")
			if err != nil {
				t.Fatal(err)
			}
			missing.Close()
			tx, err := c.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for _, q := range []string{"LOCK TABLE t IN ACCESS EXCLUSIVE MODE", "LOCK TABLE scalar_first.t IN ACCESS EXCLUSIVE MODE"} {
				fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec(q, 0))
			}
			var id, literal int64
			if err := tx.QueryRowContext(p.Ctx, "SELECT id,11 FROM t WHERE flag").Scan(&id, &literal); err != nil || id != 9 || literal != 11 {
				t.Fatal(id, literal, err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
