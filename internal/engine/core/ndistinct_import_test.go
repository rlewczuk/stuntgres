package core_test

import (
	"context"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStatisticsNDistinctDataSchema(t *testing.T) {
	e, _, _ := ndistinctCoreFixture(t)
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 3429}
	d, ok := auth.Catalog.Relation(address)
	if !ok || d.RowType != 10033 || d.Name() != "pg_statistic_ext_data" || len(d.Columns) != 6 || !pgcatalog.RegisteredSource(auth.Catalog, d) {
		t.Fatal("data source identity", d)
	}
	names := []string{"stxoid", "stxdinherit", "stxdndistinct", "stxddependencies", "stxdmcv", "stxdexpr"}
	types := []sqltype.ID{26, 16, 3361, 3402, 5017, 10028}
	for i, c := range d.Columns {
		if c.Name != names[i] || c.Usage.Type.ID() != types[i] || c.Nullable != (i >= 2) || !c.NullabilityKnown {
			t.Fatal(i, c)
		}
	}
	if d.Security.ACL.Explicit || len(d.Security.ACL.Entries) != 0 {
		t.Fatal("restricted bootstrap ACL changed", d.Security.ACL)
	}
	produce, ok := pgcatalog.ProducerFor(3429)
	if !ok {
		t.Fatal("unregistered source")
	}
	ref := auth.Ref(address)
	in := pgcatalog.Input{Context: op.Context(), Catalog: auth.Catalog, Roles: auth.Roles, Owner: auth.Owner, Ref: ref, Source: d, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth})}
	rows, err := produce(in)
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	for _, row := range rows {
		for i, value := range row {
			if !value.ValidResult(d.Columns[i].Usage) || i >= 3 && !value.IsNull() {
				t.Fatal("dishonest payload", i, value)
			}
		}
	}
	foreign := engine.New()
	in.Owner = &foreign.Catalog.Owner
	if _, err := produce(in); err == nil {
		t.Fatal("foreign catalog authority accepted")
	}
}

