package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGProcShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			const q = "SELECT * FROM pg_catalog.pg_proc WHERE oid=$1::oid"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			for _, arg := range []any{nil, int64(0), int64(1397), int64(0)} {
				var rows [][]any
				if arg == int64(1397) {
					rows = [][]any{pgProcAbsRow()}
				}
				s := pgProcShapeStep(b, q, rows, arg)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			for _, empty := range []string{"SELECT * FROM pg_proc WHERE FALSE", "SELECT * FROM pg_proc LIMIT 0"} {
				s := pgProcShapeStep(b, empty, nil)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
			}
			retained, err := b.DB.QueryContext(p.Ctx, q, int64(1397))
			if err != nil {
				t.Fatal(err)
			}
			descriptors, err := retained.ColumnTypes()
			if err != nil {
				retained.Close()
				t.Fatal(err)
			}
			pgProcShape(t, b, retained)
			if err := retained.Close(); err != nil {
				t.Fatal(err)
			}
			for i, descriptor := range descriptors {
				if descriptor.Name() != pgProcColumns[i] || descriptor.DatabaseTypeName() != pgProcTypes[i] {
					t.Fatalf("descriptor changed after Close at %d", i)
				}
			}
			pgProcBoth(t, p, b, b.DB, "SELECT oid,reltype,relnatts FROM pg_class WHERE oid='pg_proc'::regclass", []string{"oid", "reltype", "relnatts"}, []string{"OID", "OID", "INT2"}, [][]any{{int64(1255), int64(81), int64(30)}})
			var attrs [][]any
			for i, name := range pgProcColumns {
				dims, coll := int64(0), int64(0)
				if i == 19 || i == 20 || i == 21 || i == 22 || i == 24 || i == 28 || i == 29 {
					dims = 1
				}
				if i == 1 || i == 22 || i == 23 || i == 25 || i == 26 || i == 27 || i == 28 {
					coll = 950
				}
				attrs = append(attrs, []any{name, int64(i + 1), pgProcTypeOIDs[i], int64(-1), !pgProcNullable(i), dims, coll})
			}
			pgProcBoth(t, p, b, b.DB, "SELECT attname,attnum,atttypid,atttypmod,attnotnull,attndims,attcollation FROM pg_attribute WHERE attrelid='pg_proc'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "attnum", "atttypid", "atttypmod", "attnotnull", "attndims", "attcollation"}, []string{"NAME", "INT2", "OID", "INT4", "BOOL", "INT2", "OID"}, attrs)
		})
	}
}
