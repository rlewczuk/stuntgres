package reference_test

import (
	"bytes"
	"context"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGProcInstalledControls(t *testing.T) {
	p := fixture.NewScenario(t)
	native := p.Backends()[0]
	defer pgProcInstalledCleanup(t, native.DB)
	pgProcInstalledSetup(t, p, native, native.DB)
	pgProcInstalledMembership(t, p, native, native.DB)
	pgProcInstalledCheck(t, p, native, native.DB, pgProcInstalledBodies())
	ct := pgProcCitextSchema + ".citext"
	pgProcExec(t, p, native, native.DB, "CREATE SCHEMA pp_inst_moved",
		"ALTER FUNCTION "+pgProcCitextSchema+".strpos("+ct+","+ct+") RENAME TO pp_inst_strpos",
		"ALTER FUNCTION "+pgProcCitextSchema+".pp_inst_strpos("+ct+","+ct+") SET SCHEMA pp_inst_moved",
		"ALTER FUNCTION "+pgProcCitextSchema+".citext_eq("+ct+","+ct+") RENAME TO pp_inst_eq",
		"ALTER FUNCTION "+pgProcCitextSchema+".pp_inst_eq("+ct+","+ct+") SET SCHEMA pp_inst_moved")
	facts := pgProcInstalledBodies()
	var moved pgProcInstalledBody
	for i := range facts {
		if facts[i].schema == pgProcCitextSchema && facts[i].name == "strpos" {
			facts[i].schema = "pp_inst_moved"
			facts[i].name = "pp_inst_strpos"
			moved = facts[i]
		}
	}
	check := func(t *testing.T, b fixture.Backend, x fixture.Executor) {
		t.Helper()
		pgProcInstalledCheck(t, p, b, x, facts)
		pgProcBoth(t, p, b, x, "SELECT l.lanname,p.prosrc,p.probin,p.prosqlbody IS NULL AS no_body FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.oid=$1::regprocedure::oid", []string{"lanname", "prosrc", "probin", "no_body"}, []string{"NAME", "TEXT", "TEXT", "BOOL"}, [][]any{{"c", "citext_eq", "$libdir/citext", true}}, "pp_inst_moved.pp_inst_eq("+ct+","+ct+")")
		pgProcBoth(t, p, b, x, "SELECT pp_inst_moved.pp_inst_strpos('AbA'::"+ct+",'ba'::"+ct+") AS position,pp_inst_moved.pp_inst_eq('A'::"+ct+",'a'::"+ct+") AS equal", []string{"position", "equal"}, []string{"INT4", "BOOL"}, [][]any{{int64(2), true}})
		e := pgProcEarthSchema + "."
		pgProcBoth(t, p, b, x, "SELECT "+e+"earth() AS radius,"+e+"latitude("+e+"ll_to_earth(0::float8,0::float8)) AS latitude", []string{"radius", "latitude"}, []string{"FLOAT8", "FLOAT8"}, [][]any{{float64(6378168), float64(0)}})
	}
	check(t, native, native.DB)
	dump := fixture.ViewDump(t, p)
	if len(dump) == 0 {
		t.Fatal("empty actual installed metadata Dump")
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			if b.PG {
				c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				defer pgProcInstalledCleanup(t, c)
				if _, err := c.ExecContext(p.Ctx, string(dump)); err != nil {
					t.Fatal("PG rejected actual renamed/moved installed Dump", err)
				}
				check(t, b, c)
				return
			}
			const probe = "SELECT count(*) AS n FROM pg_proc WHERE proname IN ('pp_inst_strpos','pp_inst_eq')"
			stmt, err := b.DB.PrepareContext(p.Ctx, probe)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: probe}
			count := func(n int64) {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(probe, []string{"n"}, []string{"INT8"}, [][]any{{n}}))
			}
			old, err := b.DB.QueryContext(p.Ctx, "SELECT prosqlbody FROM pg_proc WHERE proname='pp_inst_strpos'")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			count(2)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			count(0)
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			count(2)
			check(t, b, b.DB)
			pgProcInstalledTree(t, p, b, b.DB, old, moved)
			if again := fixture.ViewDump(t, p); !bytes.Equal(dump, again) {
				t.Fatal("installed metadata logical Dump changed")
			}
		})
	}
}
