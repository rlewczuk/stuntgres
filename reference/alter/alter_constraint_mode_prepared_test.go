package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterConstraintPreparedMetadata(t *testing.T) {
	constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := enforcementRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT nn NOT NULL)", 0))
		run(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT nn NO INHERIT", 0))
		run(fixture.Exec("CREATE TABLE child() INHERITS(t)", 0))
		run(fixture.Exec("INSERT INTO child VALUES(1)", 1))
		insert := addConstraintHeld(t, p, b, c, "INSERT INTO child VALUES($1)")
		read := addConstraintHeld(t, p, b, c, "SELECT a FROM child ORDER BY a")
		catalogSQL := "SELECT conname,connoinherit,conislocal,coninhcount,convalidated FROM pg_constraint WHERE conrelid=$1::regclass AND conname=$2"
		catalog := addConstraintHeld(t, p, b, c, catalogSQL)
		attach := addConstraintHeld(t, p, b, c, "ALTER TABLE t ALTER CONSTRAINT nn INHERIT")
		detach := addConstraintHeld(t, p, b, c, "ALTER TABLE t ALTER CONSTRAINT nn NO INHERIT")
		validateConstraintHeldRun(t, p, b, catalog, constraintModeNN("t", "nn", true, true, 0, true))
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, true, read.SQL, []string{"a"}, []string{"INT4"}, []bool{true}, 1)
		var old *sql.Rows
		if !b.PG {
			var err error
			old, err = c.QueryContext(p.Ctx, read.SQL)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
		}
		validateConstraintHeldRun(t, p, b, insert, fixture.Exec(insert.SQL, 1, nil))
		run(fixture.Exec("DELETE FROM child WHERE a IS NULL", 1))
		validateConstraintHeldRun(t, p, b, attach, fixture.Exec(attach.SQL, 0))
		id := constraintModeOID(t, p, c, "child", "nn")
		validateConstraintHeldRun(t, p, b, catalog, constraintModeNN("t", "nn", false, true, 0, true))
		validateConstraintHeldRun(t, p, b, catalog, constraintModeNN("child", "nn", false, false, 1, true))
		validateConstraintHeldRun(t, p, b, insert, fixture.ErrorDiagnostic(insert.SQL, fixture.PrimaryKeyReferenceNull("child", "a", "null"), nil))
		validateConstraintHeldRun(t, p, b, read, fixture.Select(read.SQL, []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, true, read.SQL, []string{"a"}, []string{"INT4"}, []bool{false}, 1)
		if old != nil {
			constraintModeDetachedRows(t, old, true, []any{int64(1)})
		}
		validateConstraintHeldRun(t, p, b, detach, fixture.Exec(detach.SQL, 0))
		constraintModeSameOID(t, p, c, "child", "nn", id)
		validateConstraintHeldRun(t, p, b, catalog, constraintModeNN("child", "nn", false, true, 0, true))
		validateConstraintHeldRun(t, p, b, insert, fixture.ErrorDiagnostic(insert.SQL, fixture.PrimaryKeyReferenceNull("child", "a", "null"), nil))
		run(fixture.Exec("CREATE TABLE fresh() INHERITS(t)", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO fresh VALUES(NULL)", 1)))
		run(fixture.Select("SELECT a FROM fresh", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
		// Query metadata is independently specified, including the INT2 count.
		step := constraintModeNN("child", "nn", false, true, 0, true)
		step.CheckMetadata = func(t *testing.T, r *sql.Rows) {
			types, err := r.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			want := []reflect.Type{reflect.TypeOf(""), reflect.TypeOf(false), reflect.TypeOf(false), reflect.TypeOf(int64(0)), reflect.TypeOf(false)}
			if b.PG {
				// pq has no specialized ScanType for PostgreSQL NAME.
				want[0] = reflect.TypeOf((*any)(nil)).Elem()
				want[3] = reflect.TypeOf(int16(0))
			}
			if len(types) != len(want) {
				t.Fatal(types)
			}
			for i, ct := range types {
				if ct.ScanType() != want[i] {
					t.Fatalf("column %d ScanType %v, want %v", i, ct.ScanType(), want[i])
				}
			}
		}
		validateConstraintHeldRun(t, p, b, catalog, step)
	})
}

// Native Rows are detached snapshots: consume only after the mutation/control.
func constraintModeDetachedRows(t *testing.T, r *sql.Rows, nullable bool, want []any) {
	t.Helper()
	defer r.Close()
	cols, err := r.Columns()
	if err != nil {
		t.Fatal(err)
	}
	types, err := r.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cols, []string{"a"}) || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" || types[0].ScanType() != reflect.TypeOf(int64(0)) {
		t.Fatal("detached metadata changed", cols, types)
	}
	if n, known := types[0].Nullable(); !known || n != nullable {
		t.Fatal("detached nullability changed", n, known, nullable)
	}
	for _, v := range want {
		if !r.Next() {
			t.Fatal("detached row missing", r.Err())
		}
		var got any
		if err := r.Scan(&got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(fixture.Normalize(got), v) {
			t.Fatal("detached value changed", got, v)
		}
	}
	if r.Next() || r.Err() != nil {
		t.Fatal("detached tail", r.Err())
	}
}
