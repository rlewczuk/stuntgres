package writes_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestStatisticsExpressionCollectionEqualityOnlyProjection(t *testing.T) {
	e, s, _ := expressionCollectionFixture(t)
	for _, statement := range []string{
		"CREATE TABLE expr_xid(w xid)",
		"INSERT INTO expr_xid VALUES ('1'),('1'),('2'),('2')",
		"CREATE STATISTICS expr_xid_s ON (coalesce(w,w)) FROM expr_xid",
		"ANALYZE expr_xid",
	} {
		enginetest.SessionSQL(t, s, statement)
	}
	ref, _ := vacuumStore(t, e, "expr_xid")
	key := ndistinctKey(t, e, "expr_xid_s")
	facts := analyzeFacts(e, ref.Address)
	fact := facts.Extended[key]
	if fact == nil || fact.ExpressionLen() != 1 || len(fact.Expression(0).MCV) != 2 || len(fact.Expression(0).Histogram) != 0 {
		t.Fatal("equality-only collector did not retain real MCV facts", fact)
	}
	var captured sqltype.Value
	statisticExtDataInput(t, e, func(in pgcatalog.Input, _ core.AuthorizationView) {
		xid, ok := in.Catalog.LookupType(sqltype.Xid)
		if !ok {
			t.Fatal("missing authentic xid type")
		}
		if _, found, err := in.Catalog.DefaultIndexClass(403, xid); err != nil || found {
			t.Fatal("fixture unexpectedly has xid ordering", found, err)
		}
		hash, found, err := in.Catalog.DefaultIndexClass(405, xid)
		if err != nil || !found {
			t.Fatal("missing authentic xid equality class", err)
		}
		member, found := in.Catalog.FamilyOperator(ids.OID(hash.FamilyOID()), ids.OID(hash.InputOID()), ids.OID(hash.InputOID()), 1, 's')
		if !found || member.Operator != 352 {
			t.Fatal("wrong released xid equality authority", member)
		}
		seen := make(map[ids.OID]bool)
		err = in.Runtime.VisitExtendedStatistics(in.Context, func(oid ids.OID, inherited bool, joint, dependencies, mcv, expressions sqltype.Value) error {
			if inherited || seen[oid] {
				t.Fatal("unexpected local projection identity", oid, inherited)
			}
			seen[oid] = true
			if oid != key.Definition.Address.ObjectID {
				return nil
			}
			if !joint.IsNull() || !mcv.IsNull() || !mcv.ValidResult(in.Source.Columns[4].Usage) || expressions.ArrayLen() != 1 || !expressions.ValidResult(in.Source.Columns[5].Usage) {
				t.Fatal("invalid equality-only typed projection")
			}
			captured = expressions
			return nil
		})
		if err != nil || len(seen) != 3 || captured.Type() == nil {
			t.Fatal("equality-only object obstructed unrelated catalog data", len(seen), err)
		}
	})
	row := captured.ArrayElement(0)
	kind, err := row.CompositeField(6).Integer()
	operator, opErr := row.CompositeField(11).OID()
	values := row.CompositeField(26)
	usage, ok := values.StatisticArrayUsage()
	if err != nil || opErr != nil || kind != 1 || operator != 352 || !ok || usage != fact.Expression(0).Usage || usage.Type.ID() != sqltype.Xid || values.ArrayElement(0).Type() != usage.Type || values.ArrayLen() != 2 {
		t.Fatal("MCV did not preserve genuine xid values and equality operator", kind, operator, usage, err, opErr)
	}
	seenValues := make(map[string]bool)
	for i := range values.ArrayLen() {
		value, err := values.ArrayElement(i).Type().Output(context.Background(), values.ArrayElement(i))
		if err != nil || value != "1" && value != "2" {
			t.Fatal("unexpected concrete xid MCV", value, err)
		}
		if seenValues[value] {
			t.Fatal("duplicate xid MCV", value)
		}
		seenValues[value] = true
		frequency, err := row.CompositeField(21).ArrayElement(i).Float()
		if err != nil || frequency != 0.5 {
			t.Fatal("wrong equality-only MCV frequency", frequency, err)
		}
	}
	enginetest.SessionSQL(t, s, "INSERT INTO expr_xid VALUES ('3'),('3')")
	enginetest.SessionSQL(t, s, "ANALYZE expr_xid")
	if analyzeFacts(e, ref.Address).Extended[key] == fact || facts.Extended[key] != fact || values.ArrayLen() != 2 || row.CompositeField(26).Type().ID() != 2277 {
		t.Fatal("refresh mutated retained projection or source backing")
	}
}

func expressionCollectionFixture(t *testing.T) (*core.Engine, *session.Session, ids.ObjectRef) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE TABLE expr_owner(a INT,b INT,c INT)",
		"CREATE STATISTICS expr_a ON (a+1) FROM expr_owner",
		"CREATE STATISTICS expr_b(ndistinct) ON (b+1),c FROM expr_owner",
		"INSERT INTO expr_owner VALUES(1,10,10),(1,10,10),(2,20,20),(NULL,NULL,NULL)",
		"ANALYZE expr_owner",
	} {
		enginetest.SessionSQL(t, s, q)
	}
	ref, _ := vacuumStore(t, e, "expr_owner")
	return e, s, ref
}

func vacuumStore(t *testing.T, e *core.Engine, name string) (ids.ObjectRef, *storage.Relation) {
	t.Helper()
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), name)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	store := e.Storage.Relations[ref.Address]
	if store == nil {
		t.Fatal("missing store", name)
	}
	return ref, store
}

func ndistinctKey(t *testing.T, e *core.Engine, name string) storage.ExtendedStatisticsKey {
	t.Helper()
	for _, raw := range e.CatalogRead().Descriptors() {
		if d, ok := raw.(*catalog.Statistics); ok && d.Name() == name {
			return storage.ExtendedStatisticsKey{Definition: e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), d.Address())}
		}
	}
	t.Fatal("missing definition", name)
	return storage.ExtendedStatisticsKey{}
}

func analyzeFacts(e *core.Engine, address ids.ObjectAddress) *storage.RelationStatistics {
	if metadata := e.Storage.RelationMetadata[address]; metadata != nil {
		return metadata.Statistics
	}
	return nil
}

func statisticExtDataInput(t *testing.T, e *core.Engine, body func(pgcatalog.Input, core.AuthorizationView)) {
	t.Helper()
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
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatisticExtDataOID}
	d := auth.Catalog.MustRelation(address)
	body(pgcatalog.Input{Context: op.Context(), Catalog: auth.Catalog, Roles: auth.Roles, Owner: auth.Owner, Ref: auth.Ref(address), Source: d, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth})}, auth)
}
