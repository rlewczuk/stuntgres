package reference_test

import (
	"database/sql"
	"stuntgres"
	"testing"
)

func TestReferenceSchemaPreparedFrozenContracts(t *testing.T) {
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "s1", "s2", "text_shape", "label_shape", "count_shape")
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, s) }
			for _, q := range []string{"CREATE SCHEMA s1 CREATE TABLE t(id INT)", "CREATE SCHEMA s2 CREATE TABLE t(id INT)", "CREATE SCHEMA text_shape CREATE TABLE t(id TEXT)", "CREATE SCHEMA label_shape CREATE TABLE t(other INT)", "CREATE SCHEMA count_shape CREATE TABLE t(id INT,note TEXT)"} {
				run(referenceExec(q, 0))
			}
			run(referenceExec("INSERT INTO s1.t VALUES(7)", 1))
			run(referenceExec("INSERT INTO s2.t VALUES(9)", 1))
			run(referenceExec("SET search_path TO s1", 0))
			selectStmt, err := c.PrepareContext(p.ctx, "SELECT * FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer selectStmt.Close()
			insertStmt, err := c.PrepareContext(p.ctx, "INSERT INTO t VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer insertStmt.Close()
			read := func(want int64) {
				t.Helper()
				referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: selectStmt, query: "SELECT * FROM t"}, backend.recorder, referenceSelect("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{want}}))
			}
			read(7)
			run(referenceExec("SET search_path TO s2", 0))
			read(9)
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: insertStmt, query: "INSERT INTO t VALUES($1)"}, backend.recorder, referenceExec("INSERT INTO t VALUES($1)", 1, 12))
			var inserted int64
			if err := c.QueryRowContext(p.ctx, "SELECT id FROM s2.t WHERE id=12").Scan(&inserted); err != nil || inserted != 12 {
				t.Fatal(inserted, err)
			}
			run(referenceSelect("SELECT id FROM s1.t WHERE id=12", []string{"id"}, []string{"INT4"}, nil))
			for _, schema := range []string{"text_shape", "label_shape", "count_shape"} {
				run(referenceExec("SET search_path TO "+schema, 0))
				rows, err := selectStmt.QueryContext(p.ctx)
				if rows != nil {
					rows.Close()
					t.Fatal("incompatible cursor")
				}
				want := controlsReferenceDiagnostic("0A000", "cached plan must not change result type")
				referenceAssertDiagnostic(t, err, &want)
			}
			run(referenceExec("SET search_path TO text_shape", 0))
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: insertStmt, query: "INSERT INTO t VALUES($1)"}, backend.recorder, referenceExec("INSERT INTO t VALUES($1)", 1, 7))
			var text string
			if err := c.QueryRowContext(p.ctx, "SELECT id FROM text_shape.t").Scan(&text); err != nil || text != "7" {
				t.Fatal(text, err)
			}
			run(referenceExec("SET search_path TO s1", 0))
			if !backend.pg {
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				run(referenceExec("CREATE SCHEMA s1 CREATE TABLE t(id INT)", 0))
				run(referenceExec("INSERT INTO s1.t VALUES(11)", 1))
				read(11)
			} else {
				read(7)
			}
		})
	}
}

func TestReferenceSchemaTransactionBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "transaction_schema")
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			for _, q := range []string{"CREATE SCHEMA transaction_schema", "ALTER SCHEMA public RENAME TO transaction_schema", "DROP SCHEMA public"} {
				for _, nonContext := range []bool{false, true} {
					tx, err := backend.db.BeginTx(p.ctx, &sql.TxOptions{})
					if err != nil {
						t.Fatal(err)
					}
					var result sql.Result
					if nonContext {
						result, err = tx.Exec(q)
					} else {
						result, err = tx.ExecContext(p.ctx, q)
					}
					want := "0A000"
					if backend.pg {
						want = ""
					}
					referenceState(t, err, want)
					if backend.pg {
						deleteReferenceCount(t, result, err, 0)
					} else if result != nil {
						t.Fatal("rejected schema utility published result")
					}
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				}
			}
			result, err := backend.db.ExecContext(p.ctx, "CREATE TABLE transaction_schema.t(id INT)")
			if err == nil || result != nil {
				t.Fatal("transaction CREATE leaked", result, err)
			}
			result, err = backend.db.Exec("CREATE TABLE transaction_schema.t(id INT)")
			if err == nil || result != nil {
				t.Fatal("transaction CREATE leaked through Exec", result, err)
			}
		})
	}
}

func TestReferenceSchemaScalarAndLockIntegration(t *testing.T) {
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "scalar_first", "scalar_second")
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, s) }
			run(referenceExec("CREATE SCHEMA scalar_first CREATE TABLE t(id INT,flag BOOL)", 0))
			run(referenceExec("CREATE SCHEMA scalar_second CREATE TABLE t(id INT,flag BOOL)", 0))
			run(referenceExec("INSERT INTO scalar_first.t VALUES(7,TRUE)", 1))
			run(referenceExec("INSERT INTO scalar_second.t VALUES(9,TRUE)", 1))
			run(referenceExec("SET search_path TO scalar_first", 0))
			held, err := c.PrepareContext(p.ctx, "SELECT id,11,$1 FROM t WHERE flag")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, tc := range []struct {
				path string
				want int64
			}{{"scalar_first", 7}, {"scalar_second", 9}} {
				run(referenceExec("SET search_path TO "+tc.path, 0))
				var id, literal int64
				var bound string
				if err := held.QueryRowContext(p.ctx, "bound").Scan(&id, &literal, &bound); err != nil || id != tc.want || literal != 11 || bound != "bound" {
					t.Fatal(id, literal, bound, err)
				}
			}
			missing, err := c.PrepareContext(p.ctx, "LOCK TABLE missing_schema.t IN ACCESS EXCLUSIVE MODE")
			if err != nil {
				t.Fatal(err)
			}
			missing.Close()
			tx, err := c.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for _, q := range []string{"LOCK TABLE t IN ACCESS EXCLUSIVE MODE", "LOCK TABLE scalar_first.t IN ACCESS EXCLUSIVE MODE"} {
				referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec(q, 0))
			}
			var id, literal int64
			if err := tx.QueryRowContext(p.ctx, "SELECT id,11 FROM t WHERE flag").Scan(&id, &literal); err != nil || id != 9 || literal != 11 {
				t.Fatal(id, literal, err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
