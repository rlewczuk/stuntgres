package reference_test

import (
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDefaultACLNativeControls(t *testing.T) {
	pgDefaultACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		const creator, reader = "dacl18_control_creator", "dacl18_control_reader"
		pgDefaultACLRoles(t, p, b, creator, reader)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE "+creator+" GRANT SELECT ON TABLES TO "+reader, 0))
		creatorID := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_roles WHERE rolname=$1", creator)
		readerID := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_roles WHERE rolname=$1", reader)
		id := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_default_acl WHERE defaclrole=$1", creatorID)
		acl := pgDefaultACLText(t, creator, pgDefaultACLEntry{creatorID, creator, "arwdDxtm"}, pgDefaultACLEntry{readerID, reader, "r"})
		q := "SELECT * FROM pg_default_acl WHERE defaclrole=$1"
		expected := [][]any{{id, creatorID, int64(0), "r", acl}}
		pgDefaultACLRead(t, p, b, q, expected, creatorID)
		if b.PG {
			return
		}
		var writer pgParameterACLWriter
		err := stuntgres.Dump(p.Ctx, p.Native, &writer)
		var state interface{ SQLState() string }
		if err == nil || err.Error() != "stuntgres: dump cannot serialize default privileges" || writer.calls != 0 || errors.As(err, &state) {
			t.Fatal("ordinary prewriter Dump rejection", err, writer.calls)
		}
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE dacl18_replacement(x INT); ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO PUBLIC"))
		fixture.State(t, err, "0A000")
		pgDefaultACLRead(t, p, b, q, expected, creatorID)
		old, err := b.DB.QueryContext(p.Ctx, q, creatorID)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		saved, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.ViewHold(t, p, b, q)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgDefaultACLSelect(q, false, nil, creatorID))
		pgDefaultACLRead(t, p, b, q, nil, creatorID)
		var gotID, gotCreator, gotSchema int64
		var gotCode, gotACL string
		if !old.Next() {
			t.Fatal("Reset discarded captured row", old.Err())
		}
		if err := old.Scan(&gotID, &gotCreator, &gotSchema, &gotCode, &gotACL); err != nil || gotID != id || gotCreator != creatorID || gotSchema != 0 || gotCode != "r" || gotACL != acl {
			t.Fatal("retained Reset values", gotID, gotCreator, gotSchema, gotCode, gotACL, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained EOF", old.Err())
		}
		if err := old.Close(); err != nil {
			t.Fatal(err)
		}
		pgDefaultACLMetadata(t, saved, false)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE dacl18_pristine(x INT)")); err != nil {
			t.Fatal(err)
		}
		pgDefaultACLRead(t, p, b, "SELECT * FROM pg_default_acl", nil)
		driver := stuntgres.NewDriver()
		open := func(d *stuntgres.Driver, key string) *sql.DB {
			connector, err := d.OpenConnector(key)
			if err != nil {
				t.Fatal(err)
			}
			db := sql.OpenDB(connector)
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
			return db
		}
		seed := open(driver, "  dacl18 opaque  ")
		if _, err := seed.ExecContext(p.Ctx, "ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO PUBLIC"); err != nil {
			t.Fatal(err)
		}
		var seedID int64
		if err := seed.QueryRowContext(p.Ctx, "SELECT oid FROM pg_default_acl").Scan(&seedID); err != nil || seedID <= 0 || seedID > 4294967295 {
			t.Fatal("isolated seed identity", seedID, err)
		}
		same := open(driver, "  dacl18 opaque  ")
		var sameID int64
		if err := same.QueryRowContext(p.Ctx, "SELECT oid FROM pg_default_acl").Scan(&sameID); err != nil || sameID != seedID {
			t.Fatal("same opaque engine", sameID, seedID, err)
		}
		for _, isolated := range []*sql.DB{open(driver, "dacl18 opaque"), open(stuntgres.NewDriver(), "  dacl18 opaque  ")} {
			empty := pgDefaultACLSelect("SELECT * FROM pg_default_acl", false, nil)
			empty.NonContext = true
			fixture.RunStep(t, p.Ctx, isolated, b.Recorder, empty)
		}
	})
}
