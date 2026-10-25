package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTypedTableTemporaryPrivateProviderComposition(t *testing.T) {
	for _, typed := range []bool{false, true} {
		name := "ordinary"
		if typed {
			name = "typed"
		}
		t.Run(name, func(t *testing.T) {
			typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
				if typed {
					run(fixture.Exec("CREATE TYPE pair AS (id integer,note text)", 0))
				}
				run(fixture.Exec("BEGIN", 0))
				committed := false
				defer func() {
					if !committed {
						c.ExecContext(p.Ctx, "ROLLBACK")
					}
				}()
				if typed {
					run(fixture.Exec("CREATE TEMP TABLE root OF pair", 0))
				} else {
					run(fixture.Exec("CREATE TEMP TABLE root(id integer,note text)", 0))
				}
				// A completed statement owns this row type in the transaction's
				// private catalog. The next CREATE must consult that exact provider.
				run(fixture.Exec("INSERT INTO pg_temp.root VALUES (11,'seed'),(12,NULL)", 2))
				run(fixture.Exec("CREATE TEMP TABLE holder(k integer,v pg_temp.root)", 0))
				run(fixture.Exec("INSERT INTO pg_temp.holder VALUES (1,'(7,x)'),(2,NULL),(3,'(,)')", 3))
				var rootRelation, rootType, holderRelation, holderType int64
				identities := "SELECT 'pg_temp.root'::regclass::oid,'pg_temp.root'::regtype::oid,'pg_temp.holder'::regclass::oid,'pg_temp.holder'::regtype::oid"
				if err := c.QueryRowContext(p.Ctx, identities).Scan(&rootRelation, &rootType, &holderRelation, &holderType); err != nil {
					t.Fatal(err)
				}
				if rootRelation == 0 || rootType == 0 || holderRelation == 0 || holderType == 0 || rootRelation == holderRelation || rootType == holderType {
					t.Fatalf("invalid private relation/own-row identities: %d %d %d %d", rootRelation, rootType, holderRelation, holderType)
				}
				assertState := func() {
					run(fixture.Select("SELECT * FROM pg_temp.root ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(11), "seed"}, {int64(12), nil}}))
					fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM pg_temp.root", []string{"id", "note"}, []string{"INT4", "TEXT"}, []bool{true, true}, 2)
					run(fixture.Select("SELECT k FROM pg_temp.holder ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
					fixture.CompositeReferenceRows(t, p, b, c, "SELECT v FROM pg_temp.holder ORDER BY k", [][]any{{"(7,x)"}, {nil}, {"(,)"}}, prepared)
					fixture.CompositeReferenceRows(t, p, b, c, "SELECT v FROM pg_temp.holder WHERE k=0", [][]any{}, prepared)
					run(fixture.TypedTableAttachment("pg_temp.root", "pair", typed))
					run(fixture.TypedTableAttachment("pg_temp.holder", "pair", false))
					// Public ownership and column-provider edges are separate from
					// the typed table's attachment to its standalone source.
					run(fixture.Select("SELECT deptype FROM pg_depend WHERE classid=1247::oid AND objid='pg_temp.root'::regtype::oid AND objsubid=0 AND refclassid='pg_class'::regclass::oid AND refobjid='pg_temp.root'::regclass::oid AND refobjsubid=0", []string{"deptype"}, []string{"CHAR"}, [][]any{{"i"}}))
					run(fixture.Select("SELECT deptype FROM pg_depend WHERE classid='pg_class'::regclass::oid AND objid='pg_temp.holder'::regclass::oid AND objsubid=2 AND refclassid=1247::oid AND refobjid='pg_temp.root'::regtype::oid AND refobjsubid=0", []string{"deptype"}, []string{"CHAR"}, [][]any{{"n"}}))
					if typed {
						run(fixture.Select("SELECT 'pg_temp.root'::regtype::oid<>'pair'::regtype::oid AS distinct_row", []string{"distinct_row"}, []string{"BOOL"}, [][]any{{true}}))
						run(fixture.Select("SELECT deptype FROM pg_depend WHERE classid='pg_class'::regclass::oid AND objid='pg_temp.root'::regclass::oid AND objsubid=0 AND refclassid=1247::oid AND refobjid='pair'::regtype::oid AND refobjsubid=0", []string{"deptype"}, []string{"CHAR"}, [][]any{{"n"}}))
					}
				}
				assertState()
				run(fixture.Exec("COMMIT", 0))
				committed = true
				// An aborted transaction could return a successful COMMIT while
				// rolling back. Exact surviving data and identities rule that out.
				assertState()
				var afterRootRelation, afterRootType, afterHolderRelation, afterHolderType int64
				if err := c.QueryRowContext(p.Ctx, identities).Scan(&afterRootRelation, &afterRootType, &afterHolderRelation, &afterHolderType); err != nil {
					t.Fatal(err)
				}
				if afterRootRelation != rootRelation || afterRootType != rootType || afterHolderRelation != holderRelation || afterHolderType != holderType {
					t.Fatal("COMMIT changed relation or own-row identity")
				}
			})
		})
	}
}
