package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGAttributeConstraintAttributes(t *testing.T) {
	pgAttributeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		id := pgAttributeID(t, p, b.DB, "pg_catalog.pg_constraint")
		if id != 2606 {
			t.Fatal("released constraint identity", id)
		}
		want := [][]any{
			pgAttributeRow(id, "oid", 26, 4, 1, -1, 0, true, "i", "p", true, 0),
			pgAttributeRow(id, "conname", 19, 64, 2, -1, 0, false, "c", "p", true, 950),
			pgAttributeRow(id, "connamespace", 26, 4, 3, -1, 0, true, "i", "p", true, 0),
			pgAttributeRow(id, "contype", 18, 1, 4, -1, 0, true, "c", "p", true, 0),
			pgAttributeRow(id, "condeferrable", 16, 1, 5, -1, 0, true, "c", "p", true, 0),
			pgAttributeRow(id, "condeferred", 16, 1, 6, -1, 0, true, "c", "p", true, 0),
			pgAttributeRow(id, "conenforced", 16, 1, 7, -1, 0, true, "c", "p", true, 0),
			pgAttributeRow(id, "convalidated", 16, 1, 8, -1, 0, true, "c", "p", true, 0),
			pgAttributeRow(id, "conrelid", 26, 4, 9, -1, 0, true, "i", "p", true, 0),
			pgAttributeRow(id, "contypid", 26, 4, 10, -1, 0, true, "i", "p", true, 0),
			pgAttributeRow(id, "conindid", 26, 4, 11, -1, 0, true, "i", "p", true, 0),
			pgAttributeRow(id, "conparentid", 26, 4, 12, -1, 0, true, "i", "p", true, 0),
			pgAttributeRow(id, "confrelid", 26, 4, 13, -1, 0, true, "i", "p", true, 0),
			pgAttributeRow(id, "confupdtype", 18, 1, 14, -1, 0, true, "c", "p", true, 0),
			pgAttributeRow(id, "confdeltype", 18, 1, 15, -1, 0, true, "c", "p", true, 0),
			pgAttributeRow(id, "confmatchtype", 18, 1, 16, -1, 0, true, "c", "p", true, 0),
			pgAttributeRow(id, "conislocal", 16, 1, 17, -1, 0, true, "c", "p", true, 0),
			pgAttributeRow(id, "coninhcount", 21, 2, 18, -1, 0, true, "s", "p", true, 0),
			pgAttributeRow(id, "connoinherit", 16, 1, 19, -1, 0, true, "c", "p", true, 0),
			pgAttributeRow(id, "conperiod", 16, 1, 20, -1, 0, true, "c", "p", true, 0),
			pgAttributeRow(id, "conkey", 1005, -1, 21, -1, 1, false, "i", "x", false, 0),
			pgAttributeRow(id, "confkey", 1005, -1, 22, -1, 1, false, "i", "x", false, 0),
			pgAttributeRow(id, "conpfeqop", 1028, -1, 23, -1, 1, false, "i", "x", false, 0),
			pgAttributeRow(id, "conppeqop", 1028, -1, 24, -1, 1, false, "i", "x", false, 0),
			pgAttributeRow(id, "conffeqop", 1028, -1, 25, -1, 1, false, "i", "x", false, 0),
			pgAttributeRow(id, "confdelsetcols", 1005, -1, 26, -1, 1, false, "i", "x", false, 0),
			pgAttributeRow(id, "conexclop", 1028, -1, 27, -1, 1, false, "i", "x", false, 0),
			pgAttributeRow(id, "conbin", 194, -1, 28, -1, 0, false, "i", "x", false, 950),
		}
		pgAttributeRead(t, p, b, b.DB, id, " AND attnum>0", want)
	})
}