func TestStatisticsNDistinctImport(t *testing.T) {
	source, s, ref := ndistinctCoreFixture(t)
	enginetest.SessionSQL(t, s, "INSERT INTO nd_owner VALUES(3,3,3)")
	enginetest.SessionSQL(t, s, "ALTER STATISTICS nd_a SET STATISTICS 0")
	original := analyzeFacts(source, ref.Address)
	a := ndistinctKey(t, source, "nd_a")
	oldFact := original.Extended[a]
	var dump strings.Builder
	if err := engine.Dump(context.Background(), source, &dump); err != nil {
		t.Fatal(err)
	}
	target := engine.New()
	owner := enginetest.StartSession(t, target, nil)
	for _, q := range []string{"CREATE TABLE collision(v INT)", "INSERT INTO collision VALUES(99)", "ANALYZE collision", "CREATE TEMP TABLE nd_temp(a INT,b INT)", "CREATE STATISTICS pg_temp.nd_temp_s(ndistinct) ON a,b FROM nd_temp", "INSERT INTO nd_temp VALUES(7,7)", "ANALYZE nd_temp"} {
		enginetest.SessionSQL(t, owner, q)
	}
	var tempRef ids.ObjectRef
	for _, raw := range target.CatalogRead().Descriptors() {
		if relation, ok := raw.(*catalog.Relation); ok && relation.Name() == "nd_temp" {
			tempRef = target.Catalog.Domain.Ref(target.CatalogRead().Epoch(), relation.Address())
		}
	}
	if tempRef.Address.ObjectID == 0 {
		t.Fatal("missing TEMP relation")
	}
	tempBefore := analyzeFacts(target, tempRef.Address)
	if tempBefore == nil || len(tempBefore.Extended) != 1 {
		t.Fatal("missing genuine TEMP baseline")
	}
	var oldTempFact *storage.ExtendedStatistics
	for _, fact := range tempBefore.Extended {
		oldTempFact = fact
	}
	oldTempType := oldTempFact.Value.Type()
	baselineCatalog := target.Catalog.State
	baselineMetadata := target.Storage.RelationMetadata[tempRef.Address]
	var line string
	for _, candidate := range strings.Split(dump.String(), "\n") {
		if strings.HasPrefix(candidate, "-- stuntgres-ndistinct-v1 ") {
			line = candidate
			break
		}
	}
	if err := engine.Restore(context.Background(), target, strings.NewReader(dump.String()+line+"\n")); err == nil {
		t.Fatal("duplicate checked state installed")
	}
	if target.Catalog.State != baselineCatalog || target.Storage.RelationMetadata[tempRef.Address] != baselineMetadata {
		t.Fatal("failed import changed destination roots")
	}
	if err := engine.Restore(context.Background(), target, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	importedRef, _ := vacuumStore(t, target, "nd_owner")
	facts := analyzeFacts(target, importedRef.Address)
	key := ndistinctKey(t, target, "nd_a")
	fact := facts.Extended[key]
	if fact == nil || fact == oldFact || fact.Items[0].Value != 2 || fact.Value.Type() != target.CatalogRead().MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 3361}).SQLType || key.Definition.Domain() != target.Catalog.Domain || key.Definition.Epoch != target.CatalogRead().Epoch() {
		t.Fatal("state not reowned into destination generation", fact)
	}
	if len(facts.Columns) != 0 {
		t.Fatal("imported scalar planner distributions")
	}
	retained := analyzeFacts(target, tempRef.Address)
	if retained == nil || len(retained.Extended) != 1 || retained.Columns[storage.StatisticsKey{Attribute: 1}] != tempBefore.Columns[storage.StatisticsKey{Attribute: 1}] {
		t.Fatal("genuine destination TEMP facts lost")
	}
	for key, fact := range retained.Extended {
		if key.Definition.Epoch != target.CatalogRead().Epoch() || fact.Items[0].Value != 1 {
			t.Fatal("TEMP authority not rebound")
		}
		view := target.CatalogRead()
		definition, ok := view.Statistics(key.Definition.Address)
		if !ok || definition.Relation.Epoch != view.Epoch() || definition.Relation != fact.Relation {
			t.Fatal("retained TEMP definition kept stale relation authority")
		}
		if err := (catalog.StatisticsProvider{}).Validate(view, definition); err != nil {
			t.Fatal("retained TEMP definition invalid before session cleanup", err)
		}
		payloadType, exists := view.LookupType(sqltype.PgNDistinct)
		if !exists || fact.Value.Type() != payloadType || !fact.Value.ValidResult(payloadType.Usage()) {
			t.Fatal("retained TEMP payload kept a foreign descriptor generation")
		}
		if oldTempFact.Value.Type() != oldTempType || !oldTempFact.Value.ValidResult(oldTempType.Usage()) || oldTempFact.Items[0].Value != 1 {
			t.Fatal("replacement changed the old captured TEMP payload")
		}
	}
	enginetest.SessionSQL(t, owner, "ANALYZE nd_owner")
	if analyzeFacts(target, importedRef.Address).Extended[key] != fact {
		t.Fatal("target zero rebuilt imported stale state")
	}
	enginetest.SessionSQL(t, owner, "ALTER STATISTICS nd_a SET STATISTICS DEFAULT")
	enginetest.SessionSQL(t, owner, "ANALYZE nd_owner")
	if analyzeFacts(target, importedRef.Address).Extended[key].Items[0].Value != 3 || oldFact.Items[0].Value != 2 {
		t.Fatal("restore recomputed stale state or mutated old payload")
	}
	// Dropping a definition invalidates its own facts without touching its peer.
	enginetest.SessionSQL(t, owner, "DROP STATISTICS nd_a")
	current := analyzeFacts(target, importedRef.Address)
	if current == nil || current.Extended[key] != nil || len(current.Extended) != 1 {
		t.Fatal("definition lifecycle pruning")
	}
	retainedBeforeReset := retained
	var resetCapture *storage.ExtendedStatistics
	for _, fact := range retainedBeforeReset.Extended {
		resetCapture = fact
	}
	resetCaptureType := resetCapture.Value.Type()
	if err := engine.Reset(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	afterReset := analyzeFacts(target, tempRef.Address)
	if afterReset == nil || len(afterReset.Extended) != 1 {
		t.Fatal("Reset lost retained TEMP facts")
	}
	payloadType, exists := target.CatalogRead().LookupType(sqltype.PgNDistinct)
	for key, fact := range afterReset.Extended {
		if !exists || fact.Value.Type() != payloadType || !fact.Value.ValidResult(payloadType.Usage()) || fact.Items[0].Value != 1 || key.Definition.Epoch != target.CatalogRead().Epoch() {
			t.Fatal("Reset kept foreign-generation TEMP payload authority")
		}
	}
	if resetCapture.Value.Type() != resetCaptureType || !resetCapture.Value.ValidResult(resetCaptureType.Usage()) || resetCapture.Items[0].Value != 1 {
		t.Fatal("Reset mutated the held pre-replacement TEMP fact")
	}
}
