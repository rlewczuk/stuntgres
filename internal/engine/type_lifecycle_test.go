package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// These witnesses exercise private immutable staging/publication functions;
// public SQL/API behavior is exclusively in reference/domain_types/type_lifecycle_*.
func TestTypeLifecycleGenerationClosure(t *testing.T) {
	for _, family := range []struct{ name, extension, create, input string }{
		{"mood", "", "CREATE TYPE public.mood AS ENUM ('z','a')", "z"},
		{"pair", "", "CREATE TYPE public.pair AS(n integer,note text)", "(7,x)"},
		{"cube", "cube", "", "(1,2)"},
		{"query_int", "intarray", "", "1"},
		{"citext", "citext", "", "MiXeD"},
		{"earth", "earthdistance", "", "(6378168,0,0)"},
	} {
		t.Run(family.name, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			if family.extension != "" {
				if family.extension == "earthdistance" {
					domainUnitPublish(t, e, cubeUnitStage(t, e, op, "cube", ""))
				}
				domainUnitPublish(t, e, cubeUnitStage(t, e, op, family.extension, ""))
			} else {
				domainUnitPublish(t, e, domainUnitStage(t, e, op, family.create))
			}
			for _, q := range []string{"CREATE SCHEMA moved", "CREATE DOMAIN public.child AS public." + family.name, "CREATE TYPE public.carrier AS(v public.child)", "CREATE TYPE public.peer AS ENUM ('z')"} {
				domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
			}
			table := domainUnitTable(t, e, op, "CREATE TABLE public.rows(v public."+family.name+",d public.child)")
			before := e.CatalogRead()
			old := domainUnitType(t, before, family.name)
			child := domainUnitType(t, before, "child")
			peer := domainUnitType(t, before, "peer")
			carrier := domainUnitType(t, before, "carrier")
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: before}}
			evaluation.Input.DomainChecks = &evaluation
			value, err := old.SQLType.InputWithEnvironment(evaluation.Context, family.input, -1, evaluation.Input)
			if err != nil {
				t.Fatal(err)
			}
			// Wrap takes a physical datum; earth's already-checked logical
			// value keeps its own tag in the first column.
			physical, err := value.BaseValue()
			if err != nil {
				t.Fatal(err)
			}
			wrapped, err := child.SQLType.Wrap(physical)
			if err != nil {
				t.Fatal(err)
			}
			original := &storage.Relation{Generation: 1, Versions: []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{value, wrapped}}}}
			e.Mu.Lock()
			e.Storage.InstallRelationLocked(table.Address(), original)
			e.Visibility.SetCommitSeqLocked(1)
			e.Mu.Unlock()
			members := old.SQLType.EnumMembers()
			var procedures []*catalog.Procedure
			provider := old
			var domainIO sqltype.PGTypeMetadata
			if old.SQLType.IsDomain() {
				provider = before.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.SQLType.BaseType().ID())})
				domainIO, err = pgcatalog.TypeFacts(context.Background(), before, old, sqltype.OutputEnvironment{})
				if err != nil || domainIO.Input != 2597 || domainIO.Base != old.SQLType.DomainBase().Type.ID() {
					t.Fatal("domain I/O/base identity", domainIO, err)
				}
			}
			if provider.Extension != (ids.ObjectAddress{}) {
				installed, ok := before.Extension(provider.Extension)
				if !ok {
					t.Fatal("missing installed provider")
				}
				for _, address := range installed.Members {
					if address.ClassID != ids.ProcedureClassID {
						continue
					}
					procedure, ok := before.Procedure(address)
					if !ok {
						t.Fatal("missing genuine installed procedure")
					}
					procedures = append(procedures, procedure)
				}
			}
			for _, q := range []string{"ALTER TYPE public." + family.name + " RENAME TO renamed", "ALTER TYPE public.renamed SET SCHEMA moved"} {
				domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
			}
			current := e.CatalogRead()
			next := current.MustType(old.Address())
			if old.SQLType.IsDomain() {
				facts, err := pgcatalog.TypeFacts(context.Background(), current, next, sqltype.OutputEnvironment{})
				if err != nil || facts.Input != domainIO.Input || facts.Output != domainIO.Output || facts.Base != domainIO.Base || !next.SQLType.HasDomainChecks() {
					t.Fatal("domain codec/CHECK/base identity changed", facts, err)
				}
			}
			for _, oldProcedure := range procedures {
				procedure, ok := current.Procedure(oldProcedure.Address())
				if !ok || procedure.Signature != oldProcedure.Signature || procedure.Result != oldProcedure.Result || procedure.Name() != oldProcedure.Name() || procedure.Namespace() != oldProcedure.Namespace() || procedure.Owner != oldProcedure.Owner || procedure.Extension != oldProcedure.Extension {
					t.Fatal("installed procedure identity/signature authority changed")
				}
			}
			if len(current.State().ScalarImports()) != 0 {
				t.Fatal("transient scalar import authority leaked into published state")
			}
			if old.SQLType.IsCitext() {
				if _, err := value.Retag(next.SQLType); err == nil {
					t.Fatal("ordinary Retag accepted a distinct citext generation")
				}
			}
			array := current.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.Array)})
			if next == old || next.SQLType == old.SQLType || next.Address() != old.Address() || next.Array != old.Array || next.Owner != old.Owner || next.Name() != "renamed" || next.SQLType.SQLName() != "moved.renamed" {
				t.Fatal("type identity/provider cutover lost")
			}
			if array.SQLType.Element() != next.SQLType || array.Namespace() != next.Namespace() || array.Name() != "_renamed" {
				t.Fatal("stale companion provider")
			}
			forged := *array
			if array.SQLType.Concrete() {
				forged.SQLType, err = sqltype.Array(array.SQLType.ID(), array.Name(), old.SQLType)
			} else {
				forged.SQLType = sqltype.LookupArray(array.SQLType.ID(), array.Name(), old.SQLType)
			}
			if err != nil {
				t.Fatal(err)
			}
			invalid := catalog.CloneState(current.State())
			invalid.SetDescriptor(forged.Address(), &forged)
			if err := e.Catalog.Owner.Provider(ids.TypeClassID).Validate(e.Catalog.Read(invalid), &forged); err == nil {
				t.Fatal("stale same-OID element generation passed provider validation")
			}
			newChild := current.MustType(child.Address())
			if newChild.SQLType.DomainBase().Type != next.SQLType || current.MustType(carrier.Address()).SQLType.CompositeMember(0).Usage.Type != newChild.SQLType {
				t.Fatal("dependent provider closure stale")
			}
			if current.MustRelation(table.Address()).Columns[0].Usage.Type != next.SQLType || e.Storage.Relations[table.Address()].Versions[0].Values[0].Type() != next.SQLType {
				t.Fatal("column/storage provider closure stale")
			}
			if current.MustType(peer.Address()) != peer || old.Name() != family.name || before.MustType(old.Address()) != old || original.Versions[0].Values[0].Type() != old.SQLType || original.Versions[0].Values[1].Type() != child.SQLType {
				t.Fatal("retained or unrelated immutable roots changed")
			}
			if !reflect.DeepEqual(next.SQLType.EnumMembers(), members) {
				t.Fatal("enum members reminted")
			}
			if old.OwningRelation != (ids.ObjectAddress{}) {
				rel := current.MustRelation(old.OwningRelation)
				previous := before.MustRelation(old.OwningRelation)
				if rel.Name() != next.Name() || rel.Namespace() != next.Namespace() || rel.RowType != next.SQLType.ID() || !reflect.DeepEqual(rel.Attributes, previous.Attributes) {
					t.Fatal("backing relation identity lost")
				}
			}
		})
	}
}

