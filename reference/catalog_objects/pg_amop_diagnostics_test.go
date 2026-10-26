package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

// Independently specified diagnostics; the pinned oracle must confirm all
// fields and stages, rather than deriving expectations from native errors.
func pgAmopDiagnosticFailures(t *testing.T,p *fixture.Pair,b fixture.Backend) {
	t.Helper()
	for _, tc := range []struct{ query,code,message,position string }{
		{"SELECT missing FROM pg_catalog.pg_amop","42703",`column "missing" does not exist`,"8"},
		{"SELECT /*é*/ missing FROM pg_catalog.pg_amop","42703",`column "missing" does not exist`,"14"},
		{"SELECT 'x'::oid FROM pg_catalog.pg_amop WHERE FALSE","22P02",`invalid input syntax for type oid: "x"`,"8"},
		{"SELECT '4294967296'::oid FROM pg_catalog.pg_amop WHERE FALSE","22003",`value "4294967296" is out of range for type oid`,"8"},
	} {
		for _, prepared := range []bool{false,true} {
			s := fixture.ImplicitError(tc.query,fixture.Diagnostic{Code:tc.code,Message:tc.message,Position:tc.position})
			s.SelectRows,s.Prepared,s.ErrorStage = true,prepared,"execute"; if prepared { s.ErrorStage = "prepare" }
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
		}
	}
	stmt, err := b.DB.PrepareContext(p.Ctx,pgAmopProbe); if err != nil { t.Fatal(err) }; defer stmt.Close()
	held := fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:pgAmopProbe}
	pgAmopRows(t,p,b,held,pgAmopProbe,pgAmopFacts()[1:2],false,int64(10015))
	for _, tc := range []struct{ value,code,message string }{
		{"x","22P02",`invalid input syntax for type oid: "x"`},
		{"4294967296","22003",`value "4294967296" is out of range for type oid`},
	} {
		for _, route := range []struct{ e fixture.Executor; prepared bool }{{b.DB,false},{b.DB,true},{held,false}} {
			s := fixture.ImplicitError(pgAmopProbe,fixture.Diagnostic{Code:tc.code,Message:tc.message,Context:"unnamed portal parameter $1 = '...'"},tc.value)
			s.SelectRows,s.Prepared,s.ErrorStage = true,route.prepared,"execute"
			fixture.RunStep(t,p.Ctx,route.e,b.Recorder,s)
			pgAmopRows(t,p,b,held,pgAmopProbe,pgAmopFacts()[2:3],false,int64(10017))
		}
	}
	pgAmopRows(t,p,b,b.DB,pgAmopWitnessQuery,pgAmopFacts(),false)
}

func TestReferencePGAmopDiagnostics(t *testing.T) { pgOpclassScenario(t,pgAmopDiagnosticFailures) }

func TestReferencePGAmopPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"amop_reader"})
	pgOpclassScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		c := fixture.SequenceCoreConn(t,p.Ctx,b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
		run(fixture.Exec("CREATE ROLE amop_reader",0))
		run(fixture.Exec("CREATE TABLE amop_sentinel(v INT)",0))
		run(fixture.Exec("INSERT INTO amop_sentinel VALUES(7)",1))
		mutations := []string{
			"INSERT INTO pg_catalog.pg_amop SELECT * FROM pg_catalog.pg_amop WHERE oid=10015",
			"INSERT INTO pg_catalog.pg_amop SELECT * FROM pg_catalog.pg_amop WHERE FALSE",
			"UPDATE pg_catalog.pg_amop SET amopstrategy=1 WHERE oid=10015",
			"UPDATE pg_catalog.pg_amop SET amopstrategy=1 WHERE FALSE",
			"DELETE FROM pg_catalog.pg_amop WHERE oid=10015",
			"DELETE FROM pg_catalog.pg_amop WHERE FALSE",
		}
		// Plan under the owner, but execute on PostgreSQL only after SET ROLE.
		// Native execution uses the existing effect-free catalog write guard.
		commands := make([]fixture.HeldStatement,len(mutations))
		for i,q := range mutations {
			stmt, err := c.PrepareContext(p.Ctx,q); if err != nil { t.Fatal("owner prepare",q,err) }; defer stmt.Close()
			commands[i] = fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
		}
		reader, err := c.PrepareContext(p.Ctx,pgAmopProbe); if err != nil { t.Fatal(err) }; defer reader.Close()
		held := fixture.HeldStatement{Executor:c,Statement:reader,SQL:pgAmopProbe}
		run(fixture.Exec("SET ROLE amop_reader",0))
		defer c.ExecContext(context.Background(),"RESET ROLE")
		for i,q := range mutations {
			for _, route := range []struct{ e fixture.Executor; prepared bool }{{c,false},{c,true},{commands[i],false}} {
				pgAmopShapes(t,p,b,c)
				s := fixture.ImplicitError(q,fixture.Diagnostic{Code:"42501",Message:"permission denied for table pg_amop"})
				s.Prepared,s.ErrorStage = route.prepared,"execute"
				fixture.RunStep(t,p.Ctx,route.e,b.Recorder,s)
				pgAmopRows(t,p,b,c,pgAmopWitnessQuery,pgAmopFacts(),false)
				pgAmopRows(t,p,b,held,pgAmopProbe,pgAmopFacts()[1:2],false,int64(10015))
				fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Select("SELECT v FROM amop_sentinel",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
			}
		}
		run(fixture.Exec("RESET ROLE",0))
		if !b.PG {
			for i,q := range mutations {
				for _, route := range []struct{ e fixture.Executor; prepared bool }{{c,false},{c,true},{commands[i],false}} {
					s := fixture.ImplicitError(q,fixture.Diagnostic{Code:"0A000",Message:"catalog source mutation is unsupported"})
					s.Prepared,s.ErrorStage = route.prepared,"execute"
					fixture.RunStep(t,p.Ctx,route.e,b.Recorder,s)
					pgAmopRows(t,p,b,c,pgAmopWitnessQuery,pgAmopFacts(),false)
					pgAmopRows(t,p,b,held,pgAmopProbe,pgAmopFacts()[1:2],false,int64(10015))
					fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT v FROM amop_sentinel",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
				}
			}
		}
	})
}
