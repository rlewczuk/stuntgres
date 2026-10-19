package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func pgTypeID(t *testing.T, p *fixture.Pair, b fixture.Backend, name string) int64 {
	t.Helper()
	var id int64
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT $1::regtype::oid", name).Scan(&id); err != nil {
		t.Fatal(name, err)
	}
	if id <= 0 {
		t.Fatal("nonpositive type identity", name, id)
	}
	return id
}
func pgTypeGolden(id int64) []any {
	for _, r := range pgTypeBuiltinFacts {
		if r[0] == id {
			return append([]any(nil), r...)
		}
	}
	panic(fmt.Sprint("missing independent release golden ", id))
}

// DefineDomain: immediate base identity, inherited physical/output/analyze facts,
// own input/receive, zero subscript/element/modifier callbacks; never preferred.
func pgTypeDomainGolden(base []any, id int64, name string, owner, namespace, array, baseID, modifier, dimensions int64) []any {
	r := append([]any(nil), base...)
	r[0], r[1], r[2], r[3] = id, name, namespace, owner
	r[6], r[8], r[11], r[12], r[13], r[14] = "d", false, int64(0), int64(0), int64(0), array
	r[15], r[17], r[19], r[20] = int64(2597), int64(2598), int64(0), int64(0)
	r[24], r[25], r[26], r[27] = false, baseID, modifier, dimensions
	r[29], r[30], r[31] = nil, nil, nil
	return r
}
func pgTypeDynamicArrayGolden(element []any, id int64, name string) []any {
	r := pgTypeGolden(1007)
	r[0], r[1], r[2], r[3] = id, name, element[2], element[3]
	r[13], r[28] = element[0], element[28]
	if element[22] == "d" {
		r[22] = "d"
	}
	return r
}
func pgTypeFamilyRow(t *testing.T, p *fixture.Pair, b fixture.Backend, r []any) {
	t.Helper()
	q := "SELECT " + pgTypeNumericProjection + " FROM pg_type WHERE oid=$1::oid"
	s := fixture.Select(q, pgTypeColumns, pgTypeNumericTypes(), [][]any{r}, r[0])
	fixture.PgDescriptionRows(t, p, b, s)
	fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
}
func TestReferencePGTypeFamilies(t *testing.T) {
	pgTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		t.Run("seeded_time_stamp", func(t *testing.T) {
			// REL_18_6 information_schema.sql has CURRENT_TIMESTAMP(2), SQLValueFunction
			// op4/type1184/typmod2. Root's pinned18.6 discovery observed stored location-1.
			id := pgTypeID(t, p, b, "information_schema.time_stamp")
			array := pgTypeID(t, p, b, "information_schema.time_stamp[]")
			var namespace int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='information_schema'").Scan(&namespace); err != nil {
				t.Fatal(err)
			}
			r := pgTypeDomainGolden(pgTypeGolden(1184), id, "time_stamp", 10, namespace, array, 1184, 2, 0)
			r[29] = "{SQLVALUEFUNCTION :op 4 :type 1184 :typmod 2 :location -1}"
			r[30] = "CURRENT_TIMESTAMP(2)"
			// Retain full raw default/tree and metadata diagnostics on exact mismatch.
			raw, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_type WHERE oid=$1::oid", id)
			if err != nil {
				t.Fatal(err)
			}
			if id != 13254 || array != 13253 {
				t.Errorf("released seeded identities got=%d/%d want=13254/13253", id, array)
			}
			defer raw.Close()
			desc, err := raw.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			pgTypeSavedMetadata(t, desc, b.PG)
			if !raw.Next() {
				t.Fatal("seeded domain absent", raw.Err())
			}
			values, dest := make([]any, 32), make([]any, 32)
			for i := range dest {
				dest[i] = &values[i]
			}
			if err := raw.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			gotTree, gotDefault := fixture.Normalize(values[29]), fixture.Normalize(values[30])
			if gotTree != r[29] || gotDefault != r[30] {
				t.Errorf("seeded time_stamp exact defaults: tree raw=%T/%s normalized=%#v default raw=%T/%s normalized=%#v; want tree=%q default=%q; complete raw row=%#v", values[29], values[29], gotTree, values[30], values[30], gotDefault, r[29], r[30], values)
			}
			if raw.Next() || raw.Err() != nil {
				t.Fatal("seeded domain multiplicity/error", raw.Err())
			}
			raw.Close()
			pgTypeFamilyRow(t, p, b, r)
			pgTypeFamilyRow(t, p, b, pgTypeDynamicArrayGolden(r, array, "_time_stamp"))
		})
		t.Run("user_families", func(t *testing.T) {
			for _, q := range []string{
				"CREATE TYPE pt18_mood AS ENUM('calm','busy')",
				"CREATE DOMAIN pt18_number AS numeric(5,2)",
				"CREATE DOMAIN pt18_nested AS pt18_number",
				"CREATE DOMAIN pt18_text AS text COLLATE \"C\"",
				"CREATE DOMAIN pt18_name AS name",
				"CREATE DOMAIN pt18_matrix AS integer[][]",
				"CREATE TYPE pt18_pair AS (n integer,label text)",
				"CREATE TABLE pt18_table(n integer,m pt18_mood)",
				"CREATE VIEW pt18_view AS SELECT n FROM pt18_table",
				"CREATE MATERIALIZED VIEW pt18_mat AS SELECT n FROM pt18_table WITH NO DATA",
				"CREATE TABLE pt18_partition(n integer) PARTITION BY RANGE(n)",
				"CREATE FOREIGN DATA WRAPPER pt18_fdw NO HANDLER NO VALIDATOR",
				"CREATE SERVER pt18_server FOREIGN DATA WRAPPER pt18_fdw",
				"CREATE FOREIGN TABLE pt18_foreign(n integer) SERVER pt18_server",
			} {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
			}
			defer func() {
				// Wrappers/servers outlive public-schema reset; remove actual owned objects.
				for _, q := range []string{"DROP FOREIGN TABLE pt18_foreign", "DROP SERVER pt18_server", "DROP FOREIGN DATA WRAPPER pt18_fdw"} {
					if _, err := b.DB.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			var owner, namespace int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname=current_user").Scan(&owner); err != nil {
				t.Fatal(err)
			}
			// The fixture recreates public; its namespace OID is not bootstrap2200.
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='public'").Scan(&namespace); err != nil {
				t.Fatal(err)
			}
			enumID := pgTypeID(t, p, b, "pt18_mood")
			enumArray := pgTypeID(t, p, b, "pt18_mood[]")
			enum := []any{enumID, "pt18_mood", namespace, owner, int64(4), true, "e", "E", false, true, ",", int64(0), int64(0), int64(0), enumArray, int64(3506), int64(3507), int64(3532), int64(3533), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil}
			t.Run("enum", func(t *testing.T) {
				pgTypeFamilyRow(t, p, b, enum)
				pgTypeFamilyRow(t, p, b, pgTypeDynamicArrayGolden(enum, enumArray, "_pt18_mood"))
			})
			t.Run("raw_acl", func(t *testing.T) {
				var ownerName string
				if err := b.DB.QueryRowContext(p.Ctx, "SELECT rolname FROM pg_roles WHERE oid=$1", owner).Scan(&ownerName); err != nil {
					t.Fatal(err)
				}
				for _, c := range []struct{ q, acl string }{
					{"REVOKE ALL ON TYPE pt18_mood FROM PUBLIC", "{" + ownerName + "=U/" + ownerName + "}"},
					{"REVOKE ALL ON TYPE pt18_mood FROM CURRENT_USER", "{}"},
					{"GRANT USAGE ON TYPE pt18_mood TO PUBLIC", "{=U/" + ownerName + "}"},
				} {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(c.q, 0))
					r := append([]any(nil), enum...)
					r[31] = c.acl
					pgTypeFamilyRow(t, p, b, r)
					s := fixture.Select("SELECT typacl FROM pg_type WHERE oid=$1", []string{"typacl"}, []string{"_ACLITEM"}, [][]any{{c.acl}}, enumID)
					fixture.PgDescriptionRows(t, p, b, s)
					fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
				}
			})
			var number []any
			for _, d := range []struct {
				name                 string
				base                 int64
				modifier, dimensions int64
				collation            int64
			}{
				{"pt18_number", 1700, 327686, 0, 0}, {"pt18_nested", 0, -1, 0, 0},
				{"pt18_text", 25, -1, 0, 950}, {"pt18_name", 19, -1, 0, 950}, {"pt18_matrix", 1007, -1, 2, 0},
			} {
				base := number
				baseID := d.base
				if baseID != 0 {
					base = pgTypeGolden(baseID)
				} else {
					baseID = number[0].(int64)
				}
				id, array := pgTypeID(t, p, b, d.name), pgTypeID(t, p, b, d.name+"[]")
				r := pgTypeDomainGolden(base, id, d.name, owner, namespace, array, baseID, d.modifier, d.dimensions)
				r[28] = d.collation
				if d.name == "pt18_number" {
					number = r
				}
				t.Run(d.name, func(t *testing.T) {
					pgTypeFamilyRow(t, p, b, r)
					pgTypeFamilyRow(t, p, b, pgTypeDynamicArrayGolden(r, array, "_"+d.name))
				})
			}
			for _, name := range []string{"pt18_pair", "pt18_table", "pt18_view", "pt18_mat", "pt18_partition", "pt18_foreign"} {
				t.Run(name, func(t *testing.T) {
					id, array := pgTypeID(t, p, b, name), pgTypeID(t, p, b, name+"[]")
					var relation int64
					if err := b.DB.QueryRowContext(p.Ctx, "SELECT $1::regclass::oid", name).Scan(&relation); err != nil {
						t.Fatal(err)
					}
					r := []any{id, name, namespace, owner, int64(-1), false, "c", "C", false, true, ",", relation, int64(0), int64(0), array, int64(2290), int64(2291), int64(2402), int64(2403), int64(0), int64(0), int64(0), "d", "x", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil}
					if id == array || id == relation || array == relation {
						t.Fatal("aliased type/array/relation identities", id, array, relation)
					}
					pgTypeFamilyRow(t, p, b, r)
					pgTypeFamilyRow(t, p, b, pgTypeDynamicArrayGolden(r, array, "_"+name))
					fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT reltype FROM pg_class WHERE oid=$1", []string{"reltype"}, []string{"OID"}, [][]any{{id}}, relation))
				})
			}
		})
		t.Run("installed_query_int", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE EXTENSION intarray", 0))
			relocatedSchema := false
			defer func() {
				if _, err := b.DB.ExecContext(p.Ctx, "DROP EXTENSION intarray"); err != nil {
					t.Error(err)
				}
				if relocatedSchema {
					cleanup := "DROP SCHEMA pt18_qi_relocated"
					if !b.PG {
						cleanup = "ALTER SCHEMA pt18_qi_relocated RENAME TO public"
					}
					if _, err := b.DB.ExecContext(p.Ctx, cleanup); err != nil {
						t.Error(err)
					}
				}
			}()
			id, array := pgTypeID(t, p, b, "query_int"), pgTypeID(t, p, b, "query_int[]")
			var owner, namespace, input, output int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname=current_user").Scan(&owner); err != nil {
				t.Fatal(err)
			}
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='public'").Scan(&namespace); err != nil {
				t.Fatal(err)
			}
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT 'bqarr_in'::regproc::oid,'bqarr_out'::regproc::oid").Scan(&input, &output); err != nil {
				t.Fatal(err)
			}
			r := []any{id, "query_int", namespace, owner, int64(-1), false, "b", "U", false, true, ",", int64(0), int64(0), int64(0), array, input, output, int64(0), int64(0), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil}
			pgTypeFamilyRow(t, p, b, r)
			pgTypeFamilyRow(t, p, b, pgTypeDynamicArrayGolden(r, array, "_query_int"))
			if b.PG {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE SCHEMA pt18_qi_relocated", 0))
				relocatedSchema = true
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("ALTER EXTENSION intarray SET SCHEMA pt18_qi_relocated", 0))
			} else {
				// Extension relocation remains outside this part; schema rename
				// is the supported native installed-type namespace route.
				for _, q := range []string{"ALTER EXTENSION intarray SET SCHEMA pt18_qi_relocated"} {
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported", Position: "1"})
						s.Prepared, s.ErrorStage = prepared, "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
						pgTypeFamilyRow(t, p, b, r)
						pgTypeFamilyRow(t, p, b, pgTypeDynamicArrayGolden(r, array, "_query_int"))
					}
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("ALTER SCHEMA public RENAME TO pt18_qi_relocated", 0))
				relocatedSchema = true
			}
			var relocatedNamespace int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='pt18_qi_relocated'").Scan(&relocatedNamespace); err != nil {
				t.Fatal(err)
			}
			if !b.PG && relocatedNamespace != namespace {
				t.Fatal("schema rename reminted namespace identity", namespace, relocatedNamespace)
			}
			if pgTypeID(t, p, b, "pt18_qi_relocated.query_int") != id || pgTypeID(t, p, b, "pt18_qi_relocated.query_int[]") != array {
				t.Fatal("extension relocation reminted type identities")
			}
			r[2] = relocatedNamespace
			pgTypeFamilyRow(t, p, b, r)
			pgTypeFamilyRow(t, p, b, pgTypeDynamicArrayGolden(r, array, "_query_int"))
			retained, err := b.DB.QueryContext(p.Ctx, "SELECT oid FROM pg_type WHERE oid=$1::oid", id)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP EXTENSION intarray", 0))
			fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT oid FROM pg_type WHERE oid=$1::oid OR oid=$2::oid", []string{"oid"}, []string{"OID"}, nil, id, array))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE EXTENSION intarray WITH SCHEMA pt18_qi_relocated", 0))
			next, nextArray := pgTypeID(t, p, b, "pt18_qi_relocated.query_int"), pgTypeID(t, p, b, "pt18_qi_relocated.query_int[]")
			// SQL DROP retires genuine identities without replacing the catalog.
			if next == nextArray || next == id || nextArray == array {
				t.Fatal("reinstall reused retired type identity", next, nextArray, id, array)
			}
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='pt18_qi_relocated'").Scan(&namespace); err != nil {
				t.Fatal(err)
			}
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT 'pt18_qi_relocated.bqarr_in'::regproc::oid,'pt18_qi_relocated.bqarr_out'::regproc::oid").Scan(&input, &output); err != nil {
				t.Fatal(err)
			}
			r[0], r[2], r[14], r[15], r[16] = next, namespace, nextArray, input, output
			pgTypeFamilyRow(t, p, b, r)
			pgTypeFamilyRow(t, p, b, pgTypeDynamicArrayGolden(r, nextArray, "_query_int"))
			if !retained.Next() {
				t.Fatal("retained installed-type snapshot disappeared", retained.Err())
			}
			var old int64
			if err := retained.Scan(&old); err != nil {
				t.Fatal(err)
			}
			if old != id || retained.Next() || retained.Err() != nil {
				t.Fatal("retained installed-type snapshot changed", old, id, retained.Err())
			}
		})
	})
}
