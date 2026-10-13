package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGEnumShape(t *testing.T) {
	pgEnumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		empty := pgEnumCreate(t, p, b, b.DB, "pge_shape_empty", "")
		for _, q := range []string{"SELECT * FROM pg_catalog.pg_enum WHERE FALSE", "SELECT * FROM pg_enum WHERE FALSE", pgEnumProbe} {
			for _, arg := range []any{empty, int64(4294967295), nil} {
				args := []any{}
				if q == pgEnumProbe {
					args = append(args, arg)
				}
				pgEnumCheck(t, pgEnumQuery(t, p, b, b.DB, q, args...), empty, nil, nil)
				stmt, err := b.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				rows, err := stmt.QueryContext(p.Ctx, args...)
				if err != nil {
					stmt.Close()
					t.Fatal(err)
				}
				pgEnumCheck(t, pgEnumRead(t, rows, b.PG), empty, nil, nil)
				stmt.Close()
			}
		}
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT oid,relname,relnamespace,relkind,relnatts FROM pg_class WHERE oid=3501", []string{"oid", "relname", "relnamespace", "relkind", "relnatts"}, []string{"OID", "NAME", "OID", "CHAR", "INT2"}, [][]any{{int64(3501), "pg_enum", int64(11), "r", int64(4)}}))
		if b.PG {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT attname,atttypid::integer AS typ,atttypmod,attnotnull,attcollation::integer AS coll FROM pg_attribute WHERE attrelid=3501 AND attnum>0 ORDER BY attnum", []string{"attname", "typ", "atttypmod", "attnotnull", "coll"}, []string{"NAME", "INT4", "INT4", "BOOL", "INT4"}, [][]any{{"oid", int64(26), int64(-1), true, int64(0)}, {"enumtypid", int64(26), int64(-1), true, int64(0)}, {"enumsortorder", int64(700), int64(-1), true, int64(0)}, {"enumlabel", int64(19), int64(-1), true, int64(950)}}))
		}
	})
}

func TestReferencePGEnumDefinitionsAndQueries(t *testing.T) {
	pgEnumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a := pgEnumCreate(t, p, b, b.DB, "pge_def_planets", "'venus','earth','mars'")
		other := pgEnumCreate(t, p, b, b.DB, "pge_def_other", "'venus','earth','mars'")
		pgEnumExec(t, p, b, b.DB, "CREATE DOMAIN pge_def_domain AS pge_def_planets")
		first := pgEnumQuery(t, p, b, b.DB, pgEnumProbe, a)
		second := pgEnumQuery(t, p, b, b.DB, pgEnumProbe, other)
		pgEnumCheck(t, first, a, []string{"venus", "earth", "mars"}, []float64{1, 2, 3})
		pgEnumCheck(t, second, other, []string{"venus", "earth", "mars"}, []float64{1, 2, 3})
		for _, x := range first {
			for _, y := range second {
				if x.ID == y.ID || a == other {
					t.Fatal("aliased identities", x, y)
				}
			}
		}
		for _, name := range []string{"pge_def_domain", "pge_def_planets[]", "integer"} {
			id := pgEnumOwner(t, p, b.DB, name)
			pgEnumCheck(t, pgEnumQuery(t, p, b, b.DB, pgEnumProbe, id), id, nil, nil)
		}
		labels := []string{"", " ", "a", "A", "O'K", `back\slash`, "é", strings.Repeat("x", 63)}
		quoted := []string{}
		for _, label := range labels {
			quoted = append(quoted, "'"+strings.ReplaceAll(label, "'", "''")+"'")
		}
		exact := pgEnumCreate(t, p, b, b.DB, "pge_def_exact", strings.Join(quoted, ","))
		pgEnumCheck(t, pgEnumQuery(t, p, b, b.DB, pgEnumProbe, exact), exact, labels, []float64{1, 2, 3, 4, 5, 6, 7, 8})
		pgEnumCheck(t, pgEnumQuery(t, p, b, b.DB, "SELECT * FROM pg_enum WHERE enumtypid=$1::oid AND enumlabel='é' AND enumsortorder=7", exact), exact, []string{"é"}, []float64{7})
		pgEnumCheck(t, pgEnumQuery(t, p, b, b.DB, "SELECT * FROM pg_enum WHERE oid=$1::oid", first[1].ID), a, []string{"earth"}, []float64{2})
		fixture.PgDescriptionRows(t, p, b, fixture.Select(fmt.Sprintf("SELECT enumlabel FROM pg_enum WHERE enumtypid=%d::oid ORDER BY enumlabel", exact), []string{"enumlabel"}, []string{"NAME"}, [][]any{{""}, {" "}, {"A"}, {"O'K"}, {"a"}, {`back\slash`}, {strings.Repeat("x", 63)}, {"é"}}))
		q := "SELECT e.oid,e.enumtypid,e.enumsortorder,e.enumlabel FROM pg_enum e JOIN pg_class c ON c.oid=3501 WHERE e.enumtypid=$1::oid AND e.enumsortorder>=2 AND e.enumlabel<>'venus' ORDER BY e.enumsortorder"
		pgEnumCheck(t, pgEnumQuery(t, p, b, b.DB, q, a), a, []string{"earth", "mars"}, []float64{2, 3})
		pgEnumExec(t, p, b, b.DB, fmt.Sprintf("CREATE VIEW pge_def_view AS SELECT * FROM pg_enum WHERE enumtypid=%d::oid", a))
		viewExpected := make([][]any, len(first))
		for i, member := range first {
			viewExpected[i] = []any{member.ID, a, float64(i + 1), []string{"venus", "earth", "mars"}[i]}
		}
		view := fixture.Select("SELECT * FROM pge_def_view ORDER BY enumsortorder", pgEnumColumns, pgEnumTypes, viewExpected)
		view.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			pgEnumMetadata(t, rows, b.PG, true)
		}
		fixture.PgDescriptionRows(t, p, b, view)
		fixture.PgDescriptionRows(t, p, b, fixture.Prepared(view))
		stmt, err := b.DB.PrepareContext(p.Ctx, pgEnumProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			arg  any
			want []pgEnumRow
		}{{a, first}, {other, second}, {nil, []pgEnumRow{}}, {int64(4294967295), []pgEnumRow{}}} {
			rows, err := stmt.QueryContext(p.Ctx, tc.arg)
			if err != nil {
				t.Fatal(err)
			}
			if got := pgEnumRead(t, rows, b.PG); !reflect.DeepEqual(got, tc.want) {
				t.Fatal("held filter", got, tc.want)
			}
		}
		fixture.PgDescriptionRows(t, p, b, fixture.Select(fmt.Sprintf("SELECT enumlabel FROM pg_enum WHERE enumtypid=%d::oid ORDER BY enumlabel", a), []string{"enumlabel"}, []string{"NAME"}, [][]any{{"earth"}, {"mars"}, {"venus"}}))
	})
}

