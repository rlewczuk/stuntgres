package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoCollationsLifecycle(t *testing.T) {
	icoScenario(t, true, func(c *icoCase) {
		c.exec("CREATE ROLE icoll_owner", "CREATE SCHEMA icoll_lifecycle", "CREATE SCHEMA icoll_moved")
		q := icoQuery+" WHERE collation_schema=$1 AND collation_name=$2"
		held := c.hold(q)
		check := func(schema, name string, present bool) {
			var want [][]any
			if present { want = c.row(schema, name) }
			s := c.projection(q, want, schema, name)
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
			c.run(held, s)
		}
		check("icoll_lifecycle", "icoll_copy", false)
		c.exec(`CREATE COLLATION icoll_lifecycle.icoll_copy FROM pg_catalog."C"`)
		check("icoll_lifecycle", "icoll_copy", true)
		var retained *sql.Rows
		if !c.b.PG {
			var err error
			retained, err = held.Statement.QueryContext(c.p.Ctx, "icoll_lifecycle", "icoll_copy")
			if err != nil { t.Fatal(err) }
			defer retained.Close()
			columns, err := retained.ColumnTypes()
			if err != nil { t.Fatal(err) }
			icoMetadata(t, columns, false)
			defer icoMetadata(t, columns, false)
		}
		c.exec("ALTER COLLATION icoll_lifecycle.icoll_copy RENAME TO icoll_renamed")
		check("icoll_lifecycle", "icoll_copy", false)
		check("icoll_lifecycle", "icoll_renamed", true)
		c.exec("ALTER COLLATION icoll_lifecycle.icoll_renamed SET SCHEMA icoll_moved")
		check("icoll_lifecycle", "icoll_renamed", false)
		check("icoll_moved", "icoll_renamed", true)
		c.exec("ALTER COLLATION icoll_moved.icoll_renamed OWNER TO icoll_owner")
		check("icoll_moved", "icoll_renamed", true)
		// Transaction-local CREATE and DROP must be reflected and rollback
		// restores precisely the previously committed metadata bag.
		for _, create := range []bool{true, false} {
			tx, err := c.conn.BeginTx(c.p.Ctx, nil)
			if err != nil { t.Fatal(err) }
			func() {
				defer tx.Rollback()
				name := "icoll_renamed"
				ddl := "DROP COLLATION icoll_moved.icoll_renamed"
				var transient [][]any
				if create {
					name = "icoll_transient"
					ddl = `CREATE COLLATION icoll_moved.icoll_transient FROM pg_catalog."C"`
					transient = c.row("icoll_moved", name)
				}
				c.run(tx, fixture.Exec(ddl, 0))
				s := c.projection(q, transient, "icoll_moved", name)
				c.run(tx, s)
				c.run(tx, fixture.Prepared(s))
				if err := tx.Rollback(); err != nil { t.Fatal(err) }
			}()
			check("icoll_moved", "icoll_renamed", true)
			check("icoll_moved", "icoll_transient", false)
		}
		c.exec("DROP COLLATION icoll_moved.icoll_renamed")
		check("icoll_moved", "icoll_renamed", false)
		if retained != nil { c.consume(retained, c.row("icoll_lifecycle", "icoll_copy")) }
		c.exec("DROP SCHEMA icoll_lifecycle", "DROP SCHEMA icoll_moved")
	})
}
