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

func kcuRoundtrip(c *kcuCase, pattern string, want [][]any) {
	c.t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
		c.t.Fatal(err)
	}
	text := strings.ToLower(dump.String())
	if strings.Contains(text, "insert into information_schema.key_column_usage") || strings.Contains(text, "create view information_schema.key_column_usage") || strings.Contains(text, "create table information_schema.key_column_usage") {
		c.t.Fatal("virtual rows serialized", dump.String())
	}
	c.check(c.conn, pattern, want...)
	old, err := c.conn.QueryContext(c.p.Ctx, kcuQuery, pattern)
	if err != nil {
		c.t.Fatal(err)
	}
	defer old.Close()
	saved := kcuMetadata(c.t, old, false)
	if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
		c.t.Fatal(err)
	}
	c.check(c.conn, pattern)
	kcuAssertRows(c.t, old, want)
	for i, typ := range saved {
		if typ.Name() != fixture.KCUColumns[i] || typ.DatabaseTypeName() != kcuTypes[i] {
			c.t.Fatal("retained metadata changed")
		}
	}
	for range 2 {
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			c.t.Fatal(err)
		}
		c.check(c.conn, pattern, want...)
		var again bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &again); err != nil {
			c.t.Fatal(err)
		}
		dump = again
	}
	connector, err := stuntgres.NewDriver().OpenConnector("kcu-independent-controls")
	if err != nil {
		c.t.Fatal(err)
	}
	independent := sql.OpenDB(connector)
	defer independent.Close()
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
		fresh[i][3] = database
	}
	for _, prepared := range []bool{false, true} {
		var e fixture.Executor = independent
		if prepared {
			e = c.hold(independent, kcuQuery)
		}
		rows, err := e.QueryContext(c.p.Ctx, kcuQuery, pattern)
		if err != nil {
			c.t.Fatal(err)
		}
		kcuMetadata(c.t, rows, false)
		kcuAssertRows(c.t, rows, fresh)
	}
}

