package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCastDefinitionTransactionCacheIsolation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			peer := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, b.Recorder, s) }
			for _, q := range []string{castDefinitionSource, `CREATE FUNCTION cast_label(cast_src) RETURNS text LANGUAGE SQL AS $$ SELECT 'bound'::text $$`} {
				run(c, fixture.Exec(q, 0))
			}
			q := "SELECT ($1::cast_src)::text AS v"
			ownerStmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer ownerStmt.Close()
			peerStmt, err := peer.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer peerStmt.Close()
			ownerHeld := fixture.HeldStatement{Executor: c, Statement: ownerStmt, SQL: q}
			peerHeld := fixture.HeldStatement{Executor: peer, Statement: peerStmt, SQL: q}
			value := func(v string) fixture.Step {
				return fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{v}}, "red")
			}
			run(ownerHeld, value("red"))
			run(peerHeld, value("red"))
			run(c, fixture.Exec("BEGIN", 0))
			run(c, fixture.Exec("SAVEPOINT absent", 0))
			run(c, fixture.Exec("CREATE CAST (cast_src AS text) WITH FUNCTION cast_label(cast_src)", 0))
			run(c, value("bound"))
			run(peer, value("red"))
			run(peerHeld, value("red"))
			// PostgreSQL retains its previously analyzed IO node; native analysis
			// observes the private pair generation. Neither is a cache leak to peers.
			ownerWant := "bound"
			if b.PG {
				ownerWant = "red"
			}
			run(ownerHeld, value(ownerWant))
			run(c, fixture.Exec("ROLLBACK TO absent", 0))
			run(c, value("red"))
			run(ownerHeld, value("red"))
			run(peerHeld, value("red"))
			run(c, fixture.Exec("CREATE CAST (cast_src AS text) WITH FUNCTION cast_label(cast_src)", 0))
			run(c, fixture.Exec("RELEASE absent", 0))
			run(c, fixture.Exec("COMMIT", 0))
			run(peer, value("bound"))
			peerWant := "bound"
			if b.PG {
				peerWant = "red"
			}
			run(peerHeld, value(peerWant))
		})
	}
}

func TestReferenceCastDefinitionTransactionFailures(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{castDefinitionSource, castDefinitionScore, "CREATE TABLE cast_dest(n integer)", "CREATE FUNCTION cast_receiver(integer) RETURNS integer LANGUAGE SQL AS $$ SELECT $1+100 $$", "BEGIN", castDefinitionEdge} {
				run(fixture.Exec(q, 0))
			}
			assignment := fixture.ControlsReferenceDiagnostic("42804", `column "n" is of type integer but expression is of type cast_src`)
			assignment.Hint = "You will need to rewrite or cast the expression."
			assignment.Position = "31"
			receiver := fixture.ControlsReferenceDiagnostic("42883", "function cast_receiver(cast_src) does not exist")
			receiver.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			receiver.Position = "8"
			for _, s := range []fixture.Step{fixture.ErrorDiagnostic("INSERT INTO cast_dest VALUES ('red'::cast_src)", assignment), fixture.ErrorDiagnostic("SELECT cast_receiver('red'::cast_src)", receiver), fixture.ErrorDiagnostic(castDefinitionEdge, fixture.ControlsReferenceDiagnostic("42710", "cast from type cast_src to type integer already exists"))} {
				run(fixture.Exec("SAVEPOINT failure", 0))
				run(s)
				run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
				run(fixture.Exec("ROLLBACK TO failure", 0))
				run(fixture.Select("SELECT count(*) AS n FROM cast_dest", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				run(fixture.Exec("RELEASE failure", 0))
			}
			run(fixture.Select("SELECT ('red'::cast_src)::integer AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(41)}}))
			run(fixture.Exec("ROLLBACK", 0))
		})
	}
}

func TestReferenceCastDefinitionTransactionNotices(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec(castDefinitionSource, 0))
			q := "DROP CAST IF EXISTS (cast_src AS integer)"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			if len(notices) != 0 {
				t.Fatal("Prepare emitted notice", notices)
			}
			for _, end := range []string{"ROLLBACK", "COMMIT"} {
				fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("BEGIN", 0))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, rec, fixture.Exec(q, 0))
				want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "cast from type cast_src to type pg_catalog.int4 does not exist, skipping"}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("notices=%#v want=%#v", notices, want)
				}
				fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec(end, 0))
				notices = nil
			}
		})
	}
}
