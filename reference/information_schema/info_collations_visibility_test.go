package reference_test

import (
	"database/sql"
	"net/url"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Role reads use the same recording adapter as bootstrap reads, with a
// separate recorder per authenticated pool rather than stale bootstrap data.
func (c *icoCase) loginPool(name string) (*sql.DB, *fixture.Recorder) {
	c.t.Helper()
	if !c.b.PG {
		d := c.p.NativeConnector.Driver().(*stuntgres.Driver)
		connector, err := d.OpenConnectorAs(c.p.NativeDSN, name)
		if err != nil { c.t.Fatal(err) }
		return fixture.RecordedNativePool(c.t, connector)
	}
	u, err := url.Parse(fixture.DSN)
	if err != nil { c.t.Fatal(err) }
	u.User = url.UserPassword(name, "role-core-fixture")
	db, err := sql.Open("stuntgres_reference_postgres", u.String())
	if err != nil { c.t.Fatal(err) }
	c.t.Cleanup(func() { if err := db.Close(); err != nil { c.t.Error(err) } })
	return db, &fixture.Recorder{RawBackend:fixture.RawPostgres}
}

func TestReferenceInfoCollationsVisibility(t *testing.T) {
	icoScenario(t, true, func(c *icoCase) {
		c.exec("CREATE USER icoll_owner", "CREATE USER icoll_reader", "CREATE SCHEMA icoll_hidden AUTHORIZATION icoll_owner")
		if c.b.PG {
			c.exec("ALTER ROLE icoll_owner PASSWORD 'role-core-fixture'", "ALTER ROLE icoll_reader PASSWORD 'role-core-fixture'")
		}
		owner, ownerRecord := c.loginPool("icoll_owner")
		reader, readerRecord := c.loginPool("icoll_reader")
		for _, q := range []string{`CREATE COLLATION icoll_hidden.icoll_visible FROM pg_catalog."C"`, "CREATE TABLE icoll_hidden.witness(id integer)"} {
			fixture.RunStep(t, c.p.Ctx, owner, ownerRecord, fixture.Exec(q, 0))
		}
		q := icoQuery+" WHERE collation_schema=$1 AND collation_name=$2"
		bootstrap := "postgres"
		if c.b.PG { bootstrap = "stuntgres" }
		for _, actor := range []struct{ db *sql.DB; identity string; record *fixture.Recorder }{{c.b.DB,bootstrap,c.b.Recorder},{owner,"icoll_owner",ownerRecord},{reader,"icoll_reader",readerRecord}} {
			conn, err := actor.db.Conn(c.p.Ctx)
			if err != nil { t.Fatal(err) }
			func() {
				c := *c
				c.b.Recorder = actor.record
				defer conn.Close()
				c.run(conn, fixture.Exec("SET search_path=pg_catalog,public", 0))
				c.run(conn, fixture.Select("SELECT current_user,session_user", []string{"current_user","session_user"}, []string{"NAME","NAME"}, [][]any{{actor.identity,actor.identity}}))
				s := c.projection(q, c.row("icoll_hidden","icoll_visible"), "icoll_hidden", "icoll_visible")
				c.run(conn, s)
				c.run(conn, fixture.Prepared(s))
				stmt, err := conn.PrepareContext(c.p.Ctx, q)
				if err != nil { t.Fatal(err) }
				defer stmt.Close()
				c.run(fixture.HeldStatement{Executor:conn, Statement:stmt, SQL:q}, s)
				c.run(conn, fixture.Select(`SELECT pg_collation_is_visible(c.oid) AS visible FROM pg_catalog.pg_collation c JOIN pg_catalog.pg_namespace n ON n.oid=c.collnamespace WHERE n.nspname='icoll_hidden' AND c.collname='icoll_visible'`, []string{"visible"}, []string{"BOOL"}, [][]any{{false}}))
				if actor.identity == "icoll_reader" {
					c.run(conn, fixture.Select("SELECT has_schema_privilege(current_user,'icoll_hidden','USAGE') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{false}}))
					want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema icoll_hidden")
					want.Position = "15"
					for _, prepared := range []bool{false,true} {
						s := fixture.ErrorDiagnostic("SELECT * FROM icoll_hidden.witness", want)
						s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
						if prepared { s.ErrorStage = "prepare" }
						c.run(conn, s)
					}
					// Failed ordinary lookup neither hides nor mutates inquiry rows.
					c.run(conn, fixture.Prepared(s))
				}
			}()
		}
		c.exec("DROP TABLE icoll_hidden.witness", "DROP COLLATION icoll_hidden.icoll_visible", "DROP SCHEMA icoll_hidden")
	})
}
