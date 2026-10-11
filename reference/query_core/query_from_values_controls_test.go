package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryFromValuesControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE t(n INT)", 0), fixture.Exec("INSERT INTO t VALUES(1),(2)", 2),
			fixture.Exec("CREATE VIEW q AS SELECT * FROM (VALUES(1,'a'),(1,'a'),(2,NULL)) v(n,s)", 0),
			fixture.Exec("CREATE VIEW joined AS SELECT t.n,v.s FROM t JOIN (VALUES(1,'a'),(1,'a'),(3,'c')) v(n,s) USING(n)", 0),
			fixture.Exec("CREATE VIEW bare AS VALUES(1),(NULL)", 0),
		} {
			run(s)
		}
		witness := fixture.Select("SELECT * FROM q ORDER BY n,s", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}})
		joined := fixture.Select("SELECT * FROM joined ORDER BY n,s", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "a"}})
		run(witness)
		run(joined)
		run(fixture.Prepared(witness))
		run(fixture.Exec("ALTER TABLE t RENAME TO renamed_t", 0))
		run(joined)
		for _, prepared := range []bool{false, true} {
			failure := fixture.ImplicitError("CREATE OR REPLACE VIEW q AS SELECT * FROM (VALUES('bad','a')) v(n,s)", fixture.Diagnostic{Code: "42P16", Message: `cannot change data type of view column "n" from integer to text`})
			failure.Prepared, failure.ErrorStage = prepared, "execute"
			run(failure)
			run(witness)
			failure = fixture.ImplicitError("DROP TABLE renamed_t RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop table renamed_t because other objects depend on it", Detail: "view joined depends on table renamed_t", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
			failure.Prepared, failure.ErrorStage = prepared, "execute"
			run(failure)
			run(joined)
		}
		var definition string
		if err := backend.DB.QueryRowContext(p.Ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname=$1", "joined").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		s := joined
		s.Query = definition
		run(s)
		if backend.PG {
			continue
		} // Public controls have no PostgreSQL API counterpart.
		conn, err := p.Native.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		err = conn.Raw(func(raw any) error {
			producer := raw.(driver.QueryerContext)
			standalone, err := producer.QueryContext(p.Ctx, "VALUES(1,'old'),(1,'old'),(2,NULL) ORDER BY column1", nil)
			if err != nil {
				return err
			}
			defer standalone.Close()
			mixed, err := producer.QueryContext(p.Ctx, "SELECT t.n,v.n FROM renamed_t t CROSS JOIN (VALUES(7),(7)) v(n) ORDER BY t.n,v.n", nil)
			if err != nil {
				return err
			}
			defer mixed.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				return err
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				return err
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				return err
			}
			run(witness)
			run(joined)
			run(fixture.Select("SELECT * FROM bare ORDER BY column1", []string{"column1"}, []string{"INT4"}, [][]any{{int64(1)}, {nil}}))
			for _, script := range []string{
				"CREATE TABLE replacement(n INT); CREATE VIEW bad AS VALUES($1)",
				"CREATE TABLE replacement(n INT); CREATE VIEW bad AS SELECT * FROM (VALUES($1)) v(n)",
			} {
				want := fixture.ControlsReferenceDiagnostic("42P02", "there is no parameter $1")
				want.Position = strconv.Itoa(strings.Index(script, "$1") + 1)
				fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &want)
				run(witness)
				run(joined)
			}
			want := fixture.ControlsReferenceDiagnostic("0A000", "restore INSERT query sources are unsupported")
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE replacement(n INT); INSERT INTO replacement SELECT * FROM (VALUES(1)) v(n)")), &want)
			run(witness)
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE renamed_t(n INT); INSERT INTO renamed_t VALUES(99)")); err != nil {
				return err
			}
			drain := func(cursor driver.Rows, columns, types []string, expected [][]driver.Value) error {
				if !reflect.DeepEqual(cursor.Columns(), columns) {
					t.Fatal("retained columns", cursor.Columns(), columns)
				}
				names := cursor.Columns()
				names[0] = "mutated"
				if !reflect.DeepEqual(cursor.Columns(), columns) {
					t.Fatal("borrowed column labels")
				}
				meta := cursor.(driver.RowsColumnTypeDatabaseTypeName)
				for i, typ := range types {
					if meta.ColumnTypeDatabaseTypeName(i) != typ {
						t.Fatal("retained type", i, meta.ColumnTypeDatabaseTypeName(i), typ)
					}
				}
				dest := make([]driver.Value, len(columns))
				var saved []driver.Value
				for i, row := range expected {
					if err := cursor.Next(dest); err != nil {
						return err
					}
					if !reflect.DeepEqual(dest, row) {
						t.Fatal("retained row", dest, row)
					}
					if i == 0 {
						saved = append([]driver.Value(nil), dest...)
					}
				}
				if err := cursor.Next(dest); err != io.EOF {
					return errors.New("retained cursor did not reach EOF")
				}
				if err := cursor.Close(); err != nil {
					return err
				}
				if !reflect.DeepEqual(saved, expected[0]) {
					t.Fatal("owned scan changed", saved)
				}
				return nil
			}
			if err := drain(standalone, []string{"column1", "column2"}, []string{"INT4", "TEXT"}, [][]driver.Value{{int64(1), "old"}, {int64(1), "old"}, {int64(2), nil}}); err != nil {
				return err
			}
			return drain(mixed, []string{"n", "n"}, []string{"INT4", "INT4"}, [][]driver.Value{{int64(1), int64(7)}, {int64(1), int64(7)}, {int64(2), int64(7)}, {int64(2), int64(7)}})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceQueryFromValuesPreCanceled(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		for _, q := range []string{"VALUES(1),(NULL)", "SELECT * FROM (VALUES(1),(NULL)) v(n)"} {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := backend.DB.QueryContext(ctx, q)
			if rows != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(rows, err)
			}
			if _, ok := err.(interface{ SQLState() string }); ok {
				t.Fatal("cancellation gained SQLSTATE", err)
			}
			stmt, err := backend.DB.PrepareContext(ctx, q)
			if stmt != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(stmt, err)
			}
			if _, ok := err.(interface{ SQLState() string }); ok {
				t.Fatal("prepare cancellation gained SQLSTATE", err)
			}
			columns := []string{"column1"}
			if strings.HasPrefix(q, "SELECT") {
				columns = []string{"n"}
			}
			s := fixture.Select(q, columns, []string{"INT4"}, [][]any{{int64(1)}, {nil}})
			s.Bag = true
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
		}
	}
}
