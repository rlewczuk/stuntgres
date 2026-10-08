//go:build reference

package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"stuntgres"
	"testing"
)

func TestReferenceVisibilityInquiryStoredCheckRestore(t *testing.T) {
	p := newReferencePair(t)
	functions := []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"}
	for kind, fn := range functions {
		name := fmt.Sprintf("pgv_restore_check_%d", kind)
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("CREATE TABLE "+name+"(id OID,CONSTRAINT "+name+"_check CHECK("+fn+"(id) IS NOT NULL))", 0))
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
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal(err)
	}
	for _, backend := range []struct {
		db  *sql.DB
		rec *referenceRecorder
	}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
		conn, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, backend.rec, s) }
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
					run(step)
					run(referencePrepared(step))
				}
			}
			valid := int64(23)
			switch kind {
			case 0:
				if err := conn.QueryRowContext(p.ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='"+name+"'").Scan(&valid); err != nil {
					t.Fatal(err)
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
		conn.Close()
	}
}
