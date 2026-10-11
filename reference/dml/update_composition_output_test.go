package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceUpdateCoreCompositionConstraintOutput(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(context.Background(), "SET bytea_output=hex")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE nn(id INT,d DATE,b BYTEA,n INT NOT NULL)", 0))
			run(fixture.Exec(`INSERT INTO nn VALUES(1,'2000-01-01','\x01',1)`, 1))
			failed, err := c.PrepareContext(p.Ctx, "UPDATE nn SET b=$1,d=$2,n=NULL WHERE id=1")
			if err != nil {
				t.Fatal(err)
			}
			defer failed.Close()
			for _, output := range []struct{ name, detail string }{{"hex", `Failing row contains (1, 2024-02-29, \x005cff, null).`}, {"escape", `Failing row contains (1, 2024-02-29, \000\\\377, null).`}} {
				run(fixture.Exec("SET bytea_output="+output.name, 0))
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "n" of relation "nn" violates not-null constraint`, Detail: output.detail, Schema: "public", Table: "nn", Column: "n"}
				for _, prepared := range []bool{false, true} {
					var err error
					if prepared {
						_, err = failed.ExecContext(p.Ctx, []byte{0, 92, 255}, "2024-02-29")
					} else {
						_, err = c.ExecContext(p.Ctx, "UPDATE nn SET b=$1,d=$2,n=NULL WHERE id=1", []byte{0, 92, 255}, "2024-02-29")
					}
					fixture.AssertDiagnostic(t, err, &want)
				}
				var original any = "2000-01-01"
				if backend.PG {
					original = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
				}
				fixture.UpdateCoreByteRows(t, p.Ctx, c, "SELECT b FROM nn ORDER BY id", [][][]byte{{{1}}})
				run(fixture.Select("SELECT id,d,b,n FROM nn", []string{"id", "d", "b", "n"}, []string{"INT4", "DATE", "BYTEA", "INT4"}, [][]any{{int64(1), original, "\x01", int64(1)}}))
			}
		})
	}
}
