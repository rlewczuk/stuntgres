package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnColumnUsageNativeControls(t *testing.T) {
	colcuScenario(t, func(c *colcuCase) {
		c.exec(colcuSeed)
		want := c.seedRows()
		c.check(c.conn, "colcu_usage", want...)
		held := c.hold(c.conn, colcuQuery)
		c.check(held, "colcu_usage", want...)
		// PostgreSQL has no public Reset/Dump/Restore equivalent. The shared
		// SQL phase above specifies the same generated-definition facts.
		if c.b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
			c.t.Fatal(err)
		}
		text := strings.ToLower(dump.String())
		for _, prefix := range []string{"insert into ", "create view ", "create table "} {
			if strings.Contains(text, prefix+"information_schema.column_column_usage") {
				c.t.Fatal("virtual rows serialized", dump.String())
			}
		}
		old, err := c.conn.QueryContext(c.p.Ctx, colcuQuery, "colcu_usage")
		if err != nil {
			c.t.Fatal(err)
		}
		defer old.Close()
		types := colcuMetadata(c.t, old, false)
		// sql_identifier is a logical domain result: successful whole-catalog
		// replacement fences its held signature even though wire metadata is NAME.
		reprepare := func() {
			stale := fixture.ErrorDiagnostic(colcuQuery, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type"), "colcu_usage")
			stale.SelectRows, stale.ErrorStage = true, "execute"
			c.run(held, stale)
			if err := held.Statement.Close(); err != nil {
				c.t.Fatal(err)
			}
			held = c.hold(c.conn, colcuQuery)
		}
		if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
			c.t.Fatal(err)
		}
		reprepare()
		c.check(c.conn, "colcu_usage")
		c.check(held, "colcu_usage")
		colcuAssertRows(c.t, old, want)
		colcuColumnTypes(c.t, types, false)
		var restoredRows *sql.Rows
		var restoredTypes []*sql.ColumnType
		for round := range 2 {
			if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				c.t.Fatal(err)
			}
			reprepare()
			c.check(c.conn, "colcu_usage", want...)
			c.check(held, "colcu_usage", want...)
			if round == 0 {
				// Keep a populated eager cursor unconsumed across the second
				// successful Restore, separately from the held signature fence.
				restoredRows, err = c.conn.QueryContext(c.p.Ctx, colcuQuery, "colcu_usage")
				if err != nil {
					c.t.Fatal(err)
				}
				defer restoredRows.Close()
				restoredTypes = colcuMetadata(c.t, restoredRows, false)
			}
			var again bytes.Buffer
			if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &again); err != nil {
				c.t.Fatal(err)
			}
			dump = again
		}
		colcuColumnTypes(c.t, restoredTypes, false)
		colcuMetadata(c.t, restoredRows, false)
		colcuAssertRows(c.t, restoredRows, want)
		colcuColumnTypes(c.t, restoredTypes, false)
		connector, err := stuntgres.NewDriver().OpenConnector("colcu-independent-controls")
		if err != nil {
			c.t.Fatal(err)
		}
		independent := sql.OpenDB(connector)
		defer independent.Close()
		if _, err := independent.ExecContext(c.p.Ctx, "CREATE TABLE colcu_allocation_shift(a integer)"); err != nil {
			c.t.Fatal(err)
		}
		if err := stuntgres.Restore(c.p.Ctx, independent, bytes.NewReader(dump.Bytes())); err != nil {
			c.t.Fatal(err)
		}
		var database string
		if err := independent.QueryRowContext(c.p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			c.t.Fatal(err)
		}
		fresh := make([][]any, len(want))
		for i, row := range want {
			fresh[i] = append([]any(nil), row...)
			fresh[i][0] = database
		}
		for _, e := range []fixture.Executor{independent, c.hold(independent, colcuQuery)} {
			r, err := e.QueryContext(c.p.Ctx, colcuQuery, "colcu_usage")
			if err != nil {
				c.t.Fatal(err)
			}
			colcuMetadata(c.t, r, false)
			colcuAssertRows(c.t, r, fresh)
		}
		c.exec("CREATE TABLE colcu_ctl_sentinel(v integer)")
		c.run(c.conn, fixture.Exec("INSERT INTO colcu_ctl_sentinel VALUES(91)", 1))
		prefix := "CREATE TABLE colcu_staged(a integer,g integer GENERATED ALWAYS AS(a+1) STORED); INSERT INTO colcu_staged(a) VALUES("
		err = stuntgres.Restore(c.p.Ctx, c.p.Native, strings.NewReader(prefix+"'bad');"))
		d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		d.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
		fixture.AssertDiagnostic(c.t, err, &d)
		unchanged := func() {
			c.check(c.conn, "colcu_usage", want...)
			c.check(held, "colcu_usage", want...)
			c.check(c.conn, "colcu_staged")
			c.run(c.conn, fixture.Select("SELECT v FROM colcu_ctl_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
			c.run(c.conn, fixture.Select("SELECT relname FROM pg_class WHERE relname='colcu_staged'", []string{"relname"}, []string{"NAME"}, nil))
		}
		unchanged()
		ctx, cancel := context.WithCancel(c.p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, c.p.Native, strings.NewReader(prefix+"1);")); !errors.Is(err, context.Canceled) {
			c.t.Fatal(err)
		}
		unchanged()
	})
}

func TestReferenceInfoColumnColumnUsageFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"colcu_smoke_reader"})
	colcuScenario(t, func(c *colcuCase) {
		c.exec(colcuSeed)
		held := c.hold(c.conn, colcuQuery)
		want := c.seedRows()
		c.check(c.conn, "colcu_usage", want...)
		c.check(held, "colcu_usage", want...)
		c.check(c.conn, nil)
		c.check(held, nil)
		c.exec("ALTER TABLE colcu_usage RENAME COLUMN a TO aa")
		want = [][]any{c.pub("colcu_usage", "aa", "s"), c.pub("colcu_usage", "b", "s"), c.pub("colcu_usage", "b", "v"), c.pub("colcu_usage", "aa", "z"), c.pub("colcu_usage", "tableoid", "o")}
		c.check(c.conn, "colcu_usage", want...)
		c.check(held, "colcu_usage", want...)
		c.exec("ALTER TABLE colcu_usage ALTER s DROP EXPRESSION")
		want = [][]any{c.pub("colcu_usage", "b", "v"), c.pub("colcu_usage", "aa", "z"), c.pub("colcu_usage", "tableoid", "o")}
		c.check(c.conn, "colcu_usage", want...)
		c.check(held, "colcu_usage", want...)
		c.exec("CREATE ROLE colcu_smoke_reader", "GRANT SELECT ON colcu_usage TO colcu_smoke_reader", "SET ROLE colcu_smoke_reader")
		c.check(c.conn, "colcu_usage")
		c.check(held, "colcu_usage")
		c.exec("RESET ROLE")
		c.check(held, "colcu_usage", want...)
		s := fixture.ErrorDiagnostic("UPDATE information_schema.column_column_usage SET table_catalog='x' WHERE false", colcuWriteDiagnostic("UPDATE"))
		s.ErrorStage = "execute"
		c.run(c.conn, s)
		s = fixture.Prepared(s)
		s.ErrorStage = "prepare"
		c.run(c.conn, s)
		c.check(held, "colcu_usage", want...)
		c.exec("DROP TABLE colcu_usage")
		c.check(c.conn, "colcu_usage")
		c.check(held, "colcu_usage")
	})
}
