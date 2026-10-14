package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// These fixtures exercise private staging/publication, never SQL execution.
func domainUnitStage(t *testing.T, e *Engine, op *Operation, q string) *CatalogStage {
	t.Helper()
	ast := statement(t, q)
	e.mu.Lock()
	var stage *CatalogStage
	var err error
	if enum, ok := ast.(*parser.CreateEnum); ok {
		var auth AuthorizationView
		auth, err = e.authorizationLocked(op)
		if err == nil {
			stage = e.BeginCatalogStage(auth.catalog)
			err = e.stageCreateEnumLocked(op, auth, stage, enum)
		}
	} else {
		stage, err = e.stageSchemaLocked(op, ast, sqltype.InputEnvironment{})
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(q, err)
	}
	return stage
}
func domainUnitPublish(t *testing.T, e *Engine, stage *CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}
func domainUnitType(t *testing.T, view CatalogRead, name string) *typeDescriptor {
	t.Helper()
	addresses := view.Lookup(NameKey{ClassID: TypeClassID, Namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, Name: name})
	if len(addresses) != 1 {
		t.Fatal(name, addresses)
	}
	return view.state.descriptors[addresses[0]].(*typeDescriptor)
}
func domainUnitDiagnostic(t *testing.T, err error, code, message string) {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != code || d.Message != message {
		t.Fatal(err, code, message)
	}
}

func TestDomainCatalogIdentityBaseAndImmutableReads(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	original := e.CatalogRead()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.amount AS numeric(5,2)"))
	first := e.CatalogRead()
	a := domainUnitType(t, first, "amount")
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.chain AS public.amount"))
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.names AS name[9][]"))
	current := e.CatalogRead()
	chain := domainUnitType(t, current, "chain")
	names := domainUnitType(t, current, "names")
	numeric, _ := current.LookupType(sqltype.Numeric)
	if a.Address().ClassID != TypeClassID || a.Namespace() != (ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}) || a.owner != op.RoleState().CurrentUser || a.Revision() == 0 || chain.Address() == a.Address() || chain.typ.DomainBase().Type != a.typ || chain.typ.BaseType() != numeric || chain.typ.Usage().BaseUsage().Typmod != 327686 || names.dimensions != 2 || names.typ.BaseType().ID() != sqltype.NameArray {
		t.Fatal(a, chain, names)
	}
	if a.typ.Usage().Typmod != -1 || chain.typ.Usage().Typmod != -1 || !defaultDomainACL(a) {
		t.Fatal("logical usage/ACL", a)
	}
	if len(original.Lookup(NameKey{ClassID: TypeClassID, Namespace: a.Namespace(), Name: "amount"})) != 0 || len(first.Lookup(NameKey{ClassID: TypeClassID, Namespace: a.Namespace(), Name: "chain"})) != 0 || first.state.descriptors[a.Address()] != a {
		t.Fatal("retained catalog mutated")
	}
	before := e.CatalogRead().state
	duplicate := statement(t, "CREATE DOMAIN public.amount AS missing_base")
	e.mu.Lock()
	_, err := e.stageSchemaLocked(op, duplicate, sqltype.InputEnvironment{})
	e.mu.Unlock()
	domainUnitDiagnostic(t, err, "42710", `type "amount" already exists`)
	if e.CatalogRead().state != before {
		t.Fatal("failed CREATE published")
	}
	foreignBase, _ := New().CatalogRead().LookupType(sqltype.Int4)
	forgedType, err := sqltype.Domain(60000, "forged", "public.forged", foreignBase.Usage())
	if err != nil {
		t.Fatal(err)
	}
	forged := *a
	forged.typ = forgedType
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err = stage.Create(&forged); err != nil {
		t.Fatal(err)
	}
	if _, err = e.ValidateCatalogStage(context.Background(), stage); err == nil || e.CatalogRead().state != before {
		t.Fatal("foreign physical base accepted", err)
	}
}

func TestDomainPublicationFencesBaseAndConcurrentColumns(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.a AS integer"))
	a := domainUnitType(t, e.CatalogRead(), "a")
	chain := domainUnitStage(t, e, op, "CREATE DOMAIN public.chain AS public.a")
	prepared, err := e.ValidateCatalogStage(context.Background(), chain)
	if err != nil {
		t.Fatal(err)
	}
	change := e.BeginCatalogStage(e.CatalogRead())
	replacement := *a
	replacement.revision++
	replacement.acl = authorization.ACLValue{Explicit: true}
	if err = change.Replace(&replacement); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, change)
	before := e.CatalogRead().state
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: chain.base.Epoch()}); err == nil || e.CatalogRead().state != before {
		t.Fatal("base authorization generation lost", err)
	}
	drop := e.BeginCatalogStage(e.CatalogRead())
	drop.domainTargets = []ObjectAddress{a.Address()}
	if err = drop.removeObject(ObjectRef{owner: e.owner, Epoch: drop.base.Epoch(), Address: a.Address()}, false); err != nil {
		t.Fatal(err)
	}
	prepared, err = e.ValidateCatalogStage(context.Background(), drop)
	if err != nil {
		t.Fatal(err)
	}
	rawTable := statement(t, "CREATE TABLE public.concurrent(v public.a)").(*parser.CreateTable)
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	table := e.BeginCatalogStage(auth.catalog)
	if err == nil {
		err = e.schemaTableLocked(op, auth, table, rawTable, a.Namespace(), auth.state.CurrentUser, sqltype.InputEnvironment{}, nil)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, table)
	before = e.CatalogRead().state
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: drop.base.Epoch()}); err == nil || e.CatalogRead().state != before {
		t.Fatal("DROP lost new dependent column", err)
	}
	canceled := domainUnitStage(t, e, op, "CREATE DOMAIN public.canceled AS integer")
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err = e.ValidateCatalogStage(ctx, canceled)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: canceled.base.Epoch()}); !errors.Is(err, context.Canceled) || e.CatalogRead().state != before {
		t.Fatal("canceled domain published", err)
	}
}

