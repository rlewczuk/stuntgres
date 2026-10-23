package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoCharacterSetsCollationSelection(t *testing.T) {
	csScenario(t, false, func(c *csCase) {
		held := c.hold(csQuery)
		c.check(held, "pg_catalog", "C")
		c.exec(`CREATE COLLATION public.cs_nonmatching_posix_very_long_name FROM pg_catalog."POSIX"`)
		c.check(held, "pg_catalog", "C")
		c.exec(`CREATE COLLATION public.cs_rank_bb FROM pg_catalog."C"`)
		c.check(held, "public", "cs_rank_bb")
		c.exec(`CREATE COLLATION public.cs_rank_aa FROM pg_catalog."C"`)
		c.check(held, "public", "cs_rank_aa")
		c.exec(`CREATE COLLATION public.cs_rank_strictly_longer FROM pg_catalog."C"`)
		c.check(held, "public", "cs_rank_strictly_longer")
		c.exec("DROP COLLATION public.cs_rank_strictly_longer", "DROP COLLATION public.cs_rank_aa", "DROP COLLATION public.cs_rank_bb")
		c.check(held, "pg_catalog", "C")
		// Eight characters/thirteen bytes loses to ten characters/ten bytes.
		c.exec(`CREATE COLLATION public."cs_ééééé" FROM pg_catalog."C"`, `CREATE COLLATION public.cs_aaaaaaa FROM pg_catalog."C"`)
		c.check(held, "public", "cs_aaaaaaa")
		c.exec("DROP COLLATION public.cs_aaaaaaa")
		c.check(held, "public", "cs_ééééé")
		c.exec(`DROP COLLATION public."cs_ééééé"`, "DROP COLLATION public.cs_nonmatching_posix_very_long_name")
		c.check(held, "pg_catalog", "C")
	})
}

func TestReferenceInfoCharacterSetsLifecycle(t *testing.T) {
	csScenario(t, false, func(c *csCase) {
		held := c.hold(csQuery)
		c.check(held, "pg_catalog", "C")
		c.exec("CREATE SCHEMA cs_lifecycle", `CREATE COLLATION public.cs_selected_long_name FROM pg_catalog."C"`)
		c.check(held, "public", "cs_selected_long_name")
		var retained *sql.Rows
		var columns []*sql.ColumnType
		// Keep the retained native cursor on a separate physical handle so the
		// same held SELECT can observe every DDL transition without blocking.
		observer, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer observer.Close()
		if !c.b.PG {
			var err error
			retained, err = observer.QueryContext(c.p.Ctx, csQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			columns, err = retained.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			csMetadata(t, columns, false)
		}
		for _, q := range []string{"ALTER COLLATION public.cs_selected_long_name RENAME TO cs_renamed_long_name", "ALTER COLLATION public.cs_renamed_long_name SET SCHEMA cs_lifecycle", "DROP COLLATION cs_lifecycle.cs_renamed_long_name"} {
			c.exec(q)
			var schema, name string
			switch q {
			case "ALTER COLLATION public.cs_selected_long_name RENAME TO cs_renamed_long_name":
				schema, name = "public", "cs_renamed_long_name"
			case "ALTER COLLATION public.cs_renamed_long_name SET SCHEMA cs_lifecycle":
				schema, name = "cs_lifecycle", "cs_renamed_long_name"
			default:
				schema, name = "pg_catalog", "C"
			}
			c.check(held, schema, name)
		}
		if retained != nil {
			if !retained.Next() {
				t.Fatal("retained singleton missing", retained.Err())
			}
			values, dest := make([]any, 8), make([]any, 8)
			for i := range values {
				dest[i] = &values[i]
			}
			if err := retained.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(values, c.rows("public", "cs_selected_long_name")[0]) || retained.Next() || retained.Err() != nil {
				t.Fatal("retained row changed", values, retained.Err())
			}
			csMetadata(t, columns, false)
			if err := retained.Close(); err != nil {
				t.Fatal(err)
			}
			csMetadata(t, columns, false)
		}
		c.check(held, "pg_catalog", "C")
		c.exec("BEGIN")
		create := `CREATE COLLATION public.cs_rollback_selected_long_name FROM pg_catalog."C"`
		if c.b.PG {
			c.exec(create)
			c.check(held, "public", "cs_rollback_selected_long_name")
		} else {
			// Native collation utilities retain their existing explicit-
			// transaction boundary; this view does not expand DDL support.
			s := fixture.ErrorDiagnostic(create, fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction"))
			s.ErrorStage = "execute"
			c.run(c.conn, s)
			s = fixture.ErrorDiagnostic(csQuery, fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
			s.SelectRows, s.ErrorStage = true, "execute"
			c.run(held, s)
		}
		c.exec("ROLLBACK")
		c.check(held, "pg_catalog", "C")
		c.run(c.conn, fixture.Select("SELECT collname FROM pg_catalog.pg_collation WHERE collname='cs_rollback_selected_long_name'", []string{"collname"}, []string{"NAME"}, nil))
		c.exec("DROP SCHEMA cs_lifecycle")
	})
}
