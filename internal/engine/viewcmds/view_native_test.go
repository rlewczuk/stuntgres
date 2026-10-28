package viewcmds_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestViewNativeMixedOwnerReplay(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{
		"CREATE USER view_owner", "CREATE USER schema_owner", "CREATE SCHEMA s AUTHORIZATION view_owner",
		"CREATE TABLE public.denied(id INT)", "INSERT INTO public.denied VALUES(9)",
		"SET SESSION AUTHORIZATION view_owner", "CREATE TABLE s.t(id INT)", "INSERT INTO s.t VALUES(7)",
		"CREATE VIEW s.v WITH(security_barrier=true) AS SELECT id FROM s.t", "CREATE VIEW s.w AS SELECT id FROM s.v",
		"CREATE VIEW s.denied AS SELECT id FROM public.denied",
		"RESET SESSION AUTHORIZATION", "ALTER SCHEMA s OWNER TO schema_owner",
	} {
		enginetest.RoleExecute(t, e, op, query)
	}
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if err := engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("mixed-owner view replay", err, dump.String())
	}
	if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT id FROM s.w")); len(got) != 1 || got[0][0] != int64(7) {
		t.Fatal(got)
	}
	owner, _ := e.Roles().Lookup("view_owner")
	for _, name := range []string{"s.v", "s.w"} {
		ref := enginetest.RoleTableRef(t, e, name)
		descriptor := e.CatalogRead().MustRelation(ref.Address)
		if descriptor.Owner != owner.Ref {
			t.Fatal("view owner remap", name, descriptor.Owner)
		}
		for _, leaf := range stored.ViewOf(descriptor).Sources() {
			if leaf.Ref.Domain() != e.Catalog.Domain || leaf.Ref.Epoch != e.Catalog.State.Epoch() {
				t.Fatal("view identity remap", name, leaf.Ref)
			}
		}
	}
	_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, "SELECT id FROM s.denied"), nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for table denied" {
		t.Fatal("restore cached bootstrap read authority", err)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION view_owner")
	for _, query := range []string{"CREATE VIEW s.unmarked AS SELECT id FROM public.denied", "CREATE VIEW public.unmarked AS SELECT id FROM s.t"} {
		_, err := e.Execute(op, enginetest.Statement(t, query), 0, nil)
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" {
			t.Fatal("unmarked CREATE privilege widening", query, err)
		}
	}
}

func TestViewNativeRetainedTemporaryTypes(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE TEMPORARY SEQUENCE retained")
	oldInt8 := e.Catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 20}).SQLType
	oldBool := e.Catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 16}).SQLType
	const script = "CREATE TABLE t(id BIGINT,flag BOOL); INSERT INTO t VALUES(7,true); CREATE VIEW v(k,flag) AS SELECT COALESCE(id,0),CASE WHEN flag THEN true ELSE false END FROM t WHERE flag ORDER BY id; CREATE VIEW w(k,n) AS SELECT k,COUNT(*) FROM v GROUP BY k HAVING COUNT(*)>0;"
	if err := engine.Restore(context.Background(), e, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT k,n FROM w")); len(got) != 1 || got[0][0] != int64(7) || got[0][1] != int64(1) {
		t.Fatal(got)
	}
	ref := enginetest.RoleTableRef(t, e, "v")
	definition := stored.ViewOf(e.Catalog.State.MustRelation(ref.Address))
	if definition.Query().Root.Schema()[0].Usage.Type != oldInt8 || definition.Query().Root.Schema()[1].Usage.Type != oldBool {
		t.Fatal("view retained type generation")
	}
	for _, dependency := range definition.Dependencies() {
		if dependency.Ref.Domain() != e.Catalog.Domain || dependency.Ref.Epoch != e.Catalog.State.Epoch() {
			t.Fatal("view dependency generation", dependency)
		}
	}
}

