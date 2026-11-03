package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgsurgeryKillEffects(t *testing.T) {
	for _, action := range []string{"CASCADE", "SET NULL"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared_%t", action, prepared), func(t *testing.T) {
				ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					for _, q := range []string{
						"ALTER TABLE ks_part.heap ADD PRIMARY KEY(k)",
						"ALTER TABLE ks_part.heap ALTER COLUMN v SET NOT NULL",
						"ALTER TABLE ks_part.heap ADD CONSTRAINT ks_check CHECK(v<>'forbidden')",
						"CREATE TABLE ks_part.child(id integer PRIMARY KEY,p integer,CONSTRAINT ks_fk FOREIGN KEY(p) REFERENCES ks_part.heap(k) ON DELETE " + action + ")",
					} {
						freezeRun(t, p, b, c, fixture.Exec(q, 0))
					}
					ksAuditSetup(t, p, b, c)
					a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'removed') RETURNING k,v,ctid,xmin,xmax")
					z := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(2,'kept') RETURNING k,v,ctid,xmin,xmax")
					freezeRun(t, p, b, c, fixture.Exec("INSERT INTO ks_part.heap VALUES(3,'delete-control')", 1))
					freezeRun(t, p, b, c, fixture.Exec("BEGIN", 0))
					childOwner := freezeAssignedOwner(t, p, b, c)
					freezeRun(t, p, b, c, fixture.Exec("INSERT INTO ks_part.child VALUES(1,1),(2,2),(3,3)", 3))
					freezeRun(t, p, b, c, fixture.Exec("COMMIT", 0))
					a.xmax, z.xmax = childOwner.xid, childOwner.xid
					control := fixture.Exec("DELETE FROM ks_part.heap WHERE k=3", 1)
					control.Prepared = prepared
					freezeRun(t, p, b, c, control)
					freezeBoth(t, p, b, c, fixture.Select("SELECT k FROM ks_part.audit ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(3)}}))
					children := [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}
					if action == "SET NULL" {
						children = append(children, []any{int64(3), nil})
					}
					freezeBoth(t, p, b, c, fixture.Select("SELECT id,p FROM ks_part.child ORDER BY id", []string{"id", "p"}, []string{"INT4", "INT4"}, children))
					ksRows(t, p, b, c, a, z)
					s := ksVoid(killSQL, "ks_part.heap", freezeTIDs(a.tid))
					s.Prepared = prepared
					freezeRun(t, p, b, c, s)
					ksRows(t, p, b, c, z)
					// Unlike the ordinary DELETE control, surgery fires neither the
					// genuine user DELETE trigger nor the FK's genuine delete action.
					freezeBoth(t, p, b, c, fixture.Select("SELECT k FROM ks_part.audit ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(3)}}))
					freezeBoth(t, p, b, c, fixture.Select("SELECT id,p FROM ks_part.child ORDER BY id", []string{"id", "p"}, []string{"INT4", "INT4"}, children))
					freezeBoth(t, p, b, c, fixture.Select("SELECT convalidated,conenforced FROM pg_constraint WHERE conrelid='ks_part.child'::regclass AND conname='ks_fk'", []string{"convalidated", "conenforced"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
					freezeBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_trigger WHERE tgconstraint=(SELECT oid FROM pg_constraint WHERE conrelid='ks_part.child'::regclass AND conname='ks_fk') AND tgenabled='O'", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
					freezeBoth(t, p, b, c, fixture.Select("SELECT attnotnull FROM pg_attribute WHERE attrelid='ks_part.heap'::regclass AND attname='v'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{true}}))
					freezeBoth(t, p, b, c, fixture.Select("SELECT convalidated,conenforced FROM pg_constraint WHERE conrelid='ks_part.heap'::regclass AND conname='ks_check'", []string{"convalidated", "conenforced"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
					fk := fixture.ControlsReferenceDiagnostic("23503", "insert or update on table \"child\" violates foreign key constraint \"ks_fk\"")
					fk.Detail = "Key (p)=(1) is not present in table \"heap\"."
					fk.Schema, fk.Table, fk.Constraint = "ks_part", "child", "ks_fk"
					freezeBoth(t, p, b, c, fixture.ImplicitError("INSERT INTO ks_part.child VALUES(4,1)", fk))
					notnull := fixture.ControlsReferenceDiagnostic("23502", "null value in column \"v\" of relation \"heap\" violates not-null constraint")
					notnull.Detail, notnull.Schema, notnull.Table, notnull.Column = "Failing row contains (4, null).", "ks_part", "heap", "v"
					freezeBoth(t, p, b, c, fixture.ImplicitError("INSERT INTO ks_part.heap VALUES(4,NULL)", notnull))
					check := fixture.ControlsReferenceDiagnostic("23514", "new row for relation \"heap\" violates check constraint \"ks_check\"")
					check.Detail, check.Schema, check.Table, check.Constraint = "Failing row contains (4, forbidden).", "ks_part", "heap", "ks_check"
					freezeBoth(t, p, b, c, fixture.ImplicitError("INSERT INTO ks_part.heap VALUES(4,'forbidden')", check))
					ksRows(t, p, b, c, z)
					freezeBoth(t, p, b, c, fixture.Select("SELECT id,p FROM ks_part.child ORDER BY id", []string{"id", "p"}, []string{"INT4", "INT4"}, children))
				})
			})
		}
	}
}
