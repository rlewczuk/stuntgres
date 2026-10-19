package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDefaultACLLifecycle(t *testing.T) {
	pgDefaultACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		const creator, reader, second = "dacl18_life_creator", "dacl18_life_reader", "dacl18_life_second"
		pgDefaultACLRoles(t, p, b, creator, reader, second)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		creatorID := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_roles WHERE rolname=$1", creator)
		readerID := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_roles WHERE rolname=$1", reader)
		secondID := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_roles WHERE rolname=$1", second)
		q := "SELECT * FROM pg_default_acl WHERE defaclrole=$1"
		held := fixture.ViewHold(t, p, b, q)
		check := func(rows [][]any) {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgDefaultACLSelect(q, b.PG, rows, creatorID))
			pgDefaultACLRead(t, p, b, q, rows, creatorID)
		}
		check(nil)
		prefix := "ALTER DEFAULT PRIVILEGES FOR ROLE " + creator + " "
		grant := prefix + "GRANT SELECT ON TABLES TO " + reader
		run(fixture.Exec(grant, 0))
		id := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_default_acl WHERE defaclrole=$1", creatorID)
		acl := pgDefaultACLText(t, creator, pgDefaultACLEntry{creatorID, creator, "arwdDxtm"}, pgDefaultACLEntry{readerID, reader, "r"})
		expected := [][]any{{id, creatorID, int64(0), "r", acl}}
		check(expected)
		old, err := b.DB.QueryContext(p.Ctx, q, creatorID)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		saved, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec(grant, 0))
		check(expected)
		run(fixture.Exec(prefix+"GRANT UPDATE ON TABLES TO "+reader, 0))
		nextACL := pgDefaultACLText(t, creator, pgDefaultACLEntry{creatorID, creator, "arwdDxtm"}, pgDefaultACLEntry{readerID, reader, "rw"})
		check([][]any{{id, creatorID, int64(0), "r", nextACL}})
		fail := fixture.ImplicitError("ALTER DEFAULT PRIVILEGES FOR ROLE "+creator+",dacl18_missing_creator GRANT SELECT ON TABLES TO "+reader, fixture.Diagnostic{Code: "42704", Message: `role "dacl18_missing_creator" does not exist`})
		fail.ErrorStage = "execute"
		run(fail)
		check([][]any{{id, creatorID, int64(0), "r", nextACL}})
		fail = fixture.ImplicitError(prefix+"IN SCHEMA public,dacl18_missing_schema GRANT USAGE ON SEQUENCES TO "+reader, fixture.Diagnostic{Code: "3F000", Message: `schema "dacl18_missing_schema" does not exist`})
		fail.ErrorStage = "execute"
		run(fail)
		check([][]any{{id, creatorID, int64(0), "r", nextACL}})
		var gotID, gotCreator, gotSchema int64
		var gotCode, gotACL string
		if !old.Next() {
			t.Fatal("retained row", old.Err())
		}
		if err := old.Scan(&gotID, &gotCreator, &gotSchema, &gotCode, &gotACL); err != nil || gotID != id || gotCreator != creatorID || gotSchema != 0 || gotCode != "r" || gotACL != acl {
			t.Fatal("retained values", gotID, gotCreator, gotSchema, gotCode, gotACL, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained EOF", old.Err())
		}
		if err := old.Close(); err != nil {
			t.Fatal(err)
		}
		pgDefaultACLMetadata(t, saved, b.PG)
		run(fixture.Exec(prefix+"REVOKE ALL ON TABLES FROM "+reader, 0))
		check(nil)
		run(fixture.Exec(grant, 0))
		fresh := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_default_acl WHERE defaclrole=$1", creatorID)
		if fresh == id {
			t.Fatal("recreated default reused identity", id)
		}
		check([][]any{{fresh, creatorID, int64(0), "r", acl}})
		run(fixture.Exec(prefix+"GRANT UPDATE ON TABLES TO "+second, 0))
		run(fixture.Exec("DROP OWNED BY "+reader, 0))
		check([][]any{{fresh, creatorID, int64(0), "r", pgDefaultACLText(t, creator, pgDefaultACLEntry{creatorID, creator, "arwdDxtm"}, pgDefaultACLEntry{secondID, second, "w"})}})
		run(fixture.Exec("DROP OWNED BY "+second, 0))
		check(nil)
		run(fixture.Exec(grant, 0))
		run(fixture.Exec("DROP OWNED BY "+creator, 0))
		check(nil)
	})
}