func TestReferenceInfoKeyColumnUsageNativeControls(t *testing.T) {
	kcuScenario(t, func(c *kcuCase) {
		c.exec("CREATE TABLE kcu_ctl_parent(a integer,b integer,CONSTRAINT kcu_ctl_u UNIQUE(a,b))", "CREATE TABLE kcu_ctl_child(x integer,y integer,CONSTRAINT kcu_ctl_fk FOREIGN KEY(x,y) REFERENCES kcu_ctl_parent(b,a))", "CREATE TABLE kcu_ctl_index_parent(a integer,b integer,payload text)", "CREATE UNIQUE INDEX kcu_ctl_provider ON kcu_ctl_index_parent(b,a) INCLUDE(payload)", "CREATE TABLE kcu_ctl_index_child(x integer,y integer,CONSTRAINT kcu_ctl_index_fk FOREIGN KEY(x,y) REFERENCES kcu_ctl_index_parent(a,b))")
		want := [][]any{c.pub("kcu_ctl_u", "kcu_ctl_parent", "a", 1, nil), c.pub("kcu_ctl_u", "kcu_ctl_parent", "b", 2, nil), c.pub("kcu_ctl_fk", "kcu_ctl_child", "x", 1, int64(2)), c.pub("kcu_ctl_fk", "kcu_ctl_child", "y", 2, int64(1)), c.pub("kcu_ctl_index_fk", "kcu_ctl_index_child", "x", 1, int64(2)), c.pub("kcu_ctl_index_fk", "kcu_ctl_index_child", "y", 2, int64(1))}
		c.check(c.conn, "kcu_ctl_%", want...)
		c.run(c.conn, fixture.Exec("INSERT INTO kcu_ctl_parent VALUES(1,2)", 1))
		c.run(c.conn, fixture.Exec("INSERT INTO kcu_ctl_child VALUES(2,1)", 1))
		c.check(c.conn, "kcu_ctl_%", want...)
		if c.b.PG {
			return
		}
		kcuRoundtrip(c, "kcu_ctl_%", want)
		c.exec("CREATE TABLE kcu_ctl_sentinel(v integer)")
		c.run(c.conn, fixture.Exec("INSERT INTO kcu_ctl_sentinel VALUES(91)", 1))
		prefix := "CREATE TABLE kcu_staged(a integer CONSTRAINT kcu_staged_p PRIMARY KEY); INSERT INTO kcu_staged VALUES("
		err := stuntgres.Restore(c.p.Ctx, c.p.Native, strings.NewReader(prefix+"'bad');"))
		diagnostic := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		diagnostic.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
		fixture.AssertDiagnostic(c.t, err, &diagnostic)
		unchanged := func() {
			c.check(c.conn, "kcu_ctl_%", want...)
			c.check(c.conn, "kcu_staged")
			c.run(c.conn, fixture.Select("SELECT v FROM kcu_ctl_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
			c.run(c.conn, fixture.Select("SELECT relname FROM pg_class WHERE relname='kcu_staged'", []string{"relname"}, []string{"NAME"}, nil))
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

func TestReferenceInfoKeyColumnUsageFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"kcu_smoke_reader"})
	kcuScenario(t, func(c *kcuCase) {
		c.exec("CREATE ROLE kcu_smoke_reader", "CREATE TABLE kcu_smoke_parent(a integer,b integer,CONSTRAINT kcu_smoke_u UNIQUE(a,b))", "CREATE TABLE kcu_smoke_child(x integer,y integer,CONSTRAINT kcu_smoke_fk FOREIGN KEY(x,y) REFERENCES kcu_smoke_parent(b,a))")
		base := [][]any{c.pub("kcu_smoke_u", "kcu_smoke_parent", "a", 1, nil), c.pub("kcu_smoke_u", "kcu_smoke_parent", "b", 2, nil)}
		want := append(append([][]any(nil), base...), c.pub("kcu_smoke_fk", "kcu_smoke_child", "x", 1, int64(2)), c.pub("kcu_smoke_fk", "kcu_smoke_child", "y", 2, int64(1)))
		held := c.hold(c.conn, kcuQuery)
		c.check(c.conn, "kcu_smoke_%", want...)
		c.check(held, "kcu_smoke_%", want...)
		c.exec("GRANT SELECT(y) ON kcu_smoke_child TO kcu_smoke_reader", "SET ROLE kcu_smoke_reader")
		c.check(held, "kcu_smoke_%", c.pub("kcu_smoke_fk", "kcu_smoke_child", "y", 2, int64(1)))
		c.exec("RESET ROLE", "REVOKE SELECT(y) ON kcu_smoke_child FROM kcu_smoke_reader", "SET ROLE kcu_smoke_reader")
		c.check(held, "kcu_smoke_%")
		c.exec("RESET ROLE", "ALTER TABLE kcu_smoke_child RENAME CONSTRAINT kcu_smoke_fk TO kcu_smoke_renamed")
		want = append(append([][]any(nil), base...), c.pub("kcu_smoke_renamed", "kcu_smoke_child", "x", 1, int64(2)), c.pub("kcu_smoke_renamed", "kcu_smoke_child", "y", 2, int64(1)))
		c.check(c.conn, "kcu_smoke_%", want...)
		c.check(held, "kcu_smoke_%", want...)
		c.exec("ALTER TABLE kcu_smoke_child DROP CONSTRAINT kcu_smoke_renamed")
		c.check(held, "kcu_smoke_%", base...)
		c.exec("ALTER TABLE kcu_smoke_child ADD CONSTRAINT kcu_smoke_renamed FOREIGN KEY(y,x) REFERENCES kcu_smoke_parent(b,a)")
		want = append(append([][]any(nil), base...), c.pub("kcu_smoke_renamed", "kcu_smoke_child", "y", 1, int64(2)), c.pub("kcu_smoke_renamed", "kcu_smoke_child", "x", 2, int64(1)))
		c.check(c.conn, "kcu_smoke_%", want...)
		c.check(held, "kcu_smoke_%", want...)
		if !c.b.PG {
			kcuRoundtrip(c, "kcu_smoke_%", want)
		}
	})
}
