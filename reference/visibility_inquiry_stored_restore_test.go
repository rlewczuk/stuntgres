package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"stuntgres"
	"testing"
)

func TestReferenceVisibilityInquiryStoredCheckRestore(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(fmt.Sprint(source), func(t *testing.T) {
			p := newReferenceScenario(t)
			functions := []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"}
			for kind, fn := range functions {
				name := fmt.Sprintf("pgv_restore_check_%d", kind)
				setup := referenceExec("CREATE TABLE "+name+"(id OID,CONSTRAINT "+name+"_check CHECK("+fn+"(id) IS NOT NULL))", 0)
				setup.nonContext = source
				referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, setup)
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			if p.postgres != nil {
				if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
					t.Fatal(err)
				}
			}
			for _, backend := range p.backends() {
				backend.db.SetMaxOpenConns(1)
				var conn *sql.Conn
				var executor referenceExecutor = backend.db
				if !source {
					var err error
					conn, err = backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					executor = conn
				}
				run := func(s referenceStep) {
					s.nonContext = source
					referenceRunStep(t, p.ctx, executor, backend.recorder, s)
				}
				for kind, fn := range functions {
					name := fmt.Sprintf("pgv_restore_check_%d", kind)
					constraint := name + "_check"
					for _, pretty := range []bool{false, true} {
						expr := fn + "(id) IS NOT NULL"
						if !pretty {
							expr = "(" + expr + ")"
						}
						for _, item := range []struct{ query, want string }{{fmt.Sprintf("SELECT pg_get_expr(conbin,conrelid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='%s'", pretty, constraint), expr}, {fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='%s'", pretty, constraint), "CHECK (" + expr + ")"}} {
							step := referenceSelect(item.query, []string{"d"}, []string{"TEXT"}, [][]any{{item.want}})
							if source {
								step.checkScan = visibilitySourceTextScan(item.want)
							}
							step.checkMetadata = sessionIdentityMetadata(backend.pg)
							run(step)
							run(referencePrepared(step))
						}
					}
					valid := int64(23)
					switch kind {
					case 0:
						q := "SELECT oid FROM pg_catalog.pg_class WHERE relname='" + name + "'"
						var err error
						if source {
							err = backend.db.QueryRow(q).Scan(&valid)
						} else {
							err = conn.QueryRowContext(p.ctx, q).Scan(&valid)
						}
						if err != nil || valid <= 0 || valid > 4294967295 {
							t.Fatal(valid, err)
						}
					case 2:
						valid = 950
					case 3:
						valid = 4402
					}
					run(referenceExec(fmt.Sprintf("INSERT INTO %s VALUES(%d)", name, valid), 1))
					failure := referenceImplicitError("INSERT INTO "+name+" VALUES(0)", referenceDiagnostic{Code: "23514", Message: fmt.Sprintf(`new row for relation "%s" violates check constraint "%s"`, name, constraint), Detail: "Failing row contains (0).", Schema: "public", Table: name, Constraint: constraint})
					failure.errorStage = "execute"
					run(failure)
					run(referenceSelect("SELECT id::bigint FROM "+name, []string{"id"}, []string{"INT8"}, [][]any{{valid}}))
				}
				if conn != nil {
					conn.Close()
				}
			}
		})
	}
}
