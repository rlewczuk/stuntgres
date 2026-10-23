package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTypeLifecycleAtomicity(t *testing.T) {
	t.Run("failure_atomicity", func(t *testing.T) {
		typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			domainIdentityDDL(t, p, c, r, "CREATE SCHEMA tl_src", "CREATE SCHEMA tl_dst", "CREATE TYPE tl_src.mood AS ENUM ('z','a')", "CREATE DOMAIN tl_src.free AS integer", "CREATE TYPE tl_dst._mood AS ENUM ()", "CREATE TABLE tl_src.holder(v tl_src.mood,keep integer)")
			run(fixture.Exec("INSERT INTO tl_src.holder VALUES('z',11),(NULL,12)", 2))
			oid := domainAlterOID(t, p, c, "SELECT 'tl_src.mood'::regtype::oid")
			array := domainAlterOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tl_src.mood'::regtype::oid")
			free := domainAlterOID(t, p, c, "SELECT 'tl_src.free'::regtype::oid")
			qread := "SELECT v::text AS v,keep FROM tl_src.holder ORDER BY keep"
			consumer := typeLifecycleHeld(t, p, c, qread)
			check := func() {
				domainIdentitySameOID(t, p, c, "SELECT 'tl_src.mood'::regtype::oid", oid)
				domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tl_src.mood'::regtype::oid", array)
				domainIdentitySameOID(t, p, c, "SELECT 'tl_src.free'::regtype::oid", free)
				fixture.RunStep(t, p.Ctx, consumer, r, fixture.Select(qread, []string{"v", "keep"}, []string{"TEXT", "INT4"}, [][]any{{"z", int64(11)}, {nil, int64(12)}}))
			}
			typeLifecycleFailure(t, p, c, "ALTER TYPE tl_src.mood SET SCHEMA tl_dst", fixture.ControlsReferenceDiagnostic("42710", `type "_mood" already exists in schema "tl_dst"`))
			check()
			typeLifecycleFailure(t, p, c, "DROP TYPE tl_src.free,tl_src.absent CASCADE", fixture.ControlsReferenceDiagnostic("42704", `type "tl_src.absent" does not exist`))
			check()
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				for _, q := range []string{"ALTER TYPE tl_src.mood RENAME TO canceled", "ALTER TYPE tl_src.mood SET SCHEMA tl_dst", "ALTER TYPE tl_src.mood OWNER TO CURRENT_USER", "DROP TYPE tl_src.free,tl_src.mood CASCADE"} {
					result, err := c.ExecContext(ctx, q)
					if !errors.Is(err, context.Canceled) || result != nil {
						t.Fatalf("precanceled %s: result=%v err=%v", q, result, err)
					}
					check()
				}
			}
			writer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			reader, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			waiterPID, holderPID := 0, 0
			var relation int64
			if b.PG {
				waiterPID, holderPID = fixture.RowPIDs(t, p.Ctx, c, writer)
				relation = fixture.LockOID(t, p.Ctx, "tl_src.holder")
			}
			tx, err := writer.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, r, fixture.Exec("INSERT INTO tl_src.holder VALUES('a',13)", 1))
			q := "DROP TYPE tl_src.free,tl_src.mood CASCADE"
			*notices = nil
			fixture.BinaryConversionActiveCancelAfter(t, p, b, c, q, "(*relationRequest).wait", func(ctx context.Context) {
				if b.PG {
					fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, relation, "AccessExclusiveLock")
				}
				fixture.RunStep(t, ctx, reader, b.Recorder, fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='tl_src'::regnamespace::oid AND typname IN ('mood','_mood','free','_free') ORDER BY typname", []string{"typname"}, []string{"NAME"}, [][]any{{"_free"}, {"_mood"}, {"free"}, {"mood"}}))
			}, func(ctx context.Context) fixture.RelationOutcome { return fixture.RowExecute(ctx, c, q, false) })
			check()
			domainIdentityNotices(t, notices)
			domainIdentityDDL(t, p, c, r, "SET lock_timeout='100ms'")
			run(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")))
			domainIdentityNotices(t, notices)
			domainIdentityDDL(t, p, c, r, "SET lock_timeout=0")
			check()
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			domainIdentityDDL(t, p, c, r, "ALTER TYPE tl_src.mood RENAME TO renamed", "ALTER TYPE tl_src.renamed SET SCHEMA tl_dst")
			domainIdentitySameOID(t, p, c, "SELECT 'tl_dst.renamed'::regtype::oid", oid)
		})
	})
	t.Run("maintenance_identity", func(t *testing.T) {
		typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			domainIdentityDDL(t, p, c, r, "CREATE SCHEMA tl_src", "CREATE SCHEMA tl_dst",
				"CREATE TYPE tl_src.maintenance_mood AS ENUM ('a','z')",
				"CREATE TABLE tl_src.maintenance_holder(id integer,v tl_src.maintenance_mood)")
			insertQ := "INSERT INTO tl_src.maintenance_holder VALUES($1,$2)"
			insert := typeLifecycleHeld(t, p, c, insertQ)
			readQ := "SELECT id,v::text AS v FROM tl_src.maintenance_holder ORDER BY id"
			read := typeLifecycleHeld(t, p, c, readQ)
			for _, row := range []struct {
				id    int64
				value any
			}{{1, "a"}, {2, nil}, {3, "z"}} {
				fixture.RunStep(t, p.Ctx, insert, r, fixture.Exec(insertQ, 1, row.id, row.value))
			}
			run(fixture.Exec("DELETE FROM tl_src.maintenance_holder WHERE id=3", 1))
			oid := domainAlterOID(t, p, c, "SELECT 'tl_src.maintenance_mood'::regtype::oid")
			array, _, _ := typeLifecycleCompanion(t, p, c, "tl_src.maintenance_mood")
			relation := domainAlterOID(t, p, c, "SELECT 'tl_src.maintenance_holder'::regclass::oid")
			check := func() {
				t.Helper()
				fixture.RunStep(t, p.Ctx, read, r, fixture.Select(readQ, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), nil}}))
				domainIdentitySameOID(t, p, c, "SELECT 'tl_src.maintenance_holder'::regclass::oid", relation)
			}
			check()
			run(fixture.Exec("VACUUM tl_src.maintenance_holder", 0))
			check()
			domainIdentitySameOID(t, p, c, "SELECT 'tl_src.maintenance_mood'::regtype::oid", oid)
			domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tl_src.maintenance_mood'::regtype::oid", array)
			domainIdentityDDL(t, p, c, r, "ALTER TYPE tl_src.maintenance_mood RENAME TO maintenance_renamed",
				"ALTER TYPE tl_src.maintenance_renamed SET SCHEMA tl_dst")
			check()
			domainIdentitySameOID(t, p, c, "SELECT 'tl_dst.maintenance_renamed'::regtype::oid", oid)
			domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tl_dst.maintenance_renamed'::regtype::oid", array)
			oidQueryArgument(t, p, c, array, oid)
			run(fixture.Select("SELECT t.typname,n.nspname FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE t.oid=$1::oid OR t.oid=$2::oid ORDER BY t.typname", []string{"typname", "nspname"}, []string{"NAME", "NAME"}, [][]any{{"_maintenance_renamed", "tl_dst"}, {"maintenance_renamed", "tl_dst"}}, oid, array))
			run(fixture.Select("SELECT atttypid=$1::oid AS same_type FROM pg_attribute WHERE attrelid=$2::oid AND attname='v'", []string{"same_type"}, []string{"BOOL"}, [][]any{{true}}, oid, relation))
			typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_type WHERE typnamespace='tl_src'::regnamespace::oid AND typname IN ('maintenance_mood','_maintenance_mood','maintenance_renamed','_maintenance_renamed')", 0)
			fixture.RunStep(t, p.Ctx, insert, r, fixture.Exec(insertQ, 1, int64(4), "z"))
			fixture.RunStep(t, p.Ctx, read, r, fixture.Select(readQ, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), nil}, {int64(4), "z"}}))
			domainIdentityNotices(t, notices)
		})
	})
}
