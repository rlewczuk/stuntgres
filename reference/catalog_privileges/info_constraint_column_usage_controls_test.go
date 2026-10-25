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

func ccuRoundtrip(c *ccuCase, pattern string, want [][]any) {
	c.t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
		c.t.Fatal(err)
	}
	text := strings.ToLower(dump.String())
	for _, prefix := range []string{"insert into ", "create view ", "create table "} {
		if strings.Contains(text, prefix+"information_schema.constraint_column_usage") {
			c.t.Fatal("virtual rows serialized", dump.String())
		}
	}
	c.check(c.conn, pattern, want...)
	old, err := c.conn.QueryContext(c.p.Ctx, ccuQuery, pattern)
	if err != nil {
		c.t.Fatal(err)
	}
	defer old.Close()
	saved := ccuMetadata(c.t, old, false)
	if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
		c.t.Fatal(err)
	}
	c.check(c.conn, pattern)
	ccuAssertRows(c.t, old, want)
	ccuColumnTypes(c.t, saved, false)
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
	connector, err := stuntgres.NewDriver().OpenConnector("ccu-independent-controls")
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
		fresh[i][0], fresh[i][4] = database, database
	}
	for _, prepared := range []bool{false, true} {
		var e fixture.Executor = independent
		if prepared {
			e = c.hold(independent, ccuQuery)
		}
		rows, err := e.QueryContext(c.p.Ctx, ccuQuery, pattern)
		if err != nil {
			c.t.Fatal(err)
		}
		ccuMetadata(c.t, rows, false)
		ccuAssertRows(c.t, rows, fresh)
	}
}