func TestDomainBaseUsageAndOwnerAuthorizationInternal(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.a AS integer"))
	a := domainUnitType(t, e.CatalogRead(), "a")
	e.mu.Lock()
	reader := e.authorizationDomain.Principal(e.nextPrincipal)
	e.nextPrincipal++
	roles := cloneRoleFixture(e.roles)

	putFixtureRole(roles, authorization.RoleDescriptor{Ref: reader, Name: "reader"})
	e.roles = roles
	e.mu.Unlock()
	auth := catalogAuthorization(e, e.CatalogRead())
	auth.state.CurrentUser = reader
	ref := ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: a.Address()}
	if err := auth.CanUse(ref); err != nil {
		t.Fatal("default PUBLIC USAGE", err)
	}
	domainUnitDiagnostic(t, auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}), "42501", "must be owner of type a")
	stage := e.BeginCatalogStage(auth.catalog)
	next := *a
	next.acl = authorization.ACLValue{Explicit: true}
	next.revision++
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	auth = catalogAuthorization(e, e.CatalogRead())
	auth.state.CurrentUser = reader
	ref.Epoch = auth.catalog.Epoch()
	domainUnitDiagnostic(t, auth.CanUse(ref), "42501", "permission denied for type a")
	withPublicSchemaUnitOwner(t, e, "reader", func() {
		auth = catalogAuthorization(e, e.CatalogRead())
		auth.state.CurrentUser = reader
		before := e.CatalogRead().state
		denied := statement(t, "CREATE DOMAIN public.denied AS public.a").(*parser.CreateDomain)
		e.mu.Lock()
		stage = e.BeginCatalogStage(auth.catalog)
		err := e.stageCreateDomainLocked(op, auth, stage, denied, sqltype.InputEnvironment{})
		e.mu.Unlock()
		domainUnitDiagnostic(t, err, "42501", "permission denied for type a")
		if e.CatalogRead().state != before || len(stage.descriptors) != 0 {
			t.Fatal("denied base published")
		}
	})
	if len(descriptorReferences(a)) == 0 {
		t.Fatal("domain principal references lost")
	}
}

func TestDomainExcludedClausesNeverPublishInternal(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"CREATE DOMAIN public.denied AS integer DEFAULT 7",
		"CREATE DOMAIN public.denied AS integer NOT NULL",
	} {
		raw := statement(t, q)
		before := e.CatalogRead().state
		e.mu.Lock()
		_, err := e.stageSchemaLocked(op, raw, sqltype.InputEnvironment{})
		e.mu.Unlock()
		domainUnitDiagnostic(t, err, "0A000", "domain constraints and defaults are unsupported")
		if e.CatalogRead().state != before {
			t.Fatal("excluded clause published", q)
		}
	}
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.nullable AS integer NULL"))
	d := domainUnitType(t, e.CatalogRead(), "nullable")
	if !sqltype.Null(d.typ).IsNull() {
		t.Fatal("explicit NULL lost default")
	}
	auth := catalogAuthorization(e, e.CatalogRead())
	var dependencies []roleDependency
	descriptorRoleDependencies(d, d.owner, &dependencies)
	if len(dependencies) == 0 || dependencyDescription(auth, dependencies[0]) != "owner of type nullable" {
		t.Fatal("domain role dependency", dependencies)
	}
}

func TestDomainUniqueASTRejectedWithoutPublication(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	before := e.CatalogRead().state
	for _, named := range []bool{false, true} {
		for _, nullsNotDistinct := range []bool{false, true} {
			suffix := "UNIQUE"
			if named {
				suffix = "CONSTRAINT uq UNIQUE"
			}
			if nullsNotDistinct {
				suffix += " NULLS NOT DISTINCT"
			}
			ast := statement(t, "/*é🙂*/ CREATE DOMAIN public.rejected AS integer "+suffix).(*parser.CreateDomain)
			if len(ast.Constraints) != 1 || ast.Constraints[0].Unique == nil || ast.Constraints[0].Name.Text != "" {
				t.Fatal("missing nested UNIQUE fact", ast)
			}
			unique := ast.Constraints[0].Unique
			if unique.Pos.Character != 49 || unique.Pos.Offset != 52 || unique.NullsNotDistinct != nullsNotDistinct || (unique.Name.Text == "uq") != named {
				t.Fatal("nested UNIQUE source facts", unique)
			}
			// Preserve the real nested source position, but move the outer
			// position to another real token to catch incorrect outer provenance.
			ast.Constraints[0].Pos = ast.Pos
			e.mu.Lock()
			_, err := e.stageSchemaLocked(op, ast, sqltype.InputEnvironment{})
			e.mu.Unlock()
			domainUnitDiagnostic(t, err, "42601", "unique constraints not possible for domains")
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Position != "49" {
				t.Fatal("lost nested UNIQUE source position", err)
			}
			if e.CatalogRead().state != before || len(e.CatalogRead().Lookup(NameKey{ClassID: TypeClassID, Namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, Name: "rejected"})) != 0 {
				t.Fatal("invalid domain AST changed catalog")
			}
		}
	}
	// The rejected name remains usable for the supported unconstrained NULL case.
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.rejected AS integer NULL"))
	if domainUnitType(t, e.CatalogRead(), "rejected").typ.BaseType().ID() != sqltype.Int4 {
		t.Fatal("explicit NULL domain lost base identity")
	}
}