func TestTypeLifecyclePublicationAtomicity(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE public.mood AS ENUM ('z')"))
	before := e.CatalogRead()
	old := domainUnitType(t, before, "mood")
	for _, afterValidation := range []bool{false, true} {
		stage := domainUnitStage(t, e, op, "ALTER TYPE public.mood RENAME TO canceled")
		ctx, cancel := context.WithCancel(context.Background())
		if !afterValidation {
			cancel()
		}
		prepared, err := e.ValidateCatalogStage(ctx, stage)
		if afterValidation {
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			cancel()
			_, err = publishCatalog(e, prepared, core.PublicationToken{Epoch: before.Epoch()})
		}
		cancel()
		if !errors.Is(err, context.Canceled) || e.CatalogRead().State() != before.State() {
			t.Fatal("cancellation published private identities", err)
		}
		e.DiscardCatalogStage(stage)
	}
	stale := domainUnitStage(t, e, op, "ALTER TYPE public.mood RENAME TO stale")
	defer e.DiscardCatalogStage(stale)
	prepared, err := e.ValidateCatalogStage(context.Background(), stale)
	if err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "ALTER TYPE public.mood RENAME TO winner"))
	winner := e.CatalogRead()
	if _, err := publishCatalog(e, prepared, core.PublicationToken{Epoch: before.Epoch()}); err == nil || e.CatalogRead().State() != winner.State() {
		t.Fatal("stale identity stage published", err)
	}
	if winner.MustType(old.Address()).Name() != "winner" || winner.MustType(old.Address()).Array != old.Array {
		t.Fatal("winner companion identity changed")
	}
}

