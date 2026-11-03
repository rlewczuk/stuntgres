package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgsurgeryKillIdentity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
			t.Run("rename_move_shadow_detach", func(t *testing.T) {
				ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'original') RETURNING k,v,ctid,xmin,xmax")
					z := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(2,'sentinel') RETURNING k,v,ctid,xmin,xmax")
					var killOID, freezeOID int64
					if err := c.QueryRowContext(p.Ctx, "SELECT 'ks_part.heap_force_kill(regclass,tid[])'::regprocedure::oid::bigint,'ks_part.heap_force_freeze(regclass,tid[])'::regprocedure::oid::bigint").Scan(&killOID, &freezeOID); err != nil || killOID <= 0 || freezeOID <= 0 || killOID == freezeOID {
						t.Fatal("distinct installed identities", killOID, freezeOID, err)
					}
					st, err := c.PrepareContext(p.Ctx, killSQL)
					if err != nil {
						t.Fatal(err)
					}
					defer st.Close()
					held := fixture.HeldStatement{Executor: c, Statement: st, SQL: killSQL}
					for _, q := range []string{
						"CREATE SCHEMA ks_moved",
						"ALTER FUNCTION ks_part.heap_force_kill(regclass,tid[]) RENAME TO remove_tuple",
						"ALTER FUNCTION ks_part.remove_tuple(regclass,tid[]) SET SCHEMA ks_moved",
						"CREATE FUNCTION ks_part.heap_force_kill(regclass,tid[]) RETURNS void LANGUAGE SQL AS 'SELECT NULL::void'",
					} {
						freezeRun(t, p, b, c, fixture.Exec(q, 0))
					}
					ksIdentityOID(t, p, b, c, "ks_moved.remove_tuple", killOID)
					shadow := fixture.Select(killSQL, []string{"killed"}, []string{"VOID"}, [][]any{{nil}}, "ks_part.heap", freezeTIDs(a.tid))
					freezeBoth(t, p, b, c, shadow)
					// PostgreSQL reparses prepared SQL after name DDL. Its current
					// ordinary SQL shadow must not inherit the old C capability.
					fixture.RunStep(t, p.Ctx, held, b.Recorder, shadow)
					ksRows(t, p, b, c, a, z)
					q := "SELECT ks_moved.remove_tuple($1::regclass,$2::tid[]) AS killed"
					s := ksVoid(q, "ks_part.heap", freezeTIDs(a.tid))
					s.Prepared = prepared
					freezeRun(t, p, b, c, s)
					ksRows(t, p, b, c, z)
					for _, ddl := range []string{"ALTER FUNCTION ks_moved.remove_tuple(regclass,tid[]) SET SCHEMA ks_part", "ALTER EXTENSION pg_surgery SET SCHEMA ks_moved"} {
						freezeRun(t, p, b, c, fixture.Exec(ddl, 0))
					}
					ksIdentityOID(t, p, b, c, "ks_moved.remove_tuple", killOID)
					ksIdentityOID(t, p, b, c, "ks_moved.heap_force_freeze", freezeOID)
					freezeBoth(t, p, b, c, fixture.Select("SELECT p.proname::text AS name,p.prosrc AS source FROM pg_depend d JOIN pg_proc p ON p.oid=d.objid WHERE d.classid='pg_proc'::regclass AND d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='pg_surgery') ORDER BY p.proname", []string{"name", "source"}, []string{"TEXT", "TEXT"}, [][]any{{"heap_force_freeze", "heap_force_freeze"}, {"remove_tuple", "heap_force_kill"}}))
					freezeBoth(t, p, b, c, ksVoid("SELECT ks_moved.heap_force_freeze($1::regclass,$2::tid[]) AS killed", "ks_part.heap", freezeTIDs(z.tid)))
					z = freezeRepaired(z)
					ksRows(t, p, b, c, z)
					freezeRun(t, p, b, c, fixture.Exec("ALTER EXTENSION pg_surgery DROP FUNCTION ks_moved.remove_tuple(regclass,tid[])", 0))
					freezeRun(t, p, b, c, fixture.Exec("DROP EXTENSION pg_surgery", 0))
					ksIdentityOID(t, p, b, c, "ks_moved.remove_tuple", killOID)
					// Detaching a genuine member does not replace its authentic C
					// declaration with a name-only function (existing freeze precedent).
					d := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(3,'detached') RETURNING k,v,ctid,xmin,xmax")
					s = ksVoid(q, "ks_part.heap", freezeTIDs(d.tid))
					s.Prepared = prepared
					freezeRun(t, p, b, c, s)
					ksRows(t, p, b, c, z)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(killSQL, []string{"killed"}, []string{"VOID"}, [][]any{{nil}}, "ks_part.heap", freezeTIDs(z.tid)))
					ksRows(t, p, b, c, z)
				})
			})
			t.Run("cross_member_names", func(t *testing.T) {
				ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'symbol-not-name') RETURNING k,v,ctid,xmin,xmax")
					for _, q := range []string{
						"ALTER FUNCTION ks_part.heap_force_kill(regclass,tid[]) RENAME TO remove_tuple",
						"ALTER FUNCTION ks_part.heap_force_freeze(regclass,tid[]) RENAME TO heap_force_kill",
					} {
						freezeRun(t, p, b, c, fixture.Exec(q, 0))
					}
					s := ksVoid(killSQL, "ks_part.heap", freezeTIDs(a.tid))
					s.Prepared = prepared
					freezeRun(t, p, b, c, s)
					ksRows(t, p, b, c, freezeRepaired(a))
					s = ksVoid("SELECT ks_part.remove_tuple($1::regclass,$2::tid[]) AS killed", "ks_part.heap", freezeTIDs(a.tid))
					s.Prepared = prepared
					freezeRun(t, p, b, c, s)
					ksRows(t, p, b, c)
				})
			})
		})
	}
}

func ksIdentityOID(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string, oid int64) {
	t.Helper()
	freezeBoth(t, p, b, c, fixture.Select("SELECT '"+name+"(regclass,tid[])'::regprocedure::oid::bigint AS identity", []string{"identity"}, []string{"INT8"}, [][]any{{oid}}))
}
