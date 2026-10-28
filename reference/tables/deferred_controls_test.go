package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func deferredFeatureSetup(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
	run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)", 0))
	run(fixture.Exec("CREATE TABLE u(id INT,v INT,CONSTRAINT uq UNIQUE(v) INITIALLY DEFERRED)", 0))
	run(fixture.Exec("CREATE TABLE k(id INT,v INT,CONSTRAINT pk PRIMARY KEY(v) INITIALLY DEFERRED)", 0))
	run(fixture.Exec("CREATE TABLE x(id INT,v INT,CONSTRAINT ex EXCLUDE(v WITH =) INITIALLY DEFERRED)", 0))
}

func deferredFeatureFlags() fixture.Step {
	return fixture.DeferredConstraintFlags("SELECT conname,condeferrable,condeferred FROM pg_constraint WHERE conname IN ('fk','uq','pk','ex') ORDER BY conname", []any{"ex", true, true}, []any{"fk", true, true}, []any{"pk", true, true}, []any{"uq", true, true})
}

func deferredFeatureRows(run func(fixture.Step)) {
	for _, name := range []string{"u", "k", "x"} {
		run(fixture.Select("SELECT id,v FROM "+name+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
	}
	run(fixture.Select("SELECT pid FROM c", []string{"pid"}, []string{"INT4"}, [][]any{{int64(1)}}))
}

func TestReferenceDeferredConstraintFeatureSmoke(t *testing.T) {
	fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		deferredFeatureSetup(run)
		definition := fixture.Select("SELECT pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conname='fk'", []string{"definition"}, []string{"TEXT"}, [][]any{{"FOREIGN KEY (pid) REFERENCES p(id) DEFERRABLE INITIALLY DEFERRED"}})
		run(definition)
		run(fixture.Prepared(definition))
		flags := deferredFeatureFlags()
		held, err := c.PrepareContext(p.Ctx, flags.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO c VALUES($1)", 1, int64(1))))
		for _, name := range []string{"u", "k", "x"} {
			run(fixture.Exec("INSERT INTO "+name+" VALUES(1,1),(2,1)", 2))
			run(fixture.Select("SELECT v FROM "+name+" ORDER BY id", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}))
			run(fixture.Prepared(fixture.Exec("DELETE FROM "+name+" WHERE id=$1", 1, int64(2))))
		}
		run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
		run(fixture.Prepared(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0)))
		run(flags)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: flags.Query}, b.Recorder, flags)
		run(fixture.Exec("COMMIT", 0))
		deferredFeatureRows(run)
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO u VALUES(9,9)", 1))
		run(fixture.Exec("INSERT INTO c VALUES(9)", 1))
		run(fixture.ErrorDiagnostic("COMMIT", fixture.ForeignKeyChild("c", "fk", `Key (pid)=(9) is not present in table "p".`)))
		deferredFeatureRows(run)
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT s", 0))
		run(fixture.Exec("INSERT INTO c VALUES(9)", 1))
		run(fixture.Prepared(fixture.ErrorDiagnostic("SET CONSTRAINTS fk IMMEDIATE", fixture.ForeignKeyChild("c", "fk", `Key (pid)=(9) is not present in table "p".`))))
		run(fixture.Exec("ROLLBACK TO s", 0))
		run(fixture.Exec("INSERT INTO c VALUES(9)", 1))
		run(fixture.Exec("DELETE FROM c WHERE pid=9", 1))
		run(fixture.Exec("SET CONSTRAINTS fk IMMEDIATE", 0))
		run(fixture.Exec("COMMIT", 0))
		if b.PG {
			return
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
		run(flags)
		deferredFeatureRows(run)
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO c VALUES(9)", 1))
		run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
		run(fixture.Exec("COMMIT", 0))
	})
}

type deferredBrokenReader struct{}

