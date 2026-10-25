package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseCreateMetadata(t *testing.T) {
	databaseCreateScenario(t, func(c *databaseCreateCase) {
		t := c.t
		owner := c.scalar("SELECT current_user")
		var previous int64
		for i, name := range []string{"dc_metadata", "DC quoted é"} {
			c.create(name, "", i == 1)
			query := "SELECT d.datname,r.rolname,d.encoding,d.datlocprovider,d.datcollate,d.datctype,d.datistemplate,d.datallowconn,d.datconnlimit,d.datacl FROM pg_catalog.pg_database d JOIN pg_catalog.pg_roles r ON r.oid=d.datdba WHERE d.datname=$1"
			s := fixture.Select(query, []string{"datname", "rolname", "encoding", "datlocprovider", "datcollate", "datctype", "datistemplate", "datallowconn", "datconnlimit", "datacl"}, []string{"NAME", "NAME", "INT4", "CHAR", "TEXT", "TEXT", "BOOL", "BOOL", "INT4", "_ACLITEM"}, [][]any{{name, owner, int64(6), "c", "C", "C", false, true, int64(-1), nil}}, name)
			c.run(s)
			var oid int64
			if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_database WHERE datname=$1", name).Scan(&oid); err != nil {
				t.Fatal(err)
			}
			if oid <= 0 || oid == previous {
				t.Fatalf("database identities %d, %d", previous, oid)
			}
			previous = oid
			db, _, recorder := c.pool(name, "")
			fixture.RunStep(t, c.p.Ctx, db, recorder, fixture.Prepared(s))
			fixture.RunStep(t, c.p.Ctx, db, recorder, fixture.Select("SELECT current_database(),current_catalog", []string{"current_database", "current_catalog"}, []string{"NAME", "NAME"}, [][]any{{name, name}}))
			var selectedOID int64
			if err := db.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&selectedOID); err != nil || selectedOID != oid {
				t.Fatalf("selected identity %d, want %d: %v", selectedOID, oid, err)
			}
		}
		q := "SELECT datname FROM pg_database WHERE datname IN ('dc_metadata','dc_metadata_late') ORDER BY datname"
		heldQuery, err := c.conn.PrepareContext(c.p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer heldQuery.Close()
		held := fixture.HeldStatement{Executor: c.conn, Statement: heldQuery, SQL: q}
		fixture.RunStep(t, c.p.Ctx, held, c.backend.Recorder, fixture.Select(q, []string{"datname"}, []string{"NAME"}, [][]any{{"dc_metadata"}}))
		old, err := c.backend.DB.QueryContext(c.p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		c.create("dc_metadata_late", "", false)
		if !old.Next() {
			t.Fatal("old registry result lost", old.Err())
		}
		var oldName string
		if err := old.Scan(&oldName); err != nil || oldName != "dc_metadata" {
			t.Fatalf("old registry row %q: %v", oldName, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("old registry result changed", old.Err())
		}
		fixture.RunStep(t, c.p.Ctx, held, c.backend.Recorder, fixture.Select(q, []string{"datname"}, []string{"NAME"}, [][]any{{"dc_metadata"}, {"dc_metadata_late"}}))
	})
}

func TestReferenceDatabaseCreateOptions(t *testing.T) {
	databaseCreateScenario(t, func(c *databaseCreateCase) {
		owner := c.scalar("SELECT current_user")
		for i, options := range []string{
			"WITH OWNER DEFAULT TEMPLATE DEFAULT ENCODING DEFAULT LC_COLLATE DEFAULT LC_CTYPE DEFAULT",
			"ENCODING = 6 TEMPLATE = template1 OWNER = '" + owner + "' LC_CTYPE = 'C' LC_COLLATE = 'C'",
			"WITH LC_COLLATE 'C' OWNER \"" + owner + "\" ENCODING 'UTF8' LC_CTYPE 'C' TEMPLATE 'template1'",
			"ENCODING 'UTF-8'",
			"LC_COLLATE 'POSIX'",
		} {
			name := fmt.Sprintf("dc_options_%d", i)
			c.create(name, options, i%2 == 1)
			c.run(fixture.Select("SELECT datname,encoding,datcollate,datctype FROM pg_database WHERE datname=$1", []string{"datname", "encoding", "datcollate", "datctype"}, []string{"NAME", "INT4", "TEXT", "TEXT"}, [][]any{{name, int64(6), "C", "C"}}, name))
		}
		query := "CREATE DATABASE dc_suffix ENCODING 'UTF8' garbage"
		c.remember("dc_suffix")
		d := databaseCreateDiagnostic("42601", "syntax error at end of input")
		d.Position = fixture.Position(len([]rune(query)) + 1)
		c.run(fixture.ErrorDiagnostic(query, d))
		c.absent("dc_suffix")
		for i, option := range []string{"TEMPLATE template0", "LOCALE_PROVIDER libc", "STRATEGY WAL_LOG"} {
			name := fmt.Sprintf("dc_boundary_%d", i)
			c.remember(name)
			if c.backend.PG {
				c.create(name, option, false)
			} else {
				messages := []string{
					"CREATE DATABASE with TEMPLATE other than template1 is not supported",
					"CREATE DATABASE with LOCALE_PROVIDER is not supported",
					"CREATE DATABASE with STRATEGY is not supported",
				}
				c.error("CREATE DATABASE "+name+" "+option, "0A000", messages[i])
				c.absent(name)
			}
		}
		// Native capability boundary only: PostgreSQL locale spellings and
		// availability depend on the oracle environment, not this native contract.
		if !c.backend.PG {
			for _, option := range []string{"LC_COLLATE", "LC_CTYPE"} {
				name := "dc_locale_" + strings.ToLower(option)
				c.remember(name)
				c.error("CREATE DATABASE "+name+" "+option+" 'C.UTF-8'", "0A000", "CREATE DATABASE with "+option+" other than C is not supported")
				c.absent(name)
			}
		}
	})
}

func TestReferenceDatabaseCreatePrepared(t *testing.T) {
	databaseCreateScenario(t, func(c *databaseCreateCase) {
		t := c.t
		name := "dc_prepared"
		c.remember(name)
		query := "CREATE DATABASE dc_prepared OWNER dc_actor"
		stmt, err := c.conn.PrepareContext(c.p.Ctx, query)
		if err != nil {
			t.Fatal("quiet utility Prepare:", err)
		}
		defer stmt.Close()
		c.absent(name)
		held := fixture.HeldStatement{Executor: c.conn, Statement: stmt, SQL: query}
		fixture.RunStep(t, c.p.Ctx, held, c.backend.Recorder, fixture.ErrorDiagnostic(query, databaseCreateDiagnostic("42704", "role \"dc_actor\" does not exist")))
		c.absent(name)
		c.run(fixture.Exec("CREATE ROLE dc_actor CREATEDB", 0))
		c.run(fixture.Exec("SET ROLE dc_actor", 0))
		fixture.RunStep(t, c.p.Ctx, held, c.backend.Recorder, fixture.Exec(query, 0))
		fixture.RunStep(t, c.p.Ctx, held, c.backend.Recorder, fixture.ErrorDiagnostic(query, databaseCreateDiagnostic("42P04", "database \"dc_prepared\" already exists")))
		c.run(fixture.Exec("RESET ROLE", 0))
		c.run(fixture.Select("SELECT datname FROM pg_database WHERE datname=$1", []string{"datname"}, []string{"NAME"}, [][]any{{name}}, name))
		// Permissions must be checked at each execution, not captured by Prepare.
		c.run(fixture.Exec("ALTER ROLE dc_actor NOCREATEDB", 0))
		c.run(fixture.Exec("SET ROLE dc_actor", 0))
		fixture.RunStep(t, c.p.Ctx, held, c.backend.Recorder, fixture.ErrorDiagnostic(query, databaseCreateDiagnostic("42501", "permission denied to create database")))
		c.run(fixture.Exec("RESET ROLE", 0))
		c.run(fixture.Select("SELECT 42 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(42)}}))
	})
}
