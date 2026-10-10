package reference_test

import (
	"bytes"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceExclusionControls(t *testing.T) {
	exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { exclusionReferenceRun(t, p, b, b.db, s) }
		run(referenceExec("CREATE TABLE t(v INT,note TEXT,active BOOL,CONSTRAINT ex EXCLUDE USING btree(lower(note) WITH =) WHERE(active) DEFERRABLE INITIALLY DEFERRED)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,'A',TRUE),(2,'a',FALSE),(3,NULL,TRUE)", 3))
		wantRows := referenceSelect("SELECT v,note,active FROM t ORDER BY v", []string{"v", "note", "active"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), "A", true}, {int64(2), "a", false}, {int64(3), nil, true}})
		run(wantRows)
		run(referencePrepared(wantRows))
		want := exclusionReferenceConflict("t", "ex", "lower(note)", "a", "a")
		run(referenceErrorDiagnostic("INSERT INTO t VALUES(4,'a',TRUE)", want))
		if b.pg {
			return
		} // Native controls have no server control API equivalent.
		conn, err := p.native.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		err = conn.Raw(func(raw any) error {
			rows, err := raw.(driver.QueryerContext).QueryContext(p.ctx, wantRows.query, nil)
			if err != nil {
				return err
			}
			defer rows.Close()
			var original bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &original); err != nil {
				return err
			}
			if strings.Count(original.String(), "EXCLUDE") != 1 || strings.Contains(original.String(), "CREATE INDEX ex") {
				t.Fatalf("constraint/index must serialize once: %s", original.String())
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				return err
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(original.Bytes())); err != nil {
				return err
			}
			var restored bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &restored); err != nil {
				return err
			}
			if restored.String() != original.String() {
				t.Fatalf("exclusion redump changed: %s\nwant %s", restored.String(), original.String())
			}
			run(wantRows)
			run(referencePrepared(wantRows))
			run(referencePrepared(referenceErrorDiagnostic("INSERT INTO t VALUES(4,'a',TRUE)", want)))
			// Transient checking mode does not become a persistent definition.
			c := concurrentIndexConn(t, p, b)
			exclusionReferenceRun(t, p, b, c, referenceExec("BEGIN", 0))
			exclusionReferenceRun(t, p, b, c, referenceExec("SET CONSTRAINTS ex IMMEDIATE", 0))
			var during bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &during); err != nil {
				return err
			}
			if during.String() != original.String() {
				t.Fatal("transaction mode serialized")
			}
			exclusionReferenceRun(t, p, b, c, referenceExec("ROLLBACK", 0))
			// A live TEMP definition and its OIDs survive replacement staging.
			exclusionReferenceRun(t, p, b, c, referenceExec("CREATE TEMP TABLE retained(v INT)", 0))
			exclusionReferenceRun(t, p, b, c, referenceExec("INSERT INTO retained VALUES(9)", 1))
			for _, script := range []string{
				"CREATE TABLE replacement(v INT,CONSTRAINT bad EXCLUDE(v WITH =)); INSERT INTO replacement VALUES(1),(1)",
				"CREATE TABLE replacement(v INT,CONSTRAINT bad EXCLUDE(v WITH <>))",
			} {
				err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script))
				d := exclusionReferenceConflict("replacement", "bad", "v", "1", "1")
				if strings.Contains(script, "<>") {
					d = controlsReferenceDiagnostic("42809", `operator <>(integer,integer) is not a member of operator family "integer_ops"`)
					d.Detail = "The exclusion operator must be related to the index operator class for the constraint."
				}
				referenceAssertDiagnostic(t, err, &d)
				var unchanged bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &unchanged); err != nil {
					return err
				}
				if unchanged.String() != original.String() {
					t.Fatal("failed Restore mutated destination")
				}
				run(wantRows)
				exclusionReferenceRun(t, p, b, c, exclusionReferenceRows("SELECT v FROM retained", 9))
			}
			metadata := rows.(driver.RowsColumnTypeDatabaseTypeName)
			if !reflect.DeepEqual(rows.Columns(), []string{"v", "note", "active"}) {
				t.Fatal(rows.Columns())
			}
			for i, typ := range []string{"INT4", "TEXT", "BOOL"} {
				if metadata.ColumnTypeDatabaseTypeName(i) != typ {
					t.Fatal("retained metadata changed")
				}
			}
			dst := make([]driver.Value, 3)
			for _, values := range [][]driver.Value{{int64(1), "A", true}, {int64(2), "a", false}, {int64(3), nil, true}} {
				if err := rows.Next(dst); err != nil || !reflect.DeepEqual(dst, values) {
					t.Fatal(dst, values, err)
				}
			}
			if err := rows.Next(dst); err != io.EOF {
				t.Fatal(err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestReferenceExclusionFeatureSmoke(t *testing.T) {
	exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		c := concurrentIndexConn(t, p, b)
		run := func(s referenceStep) { exclusionReferenceRun(t, p, b, c, s) }
		run(referenceExec("CREATE TABLE t(v INT,note TEXT,active BOOL,CONSTRAINT ex EXCLUDE(lower(note) WITH =) WHERE(active) DEFERRABLE INITIALLY DEFERRED)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,'A',TRUE),(2,'a',FALSE)", 2))
		run(referencePrepared(referenceExec("INSERT INTO t VALUES($1,$2,$3)", 1, int64(3), nil, true)))
		run(referencePrepared(referenceSelect("SELECT v,note,active FROM t ORDER BY v", []string{"v", "note", "active"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), "A", true}, {int64(2), "a", false}, {int64(3), nil, true}})))
		want := exclusionReferenceConflict("t", "ex", "lower(note)", "a", "a")
		run(referenceExec("BEGIN", 0))
		run(referenceExec("INSERT INTO t VALUES(4,'a',TRUE)", 1))
		run(referenceExec("SAVEPOINT s", 0))
		run(referenceErrorDiagnostic("SET CONSTRAINTS ex IMMEDIATE", want))
		run(referenceExec("ROLLBACK TO s", 0))
		run(referenceExec("DELETE FROM t WHERE v=4", 1))
		run(referenceExec("SET CONSTRAINTS ex IMMEDIATE", 0))
		run(referenceExec("COMMIT", 0))
		run(referenceErrorDiagnostic("INSERT INTO t VALUES(4,'a',TRUE)", want))
		run(referenceExec("CREATE TABLE h(v INT,active BOOL,CONSTRAINT hx EXCLUDE USING hash(v WITH =) WHERE(active))", 0))
		run(referenceExec("INSERT INTO h VALUES(1,TRUE),(1,FALSE)", 2))
		run(referenceErrorDiagnostic("INSERT INTO h VALUES(1,TRUE)", exclusionReferenceConflict("h", "hx", "v", "1", "1")))
		run(referenceExec("INSERT INTO h VALUES(1,TRUE),(2,TRUE) ON CONFLICT DO NOTHING", 1))
		run(referencePrepared(referenceExec("INSERT INTO h VALUES(1,TRUE) ON CONFLICT ON CONSTRAINT hx DO NOTHING", 0)))
		run(referenceExec("CREATE TABLE added(v INT)", 0))
		run(referenceExec("INSERT INTO added VALUES(1)", 1))
		run(referenceExec("ALTER TABLE added ADD CONSTRAINT ax EXCLUDE(v WITH =)", 0))
		run(referenceExec("ALTER TABLE added DROP CONSTRAINT ax", 0))
		if !b.pg {
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
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || redump.String() != dump.String() {
				t.Fatal(redump.String(), err)
			}
			run(referenceErrorDiagnostic("INSERT INTO t VALUES(4,'a',TRUE)", want))
			run(referenceErrorDiagnostic("INSERT INTO h VALUES(1,TRUE)", exclusionReferenceConflict("h", "hx", "v", "1", "1")))
		}
	})
}