func (deferredBrokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestReferenceDeferredConstraintControls(t *testing.T) {
	fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		deferredFeatureSetup(run)
		run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
		for _, name := range []string{"u", "k", "x"} {
			run(fixture.Exec("INSERT INTO "+name+" VALUES(1,1)", 1))
		}
		run(deferredFeatureFlags())
		deferredFeatureRows(run)
		if b.PG {
			return
		}
		run(fixture.Exec("CREATE TABLE sentinel(v INT)", 0))
		run(fixture.Exec("INSERT INTO sentinel VALUES(91)", 1))
		run(fixture.Exec("CREATE TEMP TABLE retained(v INT)", 0))
		run(fixture.Exec("INSERT INTO retained VALUES(7)", 1))
		var tempOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'retained'::regclass::oid::bigint").Scan(&tempOID); err != nil {
			t.Fatal(err)
		}
		var original bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &original); err != nil {
			t.Fatal(err)
		}
		source := original.String()
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0))
		run(fixture.Exec("SET CONSTRAINTS fk DEFERRED", 0))
		run(fixture.Exec("INSERT INTO c VALUES(9)", 1))
		var during bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &during); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(original.Bytes(), during.Bytes()) {
			t.Fatal("Dump included private rows, events or SET modes")
		}
		run(fixture.Exec("ROLLBACK", 0))
		// Retain the actual driver Rows across replacement; native rows own snapshots.
		raw, err := p.Native.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		err = raw.Raw(func(conn any) error {
			rows, err := conn.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT v FROM sentinel", nil)
			if err != nil {
				return err
			}
			defer rows.Close()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				return err
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(original.Bytes())); err != nil {
				return err
			}
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
				return err
			}
			if !bytes.Equal(original.Bytes(), redump.Bytes()) {
				t.Fatal("definition/data redump changed")
			}
			for _, tc := range []struct {
				script     string
				diagnostic fixture.Diagnostic
			}{
				{"CREATE TABLE replacement(v INT,CONSTRAINT bad UNIQUE(v) INITIALLY DEFERRED); INSERT INTO replacement VALUES(1),(1)", fixture.DeferredUniqueConflict("replacement", "bad", "v", "1")},
				{"CREATE TABLE parent(id INT PRIMARY KEY);CREATE TABLE replacement(v INT CONSTRAINT bad REFERENCES parent INITIALLY DEFERRED);INSERT INTO replacement VALUES(9)", fixture.ForeignKeyChild("replacement", "bad", `Key (v)=(9) is not present in table "parent".`)},
			} {
				fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(tc.script)), &tc.diagnostic)
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
					return err
				}
				if after.String() != source {
					t.Fatal("late failure mutated destination")
				}
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, deferredBrokenReader{}); !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(original.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			var unchanged bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &unchanged); err != nil {
				return err
			}
			if unchanged.String() != source || original.String() != source {
				t.Fatal("I/O/cancellation changed destination or source")
			}
			if !reflect.DeepEqual(rows.Columns(), []string{"v"}) {
				t.Fatal(rows.Columns())
			}
			if rows.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "INT4" {
				t.Fatal("held type changed")
			}
			dst := make([]driver.Value, 1)
			if err := rows.Next(dst); err != nil || !reflect.DeepEqual(dst, []driver.Value{int64(91)}) {
				t.Fatal(dst, err)
			}
			if err := rows.Next(dst); err != io.EOF {
				t.Fatal(err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		run(deferredFeatureFlags())
		deferredFeatureRows(run)
		run(fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
		run(fixture.Select("SELECT v FROM retained", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.Select("SELECT 'retained'::regclass::oid::bigint AS oid", []string{"oid"}, []string{"INT8"}, [][]any{{tempOID}}))
		// Existing isolated replay must admit forward cyclic deferred references.
		cycle := "CREATE TABLE a(id INT PRIMARY KEY,b INT,CONSTRAINT ab FOREIGN KEY(b) REFERENCES z INITIALLY DEFERRED);CREATE TABLE z(id INT PRIMARY KEY,a INT,CONSTRAINT za FOREIGN KEY(a) REFERENCES a INITIALLY DEFERRED);INSERT INTO a VALUES(1,1);INSERT INTO z VALUES(1,1)"
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(cycle)); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT id,b FROM a", []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
	})
}
