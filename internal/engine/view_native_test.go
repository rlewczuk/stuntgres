package engine

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestViewNativeMixedOwnerReplay(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{
		"CREATE USER view_owner", "CREATE USER schema_owner", "CREATE SCHEMA s AUTHORIZATION view_owner",
		"CREATE TABLE public.denied(id INT)", "INSERT INTO public.denied VALUES(9)",
		"SET SESSION AUTHORIZATION view_owner", "CREATE TABLE s.t(id INT)", "INSERT INTO s.t VALUES(7)",
		"CREATE VIEW s.v WITH(security_barrier=true) AS SELECT id FROM s.t", "CREATE VIEW s.w AS SELECT id FROM s.v",
		"CREATE VIEW s.denied AS SELECT id FROM public.denied",
		"RESET SESSION AUTHORIZATION", "ALTER SCHEMA s OWNER TO schema_owner",
	} {
		roleExecute(t, e, op, query)
	}
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("mixed-owner view replay", err, dump.String())
	}
	if got := tuples(run(t, e, "SELECT id FROM s.w")); len(got) != 1 || got[0][0] != int64(7) {
		t.Fatal(got)
	}
	owner, _ := e.Roles().Lookup("view_owner")
	for _, name := range []string{"s.v", "s.w"} {
		ref := roleTableRef(t, e, name)
		descriptor := e.CatalogRead().state.descriptors[ref.Address].(*relationDescriptor)
		if descriptor.owner != owner.Ref || descriptor.view.source.owner != e.owner || descriptor.view.source.Epoch != e.state.epoch {
			t.Fatal("view identity remap", name, descriptor.owner, descriptor.view.source)
		}
	}
	_, err := testExecute(e, context.Background(), statement(t, "SELECT id FROM s.denied"), nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for table denied" {
		t.Fatal("restore cached bootstrap read authority", err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION view_owner")
	for _, query := range []string{"CREATE VIEW s.unmarked AS SELECT id FROM public.denied", "CREATE VIEW public.unmarked AS SELECT id FROM s.t"} {
		_, err := e.Execute(op, statement(t, query), 0, nil)
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" {
			t.Fatal("unmarked CREATE privilege widening", query, err)
		}
	}
}

func TestViewNativeRetainedTemporaryTypes(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE TEMPORARY SEQUENCE retained")
	oldInt8 := e.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: 20}].(*typeDescriptor).typ
	oldBool := e.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: 16}].(*typeDescriptor).typ
	const script = "CREATE TABLE t(id BIGINT,flag BOOL); INSERT INTO t VALUES(7,true); CREATE VIEW v(k,flag) AS SELECT COALESCE(id,0),CASE WHEN flag THEN true ELSE false END FROM t WHERE flag ORDER BY id; CREATE VIEW w(k,n) AS SELECT k,COUNT(*) FROM v GROUP BY k HAVING COUNT(*)>0;"
	if err := e.Restore(context.Background(), strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	if got := tuples(run(t, e, "SELECT k,n FROM w")); len(got) != 1 || got[0][0] != int64(7) || got[0][1] != int64(1) {
		t.Fatal(got)
	}
	ref := roleTableRef(t, e, "v")
	definition := e.state.descriptors[ref.Address].(*relationDescriptor).view
	if definition.query.Root.Schema()[0].Usage.Type != oldInt8 || definition.query.Root.Schema()[1].Usage.Type != oldBool {
		t.Fatal("view retained type generation")
	}
	for _, dependency := range definition.dependencies {
		if dependency.ref.owner != e.owner || dependency.ref.Epoch != e.state.epoch {
			t.Fatal("view dependency generation", dependency)
		}
	}
}

func TestViewProviderRejectsCyclicSources(t *testing.T) {
	e := New()
	for _, query := range []string{"CREATE TABLE t(id INT)", "CREATE VIEW v AS SELECT id FROM t", "CREATE VIEW w AS SELECT id FROM v"} {
		run(t, e, query)
	}
	base := e.CatalogRead()
	vref := roleTableRef(t, e, "v")
	wref := roleTableRef(t, e, "w")
	old := base.state.descriptors[vref.Address].(*relationDescriptor)
	replacement := *old
	definition := *old.view
	replacement.view = &definition
	replacement.revision++
	previous := definition.source
	definition.source = wref
	root, err := remapViewQuery(definition.query.Root, func(ref ObjectRef) ObjectRef {
		if ref.Address.ClassID == RelationClassID && ref.Address.ObjectID == previous.Address.ObjectID {
			ref.Address.ObjectID = wref.Address.ObjectID
		}
		return ref
	}, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ })
	if err != nil {
		t.Fatal(err)
	}
	query := *definition.query
	query.Root = root
	definition.query = &query
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil {
		t.Fatal("cyclic view source accepted")
	}
	if e.CatalogRead().state != base.state {
		t.Fatal("failed validation published")
	}
}

func TestViewRuleOIDUniquenessAfterRestore(t *testing.T) {
	source := New()
	run(t, source, "CREATE TABLE t(id INT)")
	run(t, source, "CREATE VIEW v AS SELECT id FROM t")
	var dump bytes.Buffer
	if err := source.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	for _, temporary := range []bool{false, true} {
		name := "plain"
		if temporary {
			name = "retained-temp"
		}
		t.Run(name, func(t *testing.T) {
			target := New()
			operation, done := roleOperation(t, target, "postgres")
			defer done()
			if temporary {
				roleExecute(t, target, operation, "CREATE TEMPORARY SEQUENCE retained")
			}
			if err := target.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(t, target, "CREATE TABLE fresh(id INT)")
			readOids := func(query string) []uint32 {
				result := run(t, target, query)
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
			if len(rules) != 1 {
				t.Fatal("missing restored rule", rules)
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
