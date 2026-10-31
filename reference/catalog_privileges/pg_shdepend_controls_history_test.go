package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGShdependControlsHistoricalPolicy(t *testing.T) {
	p := shScenario(t)
	for _, b := range p.Backends() {
		db, table, owner, reader, grantor := shSetup(t, p, b)
		shExec(t, p, b, b.DB,
			"CREATE EXTENSION pageinspect",
			"GRANT SELECT ON sh_table TO sh_reader",
			"GRANT UPDATE(b) ON sh_table TO sh_grantor",
			"ALTER EXTENSION pageinspect ADD TABLE sh_table",
			"REVOKE SELECT ON sh_table FROM sh_reader",
			"REVOKE UPDATE(b) ON sh_table FROM sh_grantor",
			"CREATE POLICY sh_history_policy ON sh_table TO sh_reader,sh_reader USING (true)")
		policy := shOID(t, p, b.DB, "SELECT oid FROM pg_policy WHERE polname='sh_history_policy'")
		tableEdges := func(object int64) [][]any {
			return [][]any{
				shEdge(db, 1259, object, 0, owner, "o"),
				shEdge(db, 1259, object, 0, owner, "i"),
				shEdge(db, 1259, object, 0, reader, "i"),
				shEdge(db, 1259, object, 2, owner, "i"),
				shEdge(db, 1259, object, 2, grantor, "i"),
			}
		}
		check := func() {
			t.Helper()
			table = shOID(t, p, b.DB, "SELECT oid FROM pg_class WHERE relname='sh_table' AND relnamespace='public'::regnamespace")
			policy = shOID(t, p, b.DB, "SELECT oid FROM pg_policy WHERE polname='sh_history_policy'")
			shDatabaseBag(t, p, b, b.DB, db, 1259, table, tableEdges(table)...)
			shDatabaseBag(t, p, b, b.DB, db, 3256, policy, shEdge(db, 3256, policy, 0, reader, "r"), shEdge(db, 3256, policy, 0, reader, "r"))
		}
		check()
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			oldTable, oldPolicy := table, policy
			old, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_catalog.pg_shdepend WHERE dbid=$1 AND ((classid=1259 AND objid=$2) OR (classid=3256 AND objid=$3))", db, table, policy)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			invalid := dump.String() + "INSERT INTO public.sh_table(missing) VALUES(1);"
			err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(invalid))
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column \"missing\" of relation \"sh_table\" does not exist", Position: fixture.Position(utf8.RuneCountInString(dump.String()+"INSERT INTO public.sh_table(") + 1)})
			check()
			if table != oldTable || policy != oldPolicy {
				t.Fatal("failed Restore changed dependent identities")
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.Native, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check()
			if table != oldTable || policy != oldPolicy {
				t.Fatal("canceled Restore changed dependent identities")
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			shDatabaseBag(t, p, b, b.DB, db, 1259, oldTable)
			shDatabaseBag(t, p, b, b.DB, db, 3256, oldPolicy)
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			check() // Both historical columns and duplicate policy targets rebuild.
			expectedOld := tableEdges(oldTable)
			expectedOld = append(expectedOld, shEdge(db, 3256, oldPolicy, 0, reader, "r"), shEdge(db, 3256, oldPolicy, 0, reader, "r"))
			shRetained(t, old, expectedOld)
		}
		shExec(t, p, b, b.DB, "DROP EXTENSION pageinspect CASCADE")
	}
}
