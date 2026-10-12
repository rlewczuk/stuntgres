package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeExprFeatureSmoke(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		compositeExprSetup(t, p, b, c)
		s := fixture.Select(`SELECT ROW(1,'x') AS v`, []string{"v"}, []string{"RECORD"}, [][]any{{"(1,x)"}})
		s.CheckMetadata = compositeExprRecordMetadata(b.PG, []string{"v"})
		compositeExprSelect(t, p, b, c, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES (1,ROW(1,'x'),ROW(ROW(2,'y'),3)),(2,NULL,NULL)`, 2))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`INSERT INTO holder(k,v) VALUES ($1,ROW($2,$3))`, 1, 3, 7, "z")))
		compositeExprBoth(t, p, b, c, `SELECT (v).* FROM holder ORDER BY k`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {nil, nil}, {int64(7), "z"}})
		stmt, err := c.PrepareContext(p.Ctx, `SELECT ($1::pair).i AS i`)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, x := range []struct {
			arg  any
			want any
		}{{"(7,x)", int64(7)}, {"(9,y)", int64(9)}, {nil, nil}} {
			var got any
			if err := stmt.QueryRowContext(p.Ctx, x.arg).Scan(&got); err != nil || got != x.want {
				t.Fatalf("fresh prepared field got %#v want %#v err=%v", got, x.want, err)
			}
		}
		held, err := b.DB.QueryContext(p.Ctx, `SELECT (v).* FROM holder ORDER BY k`)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		compositeExprSelect(t, p, b, c, fixture.Select(`UPDATE holder SET v.i=8,v.t='n' WHERE k=2 RETURNING (v).*`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(8), "n"}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`UPDATE holder SET v.i=(v).i+1,v.t=(v).i::text WHERE k=1`, 1))
		compositeExprBoth(t, p, b, c, `SELECT (v).* FROM holder ORDER BY k`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "1"}, {int64(8), "n"}, {int64(7), "z"}})
		compositeExprError(t, p, c, `SELECT (v).missing FROM holder`, "42703", `column "missing" not found in data type pair`, "v")
		d := fixture.CompositeReferenceDiagnostic("22012", "division by zero")
		fixture.CompositeReferenceError(t, p, c, `UPDATE holder SET v.i=100,v.t=(1/(3-k))::text`, "execute", d)
		compositeExprBoth(t, p, b, c, `SELECT (v).* FROM holder ORDER BY k`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "1"}, {int64(8), "n"}, {int64(7), "z"}})
		compositeExprBoth(t, p, b, c, `SELECT ROW(1,NULL) IS NULL AS mixed_null,ROW(1,NULL) IS NOT NULL AS mixed_not_null,ROW(NULL,NULL) IS NULL AS all_null`, []string{"mixed_null", "mixed_not_null", "all_null"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, false, true}})
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			compositeExprBoth(t, p, b, c, `SELECT (v).* FROM holder ORDER BY k`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "1"}, {int64(8), "n"}, {int64(7), "z"}})
		}
		fixture.UpdateCoreRetained(t, b.PG, held, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {nil, nil}, {int64(7), "z"}}, false)
	})
}