func TestViewProviderAcceptsCyclicSources(t *testing.T) {
	e := engine.New()
	for _, query := range []string{"CREATE TABLE t(id INT)", "CREATE VIEW v AS SELECT id FROM t", "CREATE VIEW w AS SELECT id FROM v"} {
		enginetest.Run(t, e, query)
	}
	base := e.CatalogRead()
	vref := enginetest.RoleTableRef(t, e, "v")
	wref := enginetest.RoleTableRef(t, e, "w")
	old := base.MustRelation(vref.Address)
	replacement := *old
	replacement.ObjectRevision++
	definition := stored.ViewOf(old).Parts()
	definition.Sources = append([]stored.ViewLeaf(nil), definition.Sources...)
	previous := definition.Sources[0].Ref
	definition.Sources[0].Ref = wref
	root, err := stored.RemapViewQuery(engine.New().CatalogRead(), definition.Query.Root, func(ref ids.ObjectRef) ids.ObjectRef {
		if ref.Address.ClassID == ids.RelationClassID && ref.Address.ObjectID == previous.Address.ObjectID {
			ref.Address.ObjectID = wref.Address.ObjectID
		}
		return ref
	}, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ })
	if err != nil {
		t.Fatal(err)
	}
	query := *definition.Query
	query.Root = root
	definition.Query = &query
	replacement.View = stored.NewView(definition)
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err != nil {
		t.Fatal("cyclic view source rejected", err)
	}
	if e.CatalogRead().State() != base.State() {
		t.Fatal("validation published an uncommitted stage")
	}
}

func TestViewRuleOIDUniquenessAfterRestore(t *testing.T) {
	source := engine.New()
	enginetest.Run(t, source, "CREATE TABLE t(id INT)")
	enginetest.Run(t, source, "CREATE VIEW v AS SELECT id FROM t")
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), source, &dump); err != nil {
		t.Fatal(err)
	}
	for _, temporary := range []bool{false, true} {
		name := "plain"
		if temporary {
			name = "retained-temp"
		}
		t.Run(name, func(t *testing.T) {
			target := engine.New()
			operation, done := enginetest.RoleOperation(t, target, "postgres")
			defer done()
			if temporary {
				enginetest.RoleExecute(t, target, operation, "CREATE TEMPORARY SEQUENCE retained")
			}
			if err := engine.Restore(context.Background(), target, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			enginetest.Run(t, target, "CREATE TABLE fresh(id INT)")
			readOids := func(query string) []uint32 {
				result := enginetest.Run(t, target, query)
				defer result.Rows.Close()
				if result.Rows.Schema()[0].Usage.Type.ID() != sqltype.Oid {
					t.Fatal("incorrect OID metadata")
				}
				var ids []uint32
				values := make([]sqltype.Value, 1)
				for {
					present, err := result.Rows.Next(context.Background(), values)
					if err != nil {
						t.Fatal(err)
					}
					if !present {
						break
					}
					oid, err := values[0].OID()
					if err != nil {
						t.Fatal(err)
					}
					ids = append(ids, oid)
				}
				return ids
			}
			classes := readOids("SELECT oid FROM pg_catalog.pg_class")
			rules := readOids("SELECT oid FROM pg_catalog.pg_rewrite")
			userRules := readOids("SELECT oid FROM pg_catalog.pg_rewrite WHERE ev_class='public.v'::regclass AND rulename='_RETURN'")
			bootstrapRules := readOids("SELECT oid FROM pg_catalog.pg_rewrite WHERE ev_class='pg_catalog.pg_stat_progress_copy'::regclass AND rulename='_RETURN'")
			if len(userRules) != 1 || len(bootstrapRules) != 1 || bootstrapRules[0] != uint32(pgcatalog.ProgressCopyRuleOID) || len(rules) != 2 {
				t.Fatal("restored and bootstrap rule inventory", rules, userRules, bootstrapRules)
			}
			seen := make(map[uint32]bool, len(classes))
			for _, oid := range classes {
				seen[oid] = true
			}
			for _, oid := range rules {
				if oid <= 0 || seen[oid] {
					t.Fatal("rule OID reused", oid, classes)
				}
				seen[oid] = true
			}
		})
	}
}
