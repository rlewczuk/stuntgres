package core_test

import (
	"strings"
	"testing"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestPGStatUserFunctionsCatalogIdentity(t *testing.T) {
	e := engine.New(); read := e.CatalogRead()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatUserFunctionsOID}
	v := read.MustRelation(a)
	if a.ObjectID != 12279 || v.RowType != 12281 || !v.VirtualView || len(v.Columns) != 6 { t.Fatal("view identity", v) }
	names := strings.Fields("funcid schemaname funcname calls total_time self_time")
	kinds := []sqltype.ID{sqltype.Oid, sqltype.Name, sqltype.Name, sqltype.Int8, sqltype.Float8, sqltype.Float8}
	for i, c := range v.Columns { if c.Name != names[i] || c.Usage.Type.ID() != kinds[i] || c.Usage.Typmod != -1 || !c.Nullable || !c.NullabilityKnown || c.OriginTable != 12279 || c.OriginAttribute != int16(i+1) { t.Fatal("typed origin", i, c) } }
	row, ok := read.LookupType(sqltype.ID(12281)); if !ok || row.AssociatedArray() != sqltype.ID(12280) { t.Fatal("row/array", row) }
	n := 0; for _, spec := range pgcatalog.Specs { if spec.Name == "pg_stat_user_functions" { n++; if spec.OID != 12279 { t.Fatal(spec) } } }; if n != 1 { t.Fatal("duplicate source", n) }
	for _, oid := range []ids.OID{pgcatalog.StatReplicationSlotsOID, pgcatalog.StatUserFunctionsOID, pgcatalog.StatXactUserFunctionsOID, pgcatalog.StatArchiverOID} {
		if produce, ok := pgcatalog.ProducerFor(oid); !ok || produce == nil { t.Fatal("function statistics neighborhood is not executable", oid) }
	}
	def := pgcatalog.SummaryViewDefinition(pgcatalog.StatUserFunctionsOID)
	for _, term := range []string{"LEFT JOIN pg_namespace", "pg_stat_get_function_calls(p.oid)", "IS NOT NULL", "prolang"} { if !strings.Contains(def, term) { t.Fatal("released definition", term, def) } }
}