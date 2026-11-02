package reference_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceBtreeGINCompoundFeatureSmoke(t *testing.T) {
	var emitted string
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		compoundCleanup(t, p, b, conn)
		seed := func() {
			run(fixture.Exec("SET lc_monetary='C'", 0))
			run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
			run(fixture.Exec("CREATE TYPE compound_smoke_enum AS ENUM('z','a','m')", 0))
			run(fixture.Exec("CREATE TABLE compound_smoke(id integer,b bit(3),v varbit,e compound_smoke_enum,m money)", 0))
			run(fixture.Exec("CREATE INDEX compound_smoke_i ON compound_smoke USING gin(b bit_ops,v varbit_ops,e enum_ops,m money_ops)", 0))
			run(fixture.Prepared(fixture.Exec("INSERT INTO compound_smoke VALUES(1,'100','0','a','0.01'),(2,'011','','z','-0.01'),(3,NULL,NULL,NULL,NULL)", 3)))
		}
		seed()
		run(fixture.Select("SELECT extversion FROM pg_extension WHERE extname='btree_gin'", []string{"extversion"}, []string{"TEXT"}, [][]any{{"1.3"}}))
		for _, tc := range []struct{ column, typ, boundary, old, new string }{
			{"b", "bit(3)", "100", "100", "111"},
			{"v", "varbit", "0", "0", "10"},
			{"e", "compound_smoke_enum", "a", "a", "m"},
			{"m", "money", "0", "0.01", "0.02"},
		} {
			q := "SELECT id FROM compound_smoke WHERE " + tc.column + ">=$1::" + tc.typ + " ORDER BY id"
			held := classHeld(t, p, conn, q)
			s := ginIDs(q, []int64{1}, tc.boundary)
			classRoutes(t, p, b, conn, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			classRoutes(t, p, b, conn, ginIDs(q, nil, nil))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, ginIDs(q, nil, nil))
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM compound_smoke WHERE "+tc.column+"<$1::"+tc.typ+" ORDER BY id", []int64{2}, tc.boundary))
			run(ginIDs("SELECT id FROM compound_smoke WHERE "+tc.column+" IS NULL ORDER BY id", []int64{3}))
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM compound_smoke WHERE "+tc.column+"=$1::"+tc.typ+" ORDER BY id", []int64{1}, tc.old))
		}
		run(fixture.Exec("UPDATE compound_smoke SET b='111',v='10',e='m',m='0.02' WHERE id=1", 1))
		check := func() {
			t.Helper()
			for _, tc := range []struct{ column, typ, old, new string }{
				{"b", "bit(3)", "100", "111"}, {"v", "varbit", "0", "10"}, {"e", "compound_smoke_enum", "a", "m"}, {"m", "money", "0.01", "0.02"},
			} {
				classRoutes(t, p, b, conn, ginIDs("SELECT id FROM compound_smoke WHERE "+tc.column+"=$1::"+tc.typ+" ORDER BY id", nil, tc.old))
				q := "SELECT id FROM compound_smoke WHERE " + tc.column + ">=$1::" + tc.typ + " ORDER BY id"
				held := classHeld(t, p, conn, q)
				s := ginIDs(q, []int64{1}, tc.new)
				classRoutes(t, p, b, conn, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, ginIDs(q, nil, nil))
			}
			run(ginIDs("SELECT id FROM compound_smoke WHERE b IS NULL AND v IS NULL AND e IS NULL AND m IS NULL ORDER BY id", []int64{3}))
		}
		check()
		d := fixture.ControlsReferenceDiagnostic("42804", `operator class "money_ops" does not accept data type bit`)
		ginUtilityError(t, p, b, conn, "CREATE INDEX compound_smoke_retry ON compound_smoke USING gin(m money_ops,b money_ops)", d)
		compoundIndexAbsent(run, "compound_smoke", "compound_smoke_retry", 1)
		check()
		run(fixture.Exec("CREATE INDEX compound_smoke_retry ON compound_smoke USING gin(m money_ops,b bit_ops)", 0))
		if b.PG {
			run(fixture.Exec("DROP TABLE compound_smoke", 0))
			run(fixture.Exec("DROP TYPE compound_smoke_enum", 0))
			run(fixture.Exec("DROP EXTENSION btree_gin CASCADE", 0))
			if emitted != "" {
				ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
				if _, err := conn.ExecContext(ctx, emitted); err != nil {
					t.Fatalf("PostgreSQL compound smoke Dump replay: %v\n%s", err, emitted)
				}
			} else {
				seed()
				run(fixture.Exec("UPDATE compound_smoke SET b='111',v='10',e='m',m='0.02' WHERE id=1", 1))
				run(fixture.Exec("CREATE INDEX compound_smoke_retry ON compound_smoke USING gin(m money_ops,b bit_ops)", 0))
			}
		} else {
			emitted = fixture.ExtensionDump(t, p.Ctx, p.Native)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			run(amCount("SELECT count(*) FROM pg_extension WHERE extname='btree_gin'", 0))
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
		}
		check()
		run(amCount("SELECT count(*) FROM pg_index WHERE indrelid='compound_smoke'::regclass", 2))
	})
}
