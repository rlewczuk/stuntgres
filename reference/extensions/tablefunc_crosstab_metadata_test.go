package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func crosstabInventory(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, schema string) {
	t.Helper()
	run := func(name string, s fixture.Step) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("inventory query %q args %#v prepared=%v", s.Query, s.Args, s.Prepared)
				}
			})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
	q := "SELECT p.proname,p.pronargs,p.prorettype::regtype::text AS result,p.provolatile,p.proisstrict,p.proretset,p.proparallel,p.proargnames::text AS names FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='" + schema + "' AND (p.proname IN ('crosstab2','crosstab3','crosstab4') OR (p.proname='crosstab' AND p.oid::regprocedure::text IN ('" + schema + ".crosstab(text)','" + schema + ".crosstab(text,integer)')))"
	// regprocedure output omits visible schema names. Force an unrelated path.
	run("search_path/catalog", fixture.Exec("SET search_path TO pg_catalog", 0))
	s := fixture.Select(q, []string{"proname", "pronargs", "result", "provolatile", "proisstrict", "proretset", "proparallel", "names"}, []string{"NAME", "INT2", "TEXT", "CHAR", "BOOL", "BOOL", "CHAR", "TEXT"}, [][]any{
		{"crosstab", int64(1), "record", "s", true, true, "u", nil},
		{"crosstab", int64(2), "record", "s", true, true, "u", nil},
		{"crosstab2", int64(1), schema + ".tablefunc_crosstab_2", "s", true, true, "u", nil},
		{"crosstab3", int64(1), schema + ".tablefunc_crosstab_3", "s", true, true, "u", nil},
		{"crosstab4", int64(1), schema + ".tablefunc_crosstab_4", "s", true, true, "u", nil},
	})
	s.Bag = true
	run("procedures/direct", s)
	run("procedures/prepared", fixture.Prepared(s))
	var ext int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_extension WHERE extname='tablefunc'").Scan(&ext); err != nil || ext <= 0 {
		t.Fatal("extension identity", ext, err)
	}
	seen := map[int64]bool{}
	for _, signature := range []string{"crosstab(text)", "crosstab(text,integer)", "crosstab2(text)", "crosstab3(text)", "crosstab4(text)"} {
		var proc int64
		if err := c.QueryRowContext(p.Ctx, "SELECT $1::regprocedure::oid::bigint", schema+"."+signature).Scan(&proc); err != nil || proc <= 0 || seen[proc] {
			t.Fatal("procedure identity", signature, proc, err)
		}
		seen[proc] = true
		run("procedure/"+signature+"/extension_e", fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid=1255 AND objid=$1 AND objsubid=0 AND refclassid=3079 AND refobjid=$2 AND refobjsubid=0 AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, proc, ext))
	}
	seen = map[int64]bool{}
	for n := 2; n <= 4; n++ {
		name := fmt.Sprintf("tablefunc_crosstab_%d", n)
		var typ, backing, array int64
		if err := c.QueryRowContext(p.Ctx, "SELECT $1::regtype::oid::bigint", schema+"."+name).Scan(&typ); err != nil || typ <= 0 || seen[typ] {
			t.Fatal("named type identity", typ, err)
		}
		seen[typ] = true
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname=$1 AND relnamespace=$2::regnamespace", name, schema).Scan(&backing); err != nil || backing <= 0 {
			t.Fatal("backing identity", name, backing, err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT objid FROM pg_depend WHERE classid=1247 AND refclassid=1247 AND refobjid=$1 AND deptype='i'", typ).Scan(&array); err != nil || array <= 0 || array == typ {
			t.Fatal("array identity", name, array, err)
		}
		for _, edge := range []struct {
			name                         string
			class, object, refclass, ref int64
			kind                         string
			count                        int64
		}{
			{"type/extension_e", 1247, typ, 3079, ext, "e", 1},
			{"backing/type_i", 1259, backing, 1247, typ, "i", 1},
			{"array/type_i", 1247, array, 1247, typ, "i", 1},
			{"backing/extension_e", 1259, backing, 3079, ext, "e", 0},
			{"array/extension_e", 1247, array, 3079, ext, "e", 1},
		} {
			// REL_18_6 pg_type.c GenerateTypeDependencies reverses the
			// standalone composite's internal edge and records array membership.
			// heap.c heap_create_with_catalog excludes composite backing members.
			run(name+"/"+edge.name, fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid=$1 AND objid=$2 AND objsubid=0 AND refclassid=$3 AND refobjid=$4 AND refobjsubid=0 AND deptype=$5", []string{"n"}, []string{"INT8"}, [][]any{{edge.count}}, edge.class, edge.object, edge.refclass, edge.ref, edge.kind))
		}
		columns := []string{"row_name"}
		types := []string{"TEXT"}
		for i := 1; i <= n; i++ {
			columns = append(columns, fmt.Sprintf("category_%d", i))
			types = append(types, "TEXT")
		}
		step := fixture.Select(fmt.Sprintf("SELECT * FROM %s.crosstab%d(NULL::text)", schema, n), columns, types, nil)
		step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			meta, err := rows.ColumnTypes()
			if err != nil || len(meta) != len(columns) {
				t.Fatal("composite descriptor", meta, err)
			}
			for _, col := range meta {
				if col.ScanType() != reflect.TypeFor[string]() {
					t.Error("TEXT ScanType", col.ScanType())
				}
				if _, _, known := col.DecimalSize(); known {
					t.Error("TEXT decimal metadata")
				}
			}
		}
		run(name+"/descriptor/direct", step)
		run(name+"/descriptor/prepared", fixture.Prepared(step))
		if b.PG {
			run(name+"/reciprocal_links", fixture.Select("SELECT t.typrelid::bigint AS relation,t.typarray::bigint AS array,c.reltype::bigint AS type,a.typelem::bigint AS element FROM pg_type t JOIN pg_class c ON c.oid=t.typrelid JOIN pg_type a ON a.oid=t.typarray WHERE t.oid=$1", []string{"relation", "array", "type", "element"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{backing, array, typ, typ}}, typ))
		}
	}
	if !b.PG {
		// Ten supported procedures plus three named types and their implicit arrays.
		run("members/type_and_procedure_count", fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid IN (1247,1255) AND refclassid=3079 AND refobjid=$1 AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(16)}}, ext))
		run("members/total_count", fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE refclassid=3079 AND refobjid=$1 AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(16)}}, ext))
	}
	run("search_path/public", fixture.Exec("SET search_path TO public", 0))
}

func TestReferenceTablefuncCrosstabMetadata(t *testing.T) {
	crosstabCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		d := fixture.ControlsReferenceDiagnostic("42883", "function crosstab2(text) does not exist")
		d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		d.Position = fixture.Position(15)
		crosstabFailure(t, p, b, c, "SELECT * FROM crosstab2(NULL::text)", d, "prepare")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT name,version,relocatable,trusted FROM pg_available_extension_versions WHERE name='tablefunc'", []string{"name", "version", "relocatable", "trusted"}, []string{"NAME", "TEXT", "BOOL", "BOOL"}, [][]any{{"tablefunc", "1.0", true, true}}))
		crosstabSeed(t, p, b, c)
		crosstabInventory(t, p, b, c, "public")
	})
}
