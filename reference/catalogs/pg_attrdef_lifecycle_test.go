package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAttrdefLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE attrdef_lifecycle(a INT,b INT DEFAULT 7,c INT DEFAULT 9)", 0))
			owner := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_class WHERE relname='attrdef_lifecycle'")
			query := "SELECT adnum,pg_get_expr(adbin,adrelid) FROM pg_catalog.pg_attrdef WHERE adrelid=$1 ORDER BY adnum"
			stmt, err := conn.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{conn, stmt, query}
			raw, err := conn.PrepareContext(p.Ctx, attrdefRawQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			heldRaw := fixture.HeldStatement{conn, raw, attrdefRawQuery}
			check := func(nums []int64, expr []string) [][]any {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, attrdefSemantics(owner, nums, expr))
				return attrdefRead(t, p, b, heldRaw, owner, nums, false)
			}
			initial := check([]int64{2, 3}, []string{"7", "9"})
			var snapshot *sql.Rows
			var saved []*sql.ColumnType
			if !b.PG {
				snapshot, err = p.Native.QueryContext(p.Ctx, attrdefRawQuery, owner)
				if err != nil {
					t.Fatal(err)
				}
				defer snapshot.Close()
				saved, err = snapshot.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				attrdefMetadata(t, false, saved)
			}
			run(fixture.Exec("ALTER TABLE attrdef_lifecycle RENAME TO attrdef_lifecycle_renamed", 0))
			run(fixture.Exec("ALTER TABLE attrdef_lifecycle_renamed RENAME b TO renamed_b", 0))
			if current := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_class WHERE relname='attrdef_lifecycle_renamed'"); current != owner {
				t.Fatal("rename changed owner", current, owner)
			}
			attrdefSame(t, check([]int64{2, 3}, []string{"7", "9"}), initial)
			run(fixture.Exec("ALTER TABLE attrdef_lifecycle_renamed DROP a", 0))
			attrdefSame(t, check([]int64{2, 3}, []string{"7", "9"}), initial)
			run(fixture.Exec("ALTER TABLE attrdef_lifecycle_renamed ALTER renamed_b SET DEFAULT 11", 0))
			replaced := check([]int64{2, 3}, []string{"11", "9"})
			if replaced[0][0] == initial[0][0] || replaced[1][0] != initial[1][0] {
				t.Fatal("replacement identities", initial, replaced)
			}
			for range 2 {
				run(fixture.Exec("ALTER TABLE attrdef_lifecycle_renamed ALTER renamed_b DROP DEFAULT", 0))
				remaining := check([]int64{3}, []string{"9"})
				if remaining[0][0] != initial[1][0] {
					t.Fatal("survivor identity changed")
				}
			}
			run(fixture.Exec("ALTER TABLE attrdef_lifecycle_renamed ALTER renamed_b SET DEFAULT 13", 0))
			readded := check([]int64{2, 3}, []string{"13", "9"})
			if readded[0][0] == initial[0][0] || readded[0][0] == replaced[0][0] || readded[1][0] != initial[1][0] {
				t.Fatal("re-add identities", readded)
			}
			run(fixture.Exec("ALTER TABLE attrdef_lifecycle_renamed ADD d INT DEFAULT 17", 0))
			added := check([]int64{2, 3, 4}, []string{"13", "9", "17"})
			if added[0][0] != readded[0][0] || added[1][0] != initial[1][0] {
				t.Fatal("ADD changed existing identities")
			}
			run(fixture.Exec("ALTER TABLE attrdef_lifecycle_renamed DROP c", 0))
			remaining := check([]int64{2, 4}, []string{"13", "17"})
			if remaining[0][0] != added[0][0] || remaining[1][0] != added[2][0] {
				t.Fatal("DROP changed survivors")
			}
			run(fixture.Exec("DROP TABLE attrdef_lifecycle_renamed", 0))
			check(nil, nil)
			if snapshot != nil {
				columns, err := snapshot.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				attrdefMetadata(t, false, columns)
				var retained [][]any
				for snapshot.Next() {
					var id, rel, num int64
					var tree string
					if err := snapshot.Scan(&id, &rel, &num, &tree); err != nil {
						t.Fatal(err)
					}
					retained = append(retained, []any{id, rel, num, tree})
				}
				if snapshot.Err() != nil {
					t.Fatal(snapshot.Err())
				}
				attrdefSame(t, retained, initial)
				if err := snapshot.Close(); err != nil {
					t.Fatal(err)
				}
				attrdefMetadata(t, false, saved)
			}
		})
	}
}

func TestReferenceAttrdefDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE attrdef_diagnostics(v INT DEFAULT 7)", 0))
			owner := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_class WHERE relname='attrdef_diagnostics'")
			before := attrdefRead(t, p, b, conn, owner, []int64{1}, false)
			for _, tc := range []struct{ query, position string }{{"SELECT missing FROM pg_catalog.pg_attrdef", "8"}, {"SELECT /*é*/ missing FROM pg_catalog.pg_attrdef", "14"}} {
				for _, prepared := range []bool{false, true} {
					want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
					want.Position = tc.position
					s := fixture.ErrorDiagnostic(tc.query, want)
					s.SelectRows = true
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					run(s)
					attrdefSame(t, attrdefRead(t, p, b, conn, owner, []int64{1}, false), before)
					run(attrdefSemantics(owner, []int64{1}, []string{"7"}))
				}
			}
		})
	}
}
