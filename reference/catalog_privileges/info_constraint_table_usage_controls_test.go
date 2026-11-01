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

func ctuRoundtrip(c *ctuCase, pattern string, want [][]any) {
	c.t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
		c.t.Fatal(err)
	}
	text := strings.ToLower(dump.String())
	for _, prefix := range []string{"insert into ", "create view ", "create table "} {
		if strings.Contains(text, prefix+"information_schema.constraint_table_usage") || strings.Contains(text, prefix+`"information_schema"."constraint_table_usage"`) {
			c.t.Fatal("derived CTU serialized", dump.String())
		}
	}
	held := c.hold(c.conn, ctuQuery)
	c.routes(held, pattern, want...)
	old, err := c.conn.QueryContext(c.p.Ctx, ctuQuery, pattern)
	if err != nil {
		c.t.Fatal(err)
	}
	defer old.Close()
	saved := ctuMetadata(c.t, old, false)
	// Whole-catalog replacement retires the logical sql_identifier result
	// signature, not the already-open Rows or their captured ColumnTypes.
	reprepare := func() {
		stale := fixture.ErrorDiagnostic(ctuQuery, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type"), pattern)
		stale.SelectRows, stale.ErrorStage = true, "execute"
		c.run(held, stale)
		if err := held.Statement.Close(); err != nil {
			c.t.Fatal(err)
		}
		held = c.hold(c.conn, ctuQuery)
	}
	if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
		c.t.Fatal(err)
	}
	reprepare()
	c.routes(held, pattern)
	ctuCatalogMetadata(c)
	ctuMetadata(c.t, old, false)
	ctuColumnTypes(c.t, saved, false)
	for range 2 {
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			c.t.Fatal(err)
		}
		reprepare()
		c.routes(held, pattern, want...)
		ctuCatalogMetadata(c)
		ctuMetadata(c.t, old, false)
		ctuColumnTypes(c.t, saved, false)
		var again bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &again); err != nil {
			c.t.Fatal(err)
		}
		dump = again
	}
	ctuAssertRows(c.t, old, want, false)
	ctuColumnTypes(c.t, saved, false)
	connector, err := stuntgres.NewDriver().OpenConnector("ctu-independent-controls")
	if err != nil {
		c.t.Fatal(err)
	}
	independent := sql.OpenDB(connector)
	defer independent.Close()
	if err := stuntgres.Restore(c.p.Ctx, independent, bytes.NewReader(dump.Bytes())); err != nil {
		c.t.Fatal(err)
	}
	recorded, recorder := fixture.RecordedNativePool(c.t, connector)
	destination, err := recorded.Conn(c.p.Ctx)
	if err != nil {
		c.t.Fatal(err)
	}
	defer destination.Close()
	copyCase, copyBase := *c, *c.ccuCase
	copyCase.ccuCase = &copyBase
	copyCase.conn, copyCase.b.DB, copyCase.b.Recorder = destination, recorded, recorder
	if err := destination.QueryRowContext(c.p.Ctx, "SELECT current_database()").Scan(&copyCase.database); err != nil {
		c.t.Fatal(err)
	}
	if copyCase.database == c.database {
		c.t.Fatal("independent destination must distinguish database values")
	}
	fresh := make([][]any, len(want))
	for i, row := range want {
		fresh[i] = append([]any(nil), row...)
		fresh[i][0], fresh[i][3] = copyCase.database, copyCase.database
	}
	copyCase.routes(copyCase.hold(destination, ctuQuery), pattern, fresh...)
	ctuCatalogMetadata(&copyCase)
	c.routes(held, pattern, want...)
}

