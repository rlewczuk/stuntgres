package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// REL_18_6 pg_opclass.h explicitly has no unique DEFAULT(method,input) index.
// DefineOpClass scans committed defaults under RowExclusiveLock. Distinct names
// and distinct families deliberately avoid any incidental unique-index wait.
func TestReferenceTransactionalDefinitionConcurrentDefaults(t *testing.T) {
	definitionScenario(t, []string{"tx_default_am"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn) {
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		defer c.ExecContext(p.Ctx, `ROLLBACK`)
		runA := definitionRun(t, p, b, a)
		runC := definitionRun(t, p, b, c)
		runA(fixture.Exec(`CREATE ACCESS METHOD tx_default_am TYPE INDEX HANDLER bthandler`, 0))
		runA(fixture.Exec(`CREATE TABLE tx_default_rows(n integer)`, 0))
		missing := fixture.ImplicitError(`CREATE INDEX tx_default_missing ON tx_default_rows USING tx_default_am(n)`, fixture.Diagnostic{Code: "42704", Message: `data type integer has no default operator class for access method "tx_default_am"`, Hint: `You must specify an operator class for the index or define a default operator class for the data type.`})
		missing.ErrorStage = "execute"
		runA(missing)
		runA(definitionCount(`SELECT count(*) FROM pg_class WHERE relname='tx_default_missing'`, 0))
		for _, name := range []string{"first", "second"} {
			runA(fixture.Exec(`CREATE OPERATOR FAMILY public.tx_default_`+name+` USING tx_default_am`, 0))
		}
		method := definitionID(t, p, a, `SELECT oid::bigint FROM pg_am WHERE amname='tx_default_am'`)
		runA(fixture.Exec(`BEGIN`, 0))
		runC(fixture.Exec(`BEGIN`, 0))
		runA(fixture.Exec(`CREATE OPERATOR CLASS public.tx_default_first DEFAULT FOR TYPE integer USING tx_default_am FAMILY public.tx_default_first AS `+definitionClassItems, 0))
		runC(fixture.Exec(`CREATE OPERATOR CLASS public.tx_default_second DEFAULT FOR TYPE integer USING tx_default_am FAMILY public.tx_default_second AS `+definitionClassItems, 0))
		runA(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname IN ('tx_default_first','tx_default_second')`, 1))
		runC(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname IN ('tx_default_first','tx_default_second')`, 1))
		definitionRun(t, p, b, b.DB)(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname IN ('tx_default_first','tx_default_second')`, 0))
		runA(fixture.Exec(`COMMIT`, 0))
		first := definitionID(t, p, a, `SELECT oid::bigint FROM pg_opclass WHERE opcname='tx_default_first'`)
		runA(fixture.Exec(`CREATE INDEX tx_default_retained ON tx_default_rows USING tx_default_am(n)`, 0))
		retained := definitionID(t, p, a, `SELECT oid::bigint FROM pg_class WHERE relname='tx_default_retained'`)
		runA(definitionIndexProvider(retained, first, method))
		runC(fixture.Exec(`COMMIT`, 0))
		runA(fixture.Select(`SELECT opcname,opcmethod::bigint,opcintype::bigint,opcdefault FROM pg_opclass WHERE opcname IN ('tx_default_first','tx_default_second') ORDER BY opcname`, []string{"opcname", "opcmethod", "opcintype", "opcdefault"}, []string{"NAME", "INT8", "INT8", "BOOL"}, [][]any{{"tx_default_first", method, int64(23), true}, {"tx_default_second", method, int64(23), true}}))
		checkRetained := func() {
			runA(definitionIndexProvider(retained, first, method))
		}
		checkRetained()
		if !b.PG {
			var rejected bytes.Buffer
			err := stuntgres.Dump(p.Ctx, p.Native, &rejected)
			fixture.TransactionOrdinary(t, err, "stuntgres: multiple default operator classes cannot be dumped")
			if rejected.Len() != 0 {
				t.Fatal("unrepresentable graph wrote a prefix", rejected.String())
			}
			checkRetained()
		}
		ambiguous := `CREATE INDEX tx_default_ambiguous ON tx_default_rows USING tx_default_am(n)`
		held := definitionHeld(t, p, a, ambiguous)
		for _, prepared := range []bool{false, true} {
			failure := fixture.ImplicitError(ambiguous, fixture.Diagnostic{Code: "42710", Message: `there are multiple default operator classes for data type integer`})
			failure.ErrorStage = "execute"
			if prepared {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
			} else {
				runA(failure)
			}
			runA(definitionCount(`SELECT count(*) FROM pg_class WHERE relname='tx_default_ambiguous'`, 0))
		}
		runA(fixture.Exec(`CREATE INDEX tx_default_explicit ON tx_default_rows USING tx_default_am(n public.tx_default_first)`, 0))
		explicit := definitionID(t, p, a, `SELECT oid::bigint FROM pg_class WHERE relname='tx_default_explicit'`)
		runA(definitionIndexProvider(explicit, first, method))
		runA(fixture.Exec(`INSERT INTO tx_default_rows VALUES(1),(3),(NULL)`, 3))
		checkRetained()
		runA(fixture.Exec(`DROP OPERATOR CLASS public.tx_default_second USING tx_default_am RESTRICT`, 0))
		runA(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname IN ('tx_default_first','tx_default_second') AND opcdefault`, 1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(ambiguous, 0))
		recovered := definitionID(t, p, a, `SELECT oid::bigint FROM pg_class WHERE relname='tx_default_ambiguous'`)
		runA(definitionIndexProvider(recovered, first, method))
		checkRetained()
		runA(fixture.Select(`SELECT n FROM tx_default_rows ORDER BY n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}, {nil}}))
		if !b.PG {
			var encoded bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &encoded); err != nil {
				t.Fatal("unique graph remained nonrepresentable", err)
			}
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/unique-restored")
			if err != nil {
				t.Fatal(err)
			}
			clone := sql.OpenDB(connector)
			defer clone.Close()
			if err := stuntgres.Restore(p.Ctx, clone, bytes.NewReader(encoded.Bytes())); err != nil {
				t.Fatal(err)
			}
			recorded, rec := fixture.RecordedNativePool(t, connector)
			restoredMethod := definitionID(t, p, recorded, `SELECT oid::bigint FROM pg_am WHERE amname='tx_default_am'`)
			restoredClass := definitionID(t, p, recorded, `SELECT oid::bigint FROM pg_opclass WHERE opcname='tx_default_first'`)
			restoredIndex := definitionID(t, p, recorded, `SELECT oid::bigint FROM pg_class WHERE relname='tx_default_retained'`)
			fixture.RunStep(t, p.Ctx, recorded, rec, definitionIndexProvider(restoredIndex, restoredClass, restoredMethod))
			fixture.RunStep(t, p.Ctx, recorded, rec, fixture.Select(`SELECT n FROM tx_default_rows ORDER BY n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}, {nil}}))
		}
	})
}
