package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceAttrdefControlsSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	var nativeDump string
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
			run(referenceExec("CREATE TABLE attrdef_controls(a INT,b INT DEFAULT 7,g INT GENERATED ALWAYS AS(a+1) STORED,n INT GENERATED ALWAYS AS(NULL) STORED)", 0))
			run(referenceExec("INSERT INTO attrdef_controls(a) VALUES(2)", 1))
			owner := deparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_class WHERE relname='attrdef_controls'")
			nums := []int64{2, 3, 4}
			expr := []string{"7", "(a + 1)", "NULL::integer"}
			before := attrdefRead(t, p, b, conn, owner, nums, false)
			run(attrdefSemantics(owner, nums, expr))
			if !b.pg {
				held, err := p.native.QueryContext(p.ctx, attrdefRawQuery, owner)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				metadata, err := held.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				attrdefMetadata(t, false, metadata)
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				nativeDump = dump.String()
				if nativeDump == "" {
					t.Fatal("empty native dump")
				}
				unchanged := func() {
					attrdefSame(t, attrdefRead(t, p, b, conn, owner, nums, false), before)
					run(attrdefSemantics(owner, nums, expr))
				}
				failure := errors.New("attrdef restore I/O failure")
				if err := stuntgres.Restore(p.ctx, p.native, catalogControlFailingReader{failure}); !errors.Is(err, failure) {
					t.Fatal(err)
				}
				unchanged()
				canceled, cancel := context.WithCancel(p.ctx)
				cancel()
				if err := stuntgres.Restore(canceled, p.native, strings.NewReader(nativeDump)); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				unchanged()
				err = stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE attrdef_bad(v INT DEFAULT 7); INSERT INTO attrdef_bad VALUES(2147483648);"))
				want := controlsReferenceDiagnostic("22003", "integer out of range")
				referenceAssertDiagnostic(t, err, &want)
				unchanged()
				run(referenceSelect("SELECT oid FROM pg_catalog.pg_class WHERE relname='attrdef_bad'", []string{"oid"}, []string{"OID"}, nil))
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				attrdefRead(t, p, b, conn, owner, nil, false)
				empty := referenceSelect("SELECT * FROM pg_catalog.pg_attrdef", []string{"oid", "adrelid", "adnum", "adbin"}, []string{"OID", "OID", "INT2", "PG_NODE_TREE"}, nil)
				empty.checkMetadata = func(t *testing.T, rows *sql.Rows) {
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					attrdefMetadata(t, false, columns)
				}
				run(empty)
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(nativeDump)); err != nil {
					t.Fatal(err)
				}
				after, err := held.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				attrdefMetadata(t, false, after)
				for i := range metadata {
					if metadata[i].Name() != after[i].Name() || metadata[i].ScanType() != after[i].ScanType() {
						t.Fatal("held schema changed")
					}
				}
				var retained [][]any
				for held.Next() {
					var id, rel, num int64
					var tree string
					if err := held.Scan(&id, &rel, &num, &tree); err != nil {
						t.Fatal(err)
					}
					retained = append(retained, []any{id, rel, num, tree})
				}
				if held.Err() != nil {
					t.Fatal(held.Err())
				}
				if !reflect.DeepEqual(retained, before) {
					t.Fatalf("held catalog changed: %v != %v", retained, before)
				}
				if err := held.Close(); err != nil {
					t.Fatal(err)
				}
				attrdefMetadata(t, false, metadata)
			} else {
				if nativeDump == "" {
					t.Fatal("native replay payload missing")
				}
				run(referenceExec("DROP TABLE attrdef_controls", 0))
				if _, err := conn.ExecContext(p.ctx, nativeDump); err != nil {
					t.Fatal("PostgreSQL native dump replay", err)
				}
			}
			owner = deparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_class WHERE relname='attrdef_controls'")
			for _, prepared := range []bool{false, true} {
				attrdefRead(t, p, b, conn, owner, nums, prepared)
				s := attrdefSemantics(owner, nums, expr)
				if prepared {
					s = referencePrepared(s)
				}
				run(s)
			}
			run(referenceSelect("SELECT * FROM attrdef_controls", []string{"a", "b", "g", "n"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(7), int64(3), nil}}))
			run(referenceExec("INSERT INTO attrdef_controls(a) VALUES(4)", 1))
			run(referenceSelect("SELECT * FROM attrdef_controls ORDER BY a", []string{"a", "b", "g", "n"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(7), int64(3), nil}, {int64(4), int64(7), int64(5), nil}}))
			run(attrdefSemantics(owner, nums, expr))
		})
	}
}