func TestReferenceInfoConstraintColumnUsageNativeControls(t *testing.T) {
	ccuScenario(t, func(c *ccuCase) {
		c.exec("CREATE TABLE ccu_ctl_parent(a integer,b integer,CONSTRAINT ccu_ctl_u UNIQUE(a,b))",
			"CREATE TABLE ccu_ctl_child(x integer CONSTRAINT ccu_ctl_nn NOT NULL,y integer,CONSTRAINT ccu_ctl_check CHECK(x>0 AND y>0),CONSTRAINT ccu_ctl_fk FOREIGN KEY(x,y) REFERENCES ccu_ctl_parent(b,a))",
			"CREATE TABLE ccu_ctl_child_two(x integer,y integer,CONSTRAINT ccu_ctl_fk FOREIGN KEY(x,y) REFERENCES ccu_ctl_parent(a,b))",
			"CREATE TABLE ccu_ctl_pk(a integer CONSTRAINT ccu_ctl_pk_nn NOT NULL,CONSTRAINT ccu_ctl_p PRIMARY KEY(a))")
		want := [][]any{
			c.pub("ccu_ctl_parent", "a", "ccu_ctl_u"), c.pub("ccu_ctl_parent", "b", "ccu_ctl_u"),
			c.pub("ccu_ctl_parent", "a", "ccu_ctl_fk"), c.pub("ccu_ctl_parent", "b", "ccu_ctl_fk"),
			c.pub("ccu_ctl_parent", "a", "ccu_ctl_fk"), c.pub("ccu_ctl_parent", "b", "ccu_ctl_fk"),
			c.pub("ccu_ctl_child", "x", "ccu_ctl_nn"), c.pub("ccu_ctl_child", "x", "ccu_ctl_check"), c.pub("ccu_ctl_child", "y", "ccu_ctl_check"),
			c.pub("ccu_ctl_pk", "a", "ccu_ctl_p"), c.pub("ccu_ctl_pk", "a", "ccu_ctl_pk_nn"),
		}
		c.check(c.conn, "ccu_ctl_%", want...)
		held := c.hold(c.conn, ccuQuery)
		c.check(held, "ccu_ctl_%", want...)
		c.run(c.conn, fixture.Exec("INSERT INTO ccu_ctl_parent VALUES(1,2)", 1))
		c.run(c.conn, fixture.Exec("INSERT INTO ccu_ctl_child VALUES(2,1)", 1))
		c.check(c.conn, "ccu_ctl_%", want...)
		// PostgreSQL has no native Reset/Dump/Restore API; shared SQL above
		// independently specifies its complete relationship and metadata bag.
		if c.b.PG {
			return
		}
		ccuRoundtrip(c, "ccu_ctl_%", want)
		c.exec("CREATE TABLE ccu_ctl_sentinel(v integer)")
		c.run(c.conn, fixture.Exec("INSERT INTO ccu_ctl_sentinel VALUES(91)", 1))
		prefix := "CREATE TABLE ccu_staged(a integer CONSTRAINT ccu_staged_p PRIMARY KEY); INSERT INTO ccu_staged VALUES("
		err := stuntgres.Restore(c.p.Ctx, c.p.Native, strings.NewReader(prefix+"'bad');"))
		diagnostic := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		diagnostic.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
		fixture.AssertDiagnostic(c.t, err, &diagnostic)
		unchanged := func() {
			c.check(c.conn, "ccu_ctl_%", want...)
			c.check(c.conn, "ccu_staged")
			c.run(c.conn, fixture.Select("SELECT v FROM ccu_ctl_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
			c.run(c.conn, fixture.Select("SELECT relname FROM pg_class WHERE relname='ccu_staged'", []string{"relname"}, []string{"NAME"}, nil))
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

func TestReferenceInfoConstraintColumnUsageFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ccu_smoke_reader"})
	ccuScenario(t, func(c *ccuCase) {
		c.exec("CREATE TABLE ccu_smoke_parent(a integer,b integer,CONSTRAINT ccu_smoke_u UNIQUE(a,b))", "CREATE TABLE ccu_smoke_child(x integer CONSTRAINT ccu_smoke_nn NOT NULL,y integer,CONSTRAINT ccu_smoke_check CHECK(x>0 AND y>0),CONSTRAINT ccu_smoke_fk FOREIGN KEY(x,y) REFERENCES ccu_smoke_parent(b,a))")
		base := [][]any{c.pub("ccu_smoke_parent", "a", "ccu_smoke_u"), c.pub("ccu_smoke_parent", "b", "ccu_smoke_u"), c.pub("ccu_smoke_child", "x", "ccu_smoke_nn"), c.pub("ccu_smoke_child", "x", "ccu_smoke_check"), c.pub("ccu_smoke_child", "y", "ccu_smoke_check")}
		withFK := func(name string) [][]any {
			return append(append([][]any(nil), base...), c.pub("ccu_smoke_parent", "a", name), c.pub("ccu_smoke_parent", "b", name))
		}
		want := withFK("ccu_smoke_fk")
		// Exercise replay before the ACL phase: native logical dump does not
		// serialize explicit relation ACL metadata, even after REVOKE.
		if !c.b.PG {
			ccuRoundtrip(c, "ccu_smoke_%", want)
		}
		c.exec("CREATE ROLE ccu_smoke_reader")
		held := c.hold(c.conn, ccuQuery)
		c.check(c.conn, "ccu_smoke_%", want...)
		c.check(held, "ccu_smoke_%", want...)
		c.exec("GRANT SELECT ON ccu_smoke_parent,ccu_smoke_child TO ccu_smoke_reader", "SET ROLE ccu_smoke_reader")
		c.check(c.conn, "ccu_smoke_%")
		c.check(held, "ccu_smoke_%")
		c.exec("RESET ROLE")
		c.check(held, "ccu_smoke_%", want...)
		c.exec("ALTER TABLE ccu_smoke_child RENAME CONSTRAINT ccu_smoke_fk TO ccu_smoke_renamed")
		want = withFK("ccu_smoke_renamed")
		c.check(c.conn, "ccu_smoke_%", want...)
		c.check(held, "ccu_smoke_%", want...)
		c.exec("ALTER TABLE ccu_smoke_child DROP CONSTRAINT ccu_smoke_renamed")
		c.check(c.conn, "ccu_smoke_%", base...)
		c.check(held, "ccu_smoke_%", base...)
		c.exec("ALTER TABLE ccu_smoke_child ADD CONSTRAINT ccu_smoke_renamed FOREIGN KEY(y,x) REFERENCES ccu_smoke_parent(b,a)")
		c.check(c.conn, "ccu_smoke_%", want...)
		c.check(held, "ccu_smoke_%", want...)
	})
}