func TestReferenceInfoConstraintTableUsageNativeControls(t *testing.T) {
	ctuScenario(t, func(c *ctuCase) {
		c.exec("CREATE TABLE ctu_ctl_parent(a integer,b integer,CONSTRAINT ctu_ctl_u UNIQUE(a,b))",
			"CREATE TABLE ctu_ctl_child(x integer NOT NULL,y integer,CHECK(x>0 AND y>0),CONSTRAINT ctu_ctl_fk FOREIGN KEY(x,y) REFERENCES ctu_ctl_parent(b,a))",
			"CREATE TABLE ctu_ctl_child_two(x integer,y integer,CONSTRAINT ctu_ctl_fk FOREIGN KEY(x,y) REFERENCES ctu_ctl_parent(a,b))",
			"CREATE TABLE ctu_ctl_pk(a integer CONSTRAINT ctu_ctl_p PRIMARY KEY)")
		want := [][]any{c.pub("ctu_ctl_parent", "ctu_ctl_u"), c.pub("ctu_ctl_parent", "ctu_ctl_fk"), c.pub("ctu_ctl_parent", "ctu_ctl_fk"), c.pub("ctu_ctl_pk", "ctu_ctl_p")}
		held := c.hold(c.conn, ctuQuery)
		c.routes(held, "ctu_ctl_%", want...)
		c.run(c.conn, fixture.Exec("INSERT INTO ctu_ctl_parent VALUES(1,2)", 1))
		c.run(c.conn, fixture.Exec("INSERT INTO ctu_ctl_child VALUES(2,1)", 1))
		c.routes(held, "ctu_ctl_%", want...)
		ctuCatalogMetadata(c)
		// Native host controls have no PostgreSQL equivalent. The shared phase
		// above still proves the exact SQL contract on both backends.
		if c.b.PG {
			return
		}
		ctuRoundtrip(c, "ctu_ctl_%", want)
		// Refresh the pre-roundtrip signature once. Failed or canceled Restore
		// must then preserve this same statement because neither publishes.
		if err := held.Statement.Close(); err != nil {
			c.t.Fatal(err)
		}
		held = c.hold(c.conn, ctuQuery)
		c.routes(held, "ctu_ctl_%", want...)
		c.exec("CREATE TABLE ctu_ctl_sentinel(v integer)")
		c.run(c.conn, fixture.Exec("INSERT INTO ctu_ctl_sentinel VALUES(91)", 1))
		prefix := "CREATE TABLE ctu_staged(a integer CONSTRAINT ctu_staged_p PRIMARY KEY); INSERT INTO ctu_staged VALUES("
		err := stuntgres.Restore(c.p.Ctx, c.p.Native, strings.NewReader(prefix+"'bad');"))
		diagnostic := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		diagnostic.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
		fixture.AssertDiagnostic(c.t, err, &diagnostic)
		unchanged := func() {
			c.routes(held, "ctu_ctl_%", want...)
			c.check(c.conn, "ctu_staged")
			c.run(c.conn, fixture.Select("SELECT v FROM ctu_ctl_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
			c.run(c.conn, fixture.Select("SELECT relname FROM pg_class WHERE relname='ctu_staged'", []string{"relname"}, []string{"NAME"}, nil))
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

func TestReferenceInfoConstraintTableUsageFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ctu_smoke_reader"})
	ctuScenario(t, func(c *ctuCase) {
		c.exec("CREATE TABLE ctu_smoke_parent(a integer,b integer,CONSTRAINT ctu_smoke_u UNIQUE(a,b))",
			"CREATE TABLE ctu_smoke_child(x integer,y integer,CONSTRAINT ctu_smoke_fk FOREIGN KEY(x,y) REFERENCES ctu_smoke_parent(b,a))",
			"CREATE TABLE ctu_smoke_child_two(x integer,y integer,CONSTRAINT ctu_smoke_fk FOREIGN KEY(x,y) REFERENCES ctu_smoke_parent(a,b))",
			"CREATE TABLE ctu_smoke_checks(a integer NOT NULL CHECK(a>0))")
		base := [][]any{c.pub("ctu_smoke_parent", "ctu_smoke_u"), c.pub("ctu_smoke_parent", "ctu_smoke_fk")}
		withFK := func(name string) [][]any {
			return append(append([][]any(nil), base...), c.pub("ctu_smoke_parent", name))
		}
		want := withFK("ctu_smoke_fk")
		// Logical replay precedes ACL mutation; explicit ACL serialization is
		// intentionally not a capability being added by this view.
		if !c.b.PG {
			ctuRoundtrip(c, "ctu_smoke_%", want)
		}
		c.exec("CREATE ROLE ctu_smoke_reader")
		held := c.hold(c.conn, ctuQuery)
		c.routes(held, "ctu_smoke_%", want...)
		c.exec("GRANT SELECT ON ctu_smoke_parent,ctu_smoke_child,ctu_smoke_child_two,ctu_smoke_checks TO ctu_smoke_reader", "SET ROLE ctu_smoke_reader")
		c.routes(held, "ctu_smoke_%")
		c.exec("RESET ROLE")
		c.routes(held, "ctu_smoke_%", want...)
		c.exec("ALTER TABLE ctu_smoke_child RENAME CONSTRAINT ctu_smoke_fk TO ctu_smoke_renamed")
		want = withFK("ctu_smoke_renamed")
		c.routes(held, "ctu_smoke_%", want...)
		c.exec("ALTER TABLE ctu_smoke_child DROP CONSTRAINT ctu_smoke_renamed")
		c.routes(held, "ctu_smoke_%", base...)
		c.exec("ALTER TABLE ctu_smoke_child ADD CONSTRAINT ctu_smoke_renamed FOREIGN KEY(y,x) REFERENCES ctu_smoke_parent(b,a)")
		c.routes(held, "ctu_smoke_%", want...)
	})
}
