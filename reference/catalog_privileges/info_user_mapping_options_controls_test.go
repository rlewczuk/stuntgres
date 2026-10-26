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

func (c *umoCase) matrix() {
	c.t.Helper()
	c.check(c.rows("all"))
	c.actor("umo_alice", c.rows("umo_alice"))
	c.actor("umo_reader", c.rows("none"))
	c.actor("umo_owner", c.rows("PUBLIC"))
	c.actor("umo_denied", nil)
}

func TestReferenceInfoUserMappingOptionsNativeControls(t *testing.T) {
	umoScenario(t, func(c *umoCase) {
		c.seed()
		c.ownership()
		c.matrix()
		special := "quote=' slash=\\ é  "
		c.exec("ALTER USER MAPPING FOR umo_alice SERVER umo_server OPTIONS(ADD special '" + strings.ReplaceAll(special, "'", "''") + "')")
		specialQuery := "SELECT " + umoProjection + " FROM information_schema.user_mapping_options WHERE foreign_server_name='umo_server' AND option_name='special'"
		c.routes(specialQuery, [][]any{c.row("umo_alice", "umo_server", "special", special)})
		if c.b.PG {
			return
		} // Public controls have no PostgreSQL counterpart.
		c.exec("CREATE TABLE umo_sentinel(v integer)")
		c.run(fixture.Exec("INSERT INTO umo_sentinel VALUES(7)", 1))
		var dump bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		c.routes(specialQuery, [][]any{c.row("umo_alice", "umo_server", "special", special)})
		old, err := c.conn.QueryContext(c.p.Ctx, umoQuery, "umo_server")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		base := c.rows("all")
		withSpecial := [][]any{base[0], base[1], base[2], c.row("umo_alice", "umo_server", "special", special), base[3], base[4]}
		if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
			t.Fatal(err)
		}
		c.check(nil)
		c.consume(old, withSpecial)
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		c.check(withSpecial)
		c.routes(specialQuery, [][]any{c.row("umo_alice", "umo_server", "special", special)})
		// The extra option is checked independently, then remove it for the canonical matrix.
		c.exec("ALTER USER MAPPING FOR umo_alice SERVER umo_server OPTIONS(DROP special)")
		c.matrix()
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		connector, err := stuntgres.NewDriver().OpenConnector("umo independent destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		for _, role := range umoRoles {
			fixture.RunStep(t, c.p.Ctx, recorded, recorder, fixture.Exec("CREATE ROLE "+role, 0))
		}
		if err := stuntgres.Restore(c.p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		conn, err := recorded.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		target := *c
		target.conn = conn
		target.recorder = recorder
		if err := conn.QueryRowContext(c.p.Ctx, "SELECT current_database()").Scan(&target.database); err != nil {
			t.Fatal(err)
		}
		target.routes(specialQuery, [][]any{target.row("umo_alice", "umo_server", "special", special)})
		target.exec("ALTER USER MAPPING FOR umo_alice SERVER umo_server OPTIONS(DROP special)")
		target.matrix()
		// A late failure cannot publish the earlier real dump or staged relation.
		prefix := dump.String() + "\nCREATE TABLE umo_staged(v integer); INSERT INTO umo_staged VALUES("
		err = stuntgres.Restore(c.p.Ctx, c.p.Native, strings.NewReader(prefix+"'bad');"))
		diagnostic := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		diagnostic.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
		fixture.AssertDiagnostic(t, err, &diagnostic)
		unchanged := func() {
			c.check(withSpecial)
			c.run(fixture.Select("SELECT v FROM umo_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			c.run(fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='umo_staged'", []string{"relname"}, []string{"NAME"}, nil))
		}
		unchanged()
		ctx, cancel := context.WithCancel(c.p.Ctx)
		cancel()
		err = stuntgres.Restore(ctx, c.p.Native, bytes.NewReader(dump.Bytes()))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Restore=%v", err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatalf("context error acquired SQLSTATE %s", state.SQLState())
		}
		unchanged()
	})
}

func TestReferenceInfoUserMappingOptionsFeatureSmoke(t *testing.T) {
	umoScenario(t, func(c *umoCase) {
		c.exec("CREATE SERVER umo_server FOREIGN DATA WRAPPER umo_fdw",
			"CREATE USER MAPPING FOR umo_alice SERVER umo_server OPTIONS(empty '',password 'a=é')",
			"CREATE USER MAPPING FOR umo_bob SERVER umo_server OPTIONS(password 'b')",
			"CREATE USER MAPPING FOR PUBLIC SERVER umo_server OPTIONS(password 'p')",
			"GRANT USAGE ON FOREIGN SERVER umo_server TO umo_alice,umo_reader")
		rows := func(disclose, password string) [][]any {
			want := [][]any{c.row("PUBLIC", "umo_server", "password", "p"), c.row("umo_alice", "umo_server", "empty", ""), c.row("umo_alice", "umo_server", "password", password), c.row("umo_bob", "umo_server", "password", "b")}
			for _, row := range want {
				if disclose != "all" && disclose != row[0] {
					row[4] = nil
				}
			}
			return want
		}
		c.check(rows("all", "a=é"))
		c.actor("umo_alice", rows("umo_alice", "a=é"))
		c.actor("umo_reader", rows("none", "a=é"))
		c.actor("umo_denied", nil)
		stmt, held := c.hold()
		admin, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		c.exec("SET SESSION AUTHORIZATION umo_alice")
		held(c.selectStep(umoQuery, rows("umo_alice", "a=é"), "umo_server"))
		fixture.RunStep(t, c.p.Ctx, admin, c.b.Recorder, fixture.Exec("ALTER USER MAPPING FOR umo_alice SERVER umo_server OPTIONS(SET password 'changed')", 0))
		held(c.selectStep(umoQuery, rows("umo_alice", "changed"), "umo_server"))
		c.exec("RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION umo_reader")
		held(c.selectStep(umoQuery, rows("none", "changed"), "umo_server"))
		fixture.RunStep(t, c.p.Ctx, admin, c.b.Recorder, fixture.Exec("REVOKE USAGE ON FOREIGN SERVER umo_server FROM umo_reader", 0))
		held(c.selectStep(umoQuery, nil, "umo_server"))
		c.exec("RESET SESSION AUTHORIZATION")
		umoMissingColumn(c)
		c.check(rows("all", "changed"))
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		if c.b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
			t.Fatal(err)
		}
		c.check(nil)
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		c.check(rows("all", "changed"))
		c.actor("umo_alice", rows("umo_alice", "changed"))
		c.actor("umo_reader", nil)
		c.actor("umo_denied", nil)
	})
}