func TestTypeLifecycleDropClosure(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE public.mood AS ENUM ('z')"))
	before := e.CatalogRead()
	old := domainUnitType(t, before, "mood")
	stage := domainUnitStage(t, e, op, "DROP TYPE public.mood")
	defer e.DiscardCatalogStage(stage)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	if err := scope.Gate(statement(t, "DROP TYPE public.mood")); err != nil {
		t.Fatal(err)
	}
	if err := stageDomainDependents(scope, stage, parser.DropRestrict); err != nil {
		t.Fatal(err)
	}
	if stage.RemovalCount() != 2 {
		t.Fatal("enum and actual array not one retirement closure", stage.RemovalCount())
	}
	for _, a := range []ids.ObjectAddress{old.Address(), {ClassID: ids.TypeClassID, ObjectID: ids.OID(old.Array)}} {
		found := false
		for address := range stage.Removals() {
			if address == a {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("missing retirement identity", a)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.ValidateCatalogStage(ctx, stage); !errors.Is(err, context.Canceled) || e.CatalogRead().State() != before.State() {
		t.Fatal("canceled drop published", err)
	}
}

func TestTypeLifecycleDropResources(t *testing.T) {
	for _, family := range []struct{ name, create string }{
		{"mood", "CREATE TYPE public.mood AS ENUM ('a')"},
		{"wrapped", "CREATE DOMAIN public.wrapped AS integer"},
		{"pair", "CREATE TYPE public.pair AS(v integer)"},
	} {
		t.Run(family.name, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			domainUnitPublish(t, e, domainUnitStage(t, e, op, family.create))
			old := domainUnitType(t, e.CatalogRead(), family.name)
			stage := domainUnitStage(t, e, op, "DROP TYPE public."+family.name)
			defer e.DiscardCatalogStage(stage)
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			if err := scope.Gate(statement(t, "DROP TYPE public."+family.name)); err != nil {
				_ = scope.Finish(err)
				t.Fatal(err)
			}
			if err := acquireTypedSources(scope, stage, true); err != nil {
				_ = scope.Finish(err)
				t.Fatal(err)
			}
			state := e.Locks.Inspect()
			resource := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(stage.Base.Epoch(), old.Address())}
			if state.Resources[resource] == nil || state.Resources[resource].Holders[scope.LockTicket].Mode != lock.TypeExclusive {
				t.Fatal("DROP did not lock the actual type resource")
			}
			for resource, entry := range state.Resources {
				if resource.ObjectRef.Address.ClassID != ids.RelationClassID {
					continue
				}
				if !old.SQLType.IsComposite() || resource.ObjectRef.Address != old.OwningRelation || entry.Holders[scope.LockTicket].Mode != lock.AccessExclusive {
					t.Fatal("non-backing relation lock", resource)
				}
			}
			if old.SQLType.IsComposite() {
				backing := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(stage.Base.Epoch(), old.OwningRelation)}
				if state.Resources[backing] == nil || state.Resources[backing].Holders[scope.LockTicket].Mode != lock.AccessExclusive {
					t.Fatal("real composite backing lock missing")
				}
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTypeLifecycleNominalDeclaration(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, cubeUnitStage(t, e, op, "cube", ""))
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.nominal AS public.cube[]"))
	view := e.CatalogRead()
	domain := domainUnitType(t, view, "nominal")
	array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(domain.Array)})
	for _, tc := range []struct {
		usage sqltype.Usage
		want  string
	}{
		{domain.SQLType.Usage(), `"public"."nominal"`},
		{domain.SQLType.DomainBase(), `"public"."cube"[]`},
		{array.SQLType.Usage(), `"public"."nominal"[]`},
	} {
		if tc.usage.Type.Concrete() || tc.usage.Valid() {
			t.Fatal("nominal declaration acquired executable usage")
		}
		got, err := regobj.TypeDeclaration(view, tc.usage)
		if err != nil || got != tc.want {
			t.Fatal(got, tc.want, err)
		}
	}
	facts, err := pgcatalog.TypeFacts(context.Background(), view, domain, sqltype.OutputEnvironment{})
	if err != nil || facts.Kind != "d" || facts.Base != domain.SQLType.DomainBase().Type.ID() || facts.Input != 2597 || facts.Output != 751 {
		t.Fatal("nominal domain metadata lost genuine immediate array base", facts, err)
	}
	arrayFacts, err := pgcatalog.TypeFacts(context.Background(), view, array, sqltype.OutputEnvironment{})
	if err != nil || arrayFacts.Element != domain.SQLType.ID() {
		t.Fatal("nominal companion metadata lost its element", arrayFacts, err)
	}
	foreignDomain := sqltype.LookupDomain(domain.SQLType.ID(), domain.Name(), domain.SQLType.SQLName(), domain.SQLType.DomainBase())
	base := domain.SQLType.DomainBase().Type
	foreignArray := sqltype.LookupArray(base.ID(), base.Name(), base.Element())
	foreignBase := domain.SQLType.DomainBase()
	foreignBase.Type = foreignArray
	forged := *domain
	forged.SQLType = sqltype.LookupDomain(domain.SQLType.ID(), domain.Name(), domain.SQLType.SQLName(), foreignBase)
	if _, err := pgcatalog.TypeFacts(context.Background(), view, &forged, sqltype.OutputEnvironment{}); err == nil {
		t.Fatal("foreign same-OID nominal metadata base accepted")
	}
	modified := domain.SQLType.Usage()
	modified.Typmod = 1
	collated := domain.SQLType.Usage()
	collated.Collation = sqltype.CollationRef{ID: 100, Derivation: sqltype.Default}
	for _, u := range []sqltype.Usage{foreignDomain.Usage(), foreignArray.Usage(), modified, collated} {
		if _, err := regobj.TypeDeclaration(view, u); err == nil {
			t.Fatal("foreign provider or noncanonical nominal usage rendered", u)
		}
	}
}
