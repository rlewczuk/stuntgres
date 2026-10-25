package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDataTypePrivilegesSchemaRename(t *testing.T) {
	dtpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE dtp_schema_table(a integer,b varchar(7)[])", 0))
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("ALTER SCHEMA information_schema RENAME TO dtp_information", 0))
		// The oracle resets public, not this bootstrap namespace. Restore it on
		// every assertion-failure path, before returning the physical connection.
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "ALTER SCHEMA dtp_information RENAME TO information_schema"); err != nil {
				t.Error("restore information_schema name", err)
			}
		}()
		var domains [][]any
		for _, name := range []string{"cardinal_number", "character_data", "sql_identifier", "time_stamp", "yes_or_no"} {
			domains = append(domains, []any{database, "dtp_information", name, "DOMAIN", "1"})
		}
		s := fixture.Select("SELECT * FROM dtp_information.data_type_privileges WHERE object_schema='dtp_information' AND object_type='DOMAIN'", dtpColumns, dtpTypes, domains)
		s.Bag = true
		run(s)
		// An unrelated usage must remain readable even though capture visits the
		// renamed seeded domains before applying the query's outer predicate.
		s = fixture.Select("SELECT * FROM dtp_information.data_type_privileges WHERE object_schema='public' AND object_name='dtp_schema_table'", dtpColumns, dtpTypes, [][]any{
			{database, "public", "dtp_schema_table", "TABLE", "1"},
			{database, "public", "dtp_schema_table", "TABLE", "2"},
		})
		s.Bag = true
		run(s)
	})
}
