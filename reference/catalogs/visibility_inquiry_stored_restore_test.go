package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVisibilityInquiryStoredCheckRestore(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(fmt.Sprint(source), func(t *testing.T) {
			p := fixture.NewScenario(t)
			functions := []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"}
			for kind, fn := range functions {
				name := fmt.Sprintf("pgv_restore_check_%d", kind)
				setup := fixture.Exec("CREATE TABLE "+name+"(id OID,CONSTRAINT "+name+"_check CHECK("+fn+"(id) IS NOT NULL))", 0)
				setup.NonContext = source
				fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, setup)
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal(err)
				}
			}
			for _, backend := range p.Backends() {
				backend.DB.SetMaxOpenConns(1)
				var conn *sql.Conn
				var executor fixture.Executor = backend.DB
				if !source {
					var err error
					conn, err = backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					executor = conn
				}
				run := func(s fixture.Step) {
					s.NonContext = source
					fixture.RunStep(t, p.Ctx, executor, backend.Recorder, s)
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
							step := fixture.Select(item.query, []string{"d"}, []string{"TEXT"}, [][]any{{item.want}})
							if source {
								step.CheckScan = visibilitySourceTextScan(item.want)
							}
							step.CheckMetadata = fixture.SessionIdentityMetadata(backend.PG)
							run(step)
							run(fixture.Prepared(step))
						}
					}
					valid := int64(23)
					switch kind {
					case 0:
						q := "SELECT oid FROM pg_catalog.pg_class WHERE relname='" + name + "'"
						var err error
						if source {
							err = backend.DB.QueryRow(q).Scan(&valid)
						} else {
							err = conn.QueryRowContext(p.Ctx, q).Scan(&valid)
						}
						if err != nil || valid <= 0 || valid > 4294967295 {
							t.Fatal(valid, err)
						}
					case 2:
						valid = 950
					case 3:
						valid = 4402
					}
					run(fixture.Exec(fmt.Sprintf("INSERT INTO %s VALUES(%d)", name, valid), 1))
					failure := fixture.ImplicitError("INSERT INTO "+name+" VALUES(0)", fixture.Diagnostic{Code: "23514", Message: fmt.Sprintf(`new row for relation "%s" violates check constraint "%s"`, name, constraint), Detail: "Failing row contains (0).", Schema: "public", Table: name, Constraint: constraint})
					failure.ErrorStage = "execute"
					run(failure)
					run(fixture.Select("SELECT id::bigint FROM "+name, []string{"id"}, []string{"INT8"}, [][]any{{valid}}))
				}
				if conn != nil {
					conn.Close()
				}
			}
		})
	}
}
