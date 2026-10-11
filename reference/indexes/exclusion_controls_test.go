package reference_test

import (
	"bytes"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExclusionControls(t *testing.T) {
	fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, b.DB, s) }
		run(fixture.Exec("CREATE TABLE t(v INT,note TEXT,active BOOL,CONSTRAINT ex EXCLUDE USING btree(lower(note) WITH =) WHERE(active) DEFERRABLE INITIALLY DEFERRED)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'A',TRUE),(2,'a',FALSE),(3,NULL,TRUE)", 3))
		wantRows := fixture.Select("SELECT v,note,active FROM t ORDER BY v", []string{"v", "note", "active"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), "A", true}, {int64(2), "a", false}, {int64(3), nil, true}})
		run(wantRows)
		run(fixture.Prepared(wantRows))
		want := fixture.ExclusionReferenceConflict("t", "ex", "lower(note)", "a", "a")
		run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(4,'a',TRUE)", want))
		if b.PG {
			return
		} // Native controls have no server control API equivalent.
		conn, err := p.Native.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		err = conn.Raw(func(raw any) error {
			rows, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, wantRows.Query, nil)
			if err != nil {
				return err
			}
			defer rows.Close()
			var original bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &original); err != nil {
				return err
			}
			if strings.Count(original.String(), "EXCLUDE") != 1 || strings.Contains(original.String(), "CREATE INDEX ex") {
				t.Fatalf("constraint/index must serialize once: %s", original.String())
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				return err
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(original.Bytes())); err != nil {
				return err
			}
			var restored bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &restored); err != nil {
				return err
			}
			if restored.String() != original.String() {
				t.Fatalf("exclusion redump changed: %s\nwant %s", restored.String(), original.String())
			}
			run(wantRows)
			run(fixture.Prepared(wantRows))
			run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO t VALUES(4,'a',TRUE)", want)))
			// Transient checking mode does not become a persistent definition.
			c := fixture.ConcurrentIndexConn(t, p, b)
			fixture.ExclusionReferenceRun(t, p, b, c, fixture.Exec("BEGIN", 0))
			fixture.ExclusionReferenceRun(t, p, b, c, fixture.Exec("SET CONSTRAINTS ex IMMEDIATE", 0))
			var during bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &during); err != nil {
				return err
			}
			if during.String() != original.String() {
				t.Fatal("transaction mode serialized")
			}
			fixture.ExclusionReferenceRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
			// A live TEMP definition and its OIDs survive replacement staging.
			fixture.ExclusionReferenceRun(t, p, b, c, fixture.Exec("CREATE TEMP TABLE retained(v INT)", 0))
			fixture.ExclusionReferenceRun(t, p, b, c, fixture.Exec("INSERT INTO retained VALUES(9)", 1))
			for _, script := range []string{
				"CREATE TABLE replacement(v INT,CONSTRAINT bad EXCLUDE(v WITH =)); INSERT INTO replacement VALUES(1),(1)",
				"CREATE TABLE replacement(v INT,CONSTRAINT bad EXCLUDE(v WITH <>))",
			} {
				err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
				d := fixture.ExclusionReferenceConflict("replacement", "bad", "v", "1", "1")
				if strings.Contains(script, "<>") {
					d = fixture.ControlsReferenceDiagnostic("42809", `operator <>(integer,integer) is not a member of operator family "integer_ops"`)
					d.Detail = "The exclusion operator must be related to the index operator class for the constraint."
				}
				fixture.AssertDiagnostic(t, err, &d)
				var unchanged bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &unchanged); err != nil {
					return err
				}
				if unchanged.String() != original.String() {
					t.Fatal("failed Restore mutated destination")
				}
				run(wantRows)
				fixture.ExclusionReferenceRun(t, p, b, c, fixture.ExclusionReferenceRows("SELECT v FROM retained", 9))
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
	fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.ConcurrentIndexConn(t, p, b)
		run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE TABLE t(v INT,note TEXT,active BOOL,CONSTRAINT ex EXCLUDE(lower(note) WITH =) WHERE(active) DEFERRABLE INITIALLY DEFERRED)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'A',TRUE),(2,'a',FALSE)", 2))
		run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES($1,$2,$3)", 1, int64(3), nil, true)))
		run(fixture.Prepared(fixture.Select("SELECT v,note,active FROM t ORDER BY v", []string{"v", "note", "active"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), "A", true}, {int64(2), "a", false}, {int64(3), nil, true}})))
		want := fixture.ExclusionReferenceConflict("t", "ex", "lower(note)", "a", "a")
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO t VALUES(4,'a',TRUE)", 1))
		run(fixture.Exec("SAVEPOINT s", 0))
		run(fixture.ErrorDiagnostic("SET CONSTRAINTS ex IMMEDIATE", want))
		run(fixture.Exec("ROLLBACK TO s", 0))
		run(fixture.Exec("DELETE FROM t WHERE v=4", 1))
		run(fixture.Exec("SET CONSTRAINTS ex IMMEDIATE", 0))
		run(fixture.Exec("COMMIT", 0))
		run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(4,'a',TRUE)", want))
		run(fixture.Exec("CREATE TABLE h(v INT,active BOOL,CONSTRAINT hx EXCLUDE USING hash(v WITH =) WHERE(active))", 0))
		run(fixture.Exec("INSERT INTO h VALUES(1,TRUE),(1,FALSE)", 2))
		run(fixture.ErrorDiagnostic("INSERT INTO h VALUES(1,TRUE)", fixture.ExclusionReferenceConflict("h", "hx", "v", "1", "1")))
		run(fixture.Exec("INSERT INTO h VALUES(1,TRUE),(2,TRUE) ON CONFLICT DO NOTHING", 1))
		run(fixture.Prepared(fixture.Exec("INSERT INTO h VALUES(1,TRUE) ON CONFLICT ON CONSTRAINT hx DO NOTHING", 0)))
		run(fixture.Exec("CREATE TABLE added(v INT)", 0))
		run(fixture.Exec("INSERT INTO added VALUES(1)", 1))
		run(fixture.Exec("ALTER TABLE added ADD CONSTRAINT ax EXCLUDE(v WITH =)", 0))
		run(fixture.Exec("ALTER TABLE added DROP CONSTRAINT ax", 0))
		if !b.PG {
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
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != dump.String() {
				t.Fatal(redump.String(), err)
			}
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(4,'a',TRUE)", want))
			run(fixture.ErrorDiagnostic("INSERT INTO h VALUES(1,TRUE)", fixture.ExclusionReferenceConflict("h", "hx", "v", "1", "1")))
		}
	})
}
