package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedVirtualAlterTypeUsing(t *testing.T) {
	for _, tc := range []struct {
		name, recipe, definition, using string
		a, c, want                      any
	}{
		{"ordinary", "a+1", "(a + 1)", "b", int64(2), int64(0), int64(3)},
		{"present_null", "COALESCE(a,9)", "COALESCE(a, 9)", "b", nil, nil, int64(9)},
		{"table_qualified", "a+1", "(a + 1)", "generated_virtual_alter_owner.b", int64(2), int64(0), int64(3)},
		{"schema_qualified", "a+1", "(a + 1)", "public.generated_virtual_alter_owner.b", int64(2), int64(0), int64(3)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				name := "direct"
				if prepared {
					name = "prepared"
				}
				t.Run(name, func(t *testing.T) {
					generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
						run(fixture.Exec(`CREATE TABLE generated_virtual_alter_owner(a INT,b INT GENERATED ALWAYS AS(`+tc.recipe+`) VIRTUAL,c INT)`, 0))
						run(fixture.Exec(`INSERT INTO generated_virtual_alter_owner(a,c) VALUES($1,$2)`, 1, tc.a, tc.c))
						var definition int64
						if err := backend.DB.QueryRowContext(p.Ctx, `SELECT oid::bigint FROM pg_attrdef WHERE adrelid='public.generated_virtual_alter_owner'::regclass::oid AND adnum=2`).Scan(&definition); err != nil || definition <= 0 {
							t.Fatal(definition, err)
						}
						catalog := fixture.Select(`SELECT oid::bigint AS oid,adnum,pg_get_expr(adbin,adrelid) FROM pg_attrdef WHERE adrelid='public.generated_virtual_alter_owner'::regclass::oid ORDER BY adnum`, []string{"oid", "adnum", "pg_get_expr"}, []string{"INT8", "INT2", "TEXT"}, [][]any{{definition, int64(2), tc.definition}})
						run(catalog)
						// Even present_null has a real original row: every physical cell
						// is NULL, but its non-strict recipe must still be evaluated.
						alter := fixture.Exec(`ALTER TABLE generated_virtual_alter_owner ALTER COLUMN c TYPE BIGINT USING `+tc.using, 0)
						alter.Prepared = prepared
						run(alter)
						rows := fixture.Select(`SELECT a,b,c FROM generated_virtual_alter_owner`, []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT8"}, [][]any{{tc.a, tc.want, tc.want}})
						rows.Prepared = prepared
						run(rows)
						fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, prepared, rows.Query, rows.Columns, rows.Types, []bool{true, true, true}, 1)
						// Only ordinary c changes type; b's type, definition and OID survive.
						run(catalog)
						run(fixture.Exec(`UPDATE generated_virtual_alter_owner SET a=4`, 1))
						wantB := int64(5)
						if tc.name == "present_null" {
							wantB = 4
						}
						run(fixture.Select(rows.Query, rows.Columns, rows.Types, [][]any{{int64(4), wantB, tc.want}}))
					})
				})
			}
		})
	}
}

func TestReferenceGeneratedVirtualAlterTypeUsingAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec(`CREATE TABLE generated_virtual_alter_failure(a INT,b INT GENERATED ALWAYS AS(1/a) VIRTUAL,c INT DEFAULT 7)`, 0))
				run(fixture.Exec(`INSERT INTO generated_virtual_alter_failure(a,c) VALUES(1,10),(0,20)`, 2))
				// Base-only reads do not demand the zero-divisor recipe.
				rows := fixture.Select(`SELECT a,c FROM generated_virtual_alter_failure ORDER BY a`, []string{"a", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(20)}, {int64(1), int64(10)}})
				run(rows)
				var generation, defaultDefinition int64
				if err := backend.DB.QueryRowContext(p.Ctx, `SELECT oid::bigint FROM pg_attrdef WHERE adrelid='public.generated_virtual_alter_failure'::regclass::oid AND adnum=2`).Scan(&generation); err != nil || generation <= 0 {
					t.Fatal(generation, err)
				}
				if err := backend.DB.QueryRowContext(p.Ctx, `SELECT oid::bigint FROM pg_attrdef WHERE adrelid='public.generated_virtual_alter_failure'::regclass::oid AND adnum=3`).Scan(&defaultDefinition); err != nil || defaultDefinition <= 0 {
					t.Fatal(defaultDefinition, err)
				}
				catalog := fixture.Select(`SELECT oid::bigint AS oid,adnum,pg_get_expr(adbin,adrelid) FROM pg_attrdef WHERE adrelid='public.generated_virtual_alter_failure'::regclass::oid ORDER BY adnum`, []string{"oid", "adnum", "pg_get_expr"}, []string{"INT8", "INT2", "TEXT"}, [][]any{{generation, int64(2), "(1 / a)"}, {defaultDefinition, int64(3), "7"}})
				run(catalog)
				failure := fixture.ImplicitError(`ALTER TABLE generated_virtual_alter_failure ALTER COLUMN c TYPE BIGINT USING b`, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				failure.Prepared, failure.ErrorStage = prepared, "execute"
				run(failure)
				// A demanded recipe failure publishes neither converted rows nor DDL.
				rows.Prepared = prepared
				run(rows)
				fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, prepared, rows.Query, rows.Columns, rows.Types, []bool{true, true}, 2)
				run(catalog)
				run(fixture.Select(`SELECT a,b,c FROM generated_virtual_alter_failure WHERE a=1`, []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(10)}}))
				run(fixture.Exec(`INSERT INTO generated_virtual_alter_failure(a) VALUES(2)`, 1))
				run(fixture.Select(`SELECT a,b,c FROM generated_virtual_alter_failure WHERE a=2`, []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(0), int64(7)}}))
			})
		})
	}
}