func TestReferencePGEnumAlterPositions(t *testing.T) {
	pgEnumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		id := pgEnumCreate(t, p, b, b.DB, "pge_alter", "'venus','earth','mars'")
		old := pgEnumQuery(t, p, b, b.DB, pgEnumProbe, id)
		for _, clause := range []string{"'uranus'", "'mercury' BEFORE 'venus'", "'saturn' BEFORE 'uranus'", "'jupiter' AFTER 'mars'", "'neptune' AFTER 'uranus'"} {
			pgEnumExec(t, p, b, b.DB, "ALTER TYPE pge_alter ADD VALUE "+clause)
		}
		got := pgEnumQuery(t, p, b, b.DB, pgEnumProbe, id)
		pgEnumCheck(t, got, id, []string{"mercury", "venus", "earth", "mars", "jupiter", "saturn", "uranus", "neptune"}, []float64{0, 1, 2, 3, 3.25, 3.5, 4, 5})
		pgEnumStable(t, old, got, nil, true)
		pgEnumExec(t, p, b, b.DB, "ALTER TYPE pge_alter ADD VALUE 'predecessor' BEFORE 'mercury'")
		pgEnumExec(t, p, b, b.DB, "ALTER TYPE pge_alter RENAME VALUE 'earth' TO 'Earth'")
		changed := pgEnumQuery(t, p, b, b.DB, pgEnumProbe, id)
		pgEnumCheck(t, changed, id, []string{"predecessor", "mercury", "venus", "Earth", "mars", "jupiter", "saturn", "uranus", "neptune"}, []float64{-1, 0, 1, 2, 3, 3.25, 3.5, 4, 5})
		pgEnumStable(t, got, changed, map[string]string{"earth": "Earth"}, true)
		pgEnumExec(t, p, b, b.DB, "ALTER TYPE pge_alter ADD VALUE IF NOT EXISTS 'Earth'")
		if again := pgEnumQuery(t, p, b, b.DB, pgEnumProbe, id); !reflect.DeepEqual(again, changed) {
			t.Fatal("IF NOT EXISTS changed metadata", again)
		}
	})
}

func TestReferencePGEnumRenumber(t *testing.T) {
	pgEnumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		id := pgEnumCreate(t, p, b, b.DB, "pge_dense", "'left','right'")
		old := pgEnumQuery(t, p, b, b.DB, pgEnumProbe, id)
		labels := []string{"left"}
		for i := range 40 {
			label := fmt.Sprintf("n%02d", i)
			pgEnumExec(t, p, b, b.DB, "ALTER TYPE pge_dense ADD VALUE '"+label+"' BEFORE 'right'")
			labels = append(labels, label)
		}
		labels = append(labels, "right")
		got := pgEnumQuery(t, p, b, b.DB, pgEnumProbe, id)
		pgEnumCheck(t, got, id, labels, nil)
		pgEnumStable(t, old, got, nil, false)
		if got[len(got)-1].Order == old[1].Order {
			t.Fatal("dense insertion did not renumber", got)
		}
		pgEnumExec(t, p, b, b.DB, "CREATE TABLE pge_dense_values(v pge_dense)")
		for _, label := range labels {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pge_dense_values VALUES ('"+label+"')", 1))
		}
		want := [][]any{}
		for _, label := range labels {
			want = append(want, []any{label})
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v::text AS label FROM pge_dense_values ORDER BY v", []string{"label"}, []string{"TEXT"}, want))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT enum_range(NULL::pge_dense)::text AS labels", []string{"labels"}, []string{"TEXT"}, [][]any{{"{" + strings.Join(labels, ",") + "}"}}))
	})
}
