package reference_test

import (
	"bytes"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Logical controls have no PostgreSQL API equivalent. The same SQL assertions
// follow actual native Dump replay on PG and native Reset/Restore respectively.
func TestReferencePGEnumControls(t *testing.T) {
	pgEnumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		setup := []string{
			"CREATE TYPE pge_control AS ENUM ('z','q')",
			"CREATE DOMAIN pge_control_domain AS pge_control",
			"CREATE TYPE pge_control_box AS (v pge_control)",
			"CREATE TABLE pge_control_values(v pge_control,d pge_control_domain,c pge_control_box)",
			`INSERT INTO pge_control_values VALUES ('q','q','(q)'),('z','z','(z)')`,
			"ALTER TYPE pge_control ADD VALUE 'a' BEFORE 'q'",
			"ALTER TYPE pge_control ADD VALUE 'first' BEFORE 'z'",
			"ALTER TYPE pge_control RENAME VALUE 'q' TO 'end'",
		}
		for _, q := range setup {
			if _, err := b.DB.ExecContext(p.Ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		id := pgEnumOwner(t, p, b.DB, "pge_control")
		original := pgEnumQuery(t, p, b, b.DB, pgEnumProbe, id)
		pgEnumCheck(t, original, id, []string{"first", "z", "a", "end"}, []float64{0, 1, 1.5, 2})
		checkData := func() {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v::text AS v,d::text AS d,c::text AS c FROM pge_control_values ORDER BY pge_control_values.v", []string{"v", "d", "c"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"z", "z", "(z)"}, {"end", "end", "(end)"}}))
		}
		checkData()
		old, err := b.DB.QueryContext(p.Ctx, pgEnumProbe, id)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		var dump bytes.Buffer
		dumpPool := p.Native
		if b.PG {
			// The preceding native backend has already populated p.Native.
			// Use the fixture's independent public Driver/control pool for
			// PostgreSQL's genuine logical-dump replay witness.
			isolated := fixture.NewFixture(t, false, true)
			dumpPool = isolated.Native
			for _, q := range setup {
				if _, err := dumpPool.ExecContext(p.Ctx, q); err != nil {
					t.Fatal("native dump setup", err)
				}
			}
		}
		if err := stuntgres.Dump(p.Ctx, dumpPool, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "CREATE TYPE") || strings.Contains(dump.String(), "INSERT INTO pg_catalog.pg_enum") {
			t.Fatal("not a logical enum dump", dump.String())
		}
		if b.PG {
			for _, q := range []string{"DROP TABLE pge_control_values", "DROP TYPE pge_control_box", "DROP DOMAIN pge_control_domain", "DROP TYPE pge_control"} {
				pgEnumExec(t, p, b, b.DB, q)
			}
			if _, err := b.DB.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("PostgreSQL replay of real native dump", err, dump.String())
			}
		} else {
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			pgEnumCheck(t, pgEnumQuery(t, p, b, b.DB, pgEnumProbe, id), id, nil, nil)
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
		}
		newID := pgEnumOwner(t, p, b.DB, "pge_control")
		current := pgEnumQuery(t, p, b, b.DB, pgEnumProbe, newID)
		pgEnumCheck(t, current, newID, []string{"first", "z", "a", "end"}, []float64{1, 2, 3, 4})
		for _, a := range original {
			for _, c := range current {
				if a.ID == c.ID {
					t.Fatal("recreation reused member identity", a, c)
				}
			}
		}
		if got := pgEnumRead(t, old, b.PG); !reflect.DeepEqual(got, original) {
			t.Fatal("controls changed retained rows", got, original)
		}
		checkData()
		if !b.PG {
			err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TYPE pge_failed AS ENUM ('ok'); CREATE TABLE pge_failed_values(v pge_failed); INSERT INTO pge_failed_values VALUES ('missing');"))
			if err == nil {
				t.Fatal("late invalid Restore succeeded")
			}
			if got := pgEnumQuery(t, p, b, b.DB, pgEnumProbe, newID); !reflect.DeepEqual(got, current) {
				t.Fatal("failed Restore changed catalog", got, current)
			}
			checkData()
		}
	})
}
