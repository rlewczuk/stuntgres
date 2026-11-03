package core_test

import (
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

// The private capture seam is exported only to the core owner's test variant.
// Keep this live admission proof here; descriptor/Reader units live in enginetests.
func TestStatioSysTablesLiveCapture(t *testing.T) {
	e := engine.New()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259}
	ref := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), address)
	var writer statistics.Writer
	writer.Add(&e.Statistics, statistics.Key{Object: ref, Category: statistics.Relation}, statistics.Fact{BlocksFetched: 7, BlocksHit: 5})
	writer.Flush(&e.Statistics, time.Now(), true)
	s := enginetest.StartSession(t, e, nil)
	withStatioTableRuntime(t, e, s, func(_ pgcatalog.Runtime, in pgcatalog.Input, scope *core.StatementScope) {
		sys := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatioSysTablesOID}
		in.Ref = in.Catalog.Domain().Ref(in.Catalog.Epoch(), sys)
		in.Source = in.Catalog.MustRelation(sys)
		in.Keep = pgcatalog.Keep{Column: 0, Test: func(v sqltype.Value) bool { n, _ := v.OID(); return n == 1259 }}
		produce, _ := pgcatalog.ProducerFor(pgcatalog.StatioSysTablesOID)
		rows, err := produce(in)
		if err != nil || len(rows) != 1 { t.Fatal(rows, err) }
		for i, want := range []int64{2, 5} {
			got, err := rows[0][i+3].Integer()
			if err != nil || got != want { t.Fatal("independent nonzero heap formula", i, got, want, err) }
		}
		e.Mu.Lock()
		captured, err := core.EngineOpenCatalogSourceLocked(e, scope, in.Ref, in.Source)
		if err != nil { t.Fatal(err) }
		defer captured.Close()
		for _, row := range captured.Anonymous {
			n, _ := row[0].OID()
			if n == 1259 {
				if !reflect.DeepEqual(row, rows[0]) { t.Fatal("live capture lost shared Reader/auth", row, rows[0]) }
				return
			}
		}
		t.Fatal("live capture omitted positive system identity")
	})
}
