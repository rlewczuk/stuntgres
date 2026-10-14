package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeAlterMaterializedDependencies(t *testing.T) {
	for _, container := range []string{"scalar", "array", "domain", "domain_array", "nested"} {
		for _, state := range []string{"empty", "null", "populated"} {
			for _, action := range []string{"identity", "widen"} {
				t.Run(container+"_"+state+"_"+action, func(t *testing.T) {
					fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE pair AS (id integer,note text)`, `CREATE TABLE seed(k integer)`)
						typ, input, nativeType := "pair", "(7,x)", ""
						switch container {
						case "array":
							typ, input, nativeType = "pair[]", `{"(7,x)",NULL}`, "_PAIR"
						case "domain":
							fixture.CompositeReferenceSetup(t, p, b, c, `CREATE DOMAIN wrapped AS pair`)
							typ = "wrapped"
						case "domain_array":
							fixture.CompositeReferenceSetup(t, p, b, c, `CREATE DOMAIN wrapped AS pair[]`)
							typ, input, nativeType = "wrapped", `{"(7,x)",NULL}`, "_PAIR"
						case "nested":
							fixture.CompositeReferenceSetup(t, p, b, c, `CREATE DOMAIN wrapped AS pair`, `CREATE TYPE nested AS (r wrapped)`)
							typ, input = "nested", `("(7,x)")`
						}
						projection := "'" + input + "'::" + typ
						want := [][]any{}
						if state != "empty" {
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO seed VALUES(1)`, 1))
							want = [][]any{{input}}
						}
						if state == "null" {
							projection = "NULL::" + typ
							want = [][]any{{nil}}
						}
						// No ordinary table stores pair: the sole physical consumer is mv.v.
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE MATERIALIZED VIEW mv AS SELECT "+projection+" AS v FROM seed", int64(len(want))))
						check := func() {
							fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM mv`, want, false, nativeType)
							fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM mv`, want, true, nativeType)
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT relkind,relispopulated FROM pg_class WHERE oid='mv'::regclass::oid`, []string{"relkind", "relispopulated"}, []string{"CHAR", "BOOL"}, [][]any{{"m", true}}))
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT attname,atttypid='integer'::regtype::oid AS original_type FROM pg_attribute WHERE attrelid='pair'::regclass::oid AND attnum=1`, []string{"attname", "original_type"}, []string{"NAME", "BOOL"}, [][]any{{"id", true}}))
						}
						check()
						clause := "integer"
						if action == "widen" {
							clause = "bigint"
						}
						fixture.CompositeReferenceError(t, p, c, "ALTER TYPE pair ALTER ATTRIBUTE id TYPE "+clause+" CASCADE", "execute", fixture.CompositeReferenceDiagnostic("0A000", `cannot alter type "pair" because column "mv.v" uses it`))
						check()
						// The retained materialization recipe must remain executable too.
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`REFRESH MATERIALIZED VIEW mv`, 0)))
						check()
					})
				})
			}
		}
	}
}
