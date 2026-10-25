package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// These fixtures exercise private staging/publication, never SQL execution.
func domainUnitStage(t *testing.T, e *core.Engine, op *session.Operation, q string) *core.CatalogStage {
	t.Helper()
	ast := statement(t, q)
	e.Mu.Lock()
	var stage *core.CatalogStage
	var err error
	if enum, ok := ast.(*parser.CreateEnum); ok {
		var auth core.AuthorizationView
		auth, err = e.AuthorizationLocked(op)
		if err == nil {
			stage = e.BeginCatalogStage(auth.Catalog)
			err = typedef.StageCreateEnumLocked(TypeHost{TableHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}}, auth, enum)
		}
	} else {
		stage, err = stageSchemaLocked(e, op, ast, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(q, err)
	}
	return stage
}
func domainUnitPublish(t *testing.T, e *core.Engine, stage *core.CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = publishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}
func domainUnitType(t *testing.T, view catalog.Read, name string) *catalog.Type {
	t.Helper()
	addresses := view.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})
	if len(addresses) != 1 {
		t.Fatal(name, addresses)
	}
	return view.MustType(addresses[0])
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
	if a.Address().ClassID != ids.TypeClassID || a.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}) || a.Owner != op.RoleState().CurrentUser || a.Revision() == 0 || chain.Address() == a.Address() || chain.SQLType.DomainBase().Type != a.SQLType || chain.SQLType.BaseType() != numeric || chain.SQLType.Usage().BaseUsage().Typmod != 327686 || names.Dimensions != 2 || names.SQLType.BaseType().ID() != sqltype.NameArray {
		t.Fatal(a, chain, names)
	}
	if a.SQLType.Usage().Typmod != -1 || chain.SQLType.Usage().Typmod != -1 || !typedef.DefaultDomainACL(a) {
		t.Fatal("logical usage/ACL", a)
	}
	if len(original.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: a.Namespace(), Name: "amount"})) != 0 || len(first.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: a.Namespace(), Name: "chain"})) != 0 || first.Get(a.Address()) != a {
		t.Fatal("retained catalog mutated")
	}
	before := e.CatalogRead().State()
	duplicate := statement(t, "CREATE DOMAIN public.amount AS missing_base")
	e.Mu.Lock()
	_, err := stageSchemaLocked(e, op, duplicate, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	e.Mu.Unlock()
	domainUnitDiagnostic(t, err, "42710", `type "amount" already exists`)
	if e.CatalogRead().State() != before {
		t.Fatal("failed CREATE published")
	}
	foreignBase, _ := New().CatalogRead().LookupType(sqltype.Int4)
	forgedType, err := sqltype.Domain(60000, "forged", "public.forged", foreignBase.Usage())
	if err != nil {
		t.Fatal(err)
	}
	forged := *a
	forged.SQLType = forgedType
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err = stage.Create(&forged); err != nil {
		t.Fatal(err)
	}
	if _, err = e.ValidateCatalogStage(context.Background(), stage); err == nil || e.CatalogRead().State() != before {
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
	replacement.ObjectRevision++
	replacement.ACL = authorization.ACLValue{Explicit: true}
	replacement.ACLSyntheticDefault = false
	if err = change.Replace(&replacement); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, change)
	before := e.CatalogRead().State()
	if _, err = publishCatalog(e, prepared, core.PublicationToken{Epoch: chain.Base.Epoch()}); err == nil || e.CatalogRead().State() != before {
		t.Fatal("base authorization generation lost", err)
	}
	drop := e.BeginCatalogStage(e.CatalogRead())
	drop.DomainTargets = []ids.ObjectAddress{a.Address()}
	if err = core.StageRemoveObject(drop, e.Catalog.Domain.Ref(drop.Base.Epoch(), a.Address()), false); err != nil {
		t.Fatal(err)
	}
	prepared, err = e.ValidateCatalogStage(context.Background(), drop)
	if err != nil {
		t.Fatal(err)
	}
	rawTable := statement(t, "CREATE TABLE public.concurrent(v public.a)").(*parser.CreateTable)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	table := e.BeginCatalogStage(auth.Catalog)
	if err == nil {
		err = SchemaTableLocked(e, op, auth, table, rawTable, a.Namespace(), auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, table)
	before = e.CatalogRead().State()
	if _, err = publishCatalog(e, prepared, core.PublicationToken{Epoch: drop.Base.Epoch()}); err == nil || e.CatalogRead().State() != before {
		t.Fatal("DROP lost new dependent column", err)
	}
	canceled := domainUnitStage(t, e, op, "CREATE DOMAIN public.canceled AS integer")
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err = e.ValidateCatalogStage(ctx, canceled)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err = publishCatalog(e, prepared, core.PublicationToken{Epoch: canceled.Base.Epoch()}); !errors.Is(err, context.Canceled) || e.CatalogRead().State() != before {
		t.Fatal("canceled domain published", err)
	}
}

func TestDomainBaseUsageAndOwnerAuthorizationInternal(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.a AS integer"))
	a := domainUnitType(t, e.CatalogRead(), "a")
	e.Mu.Lock()
	reader := e.RoleStore.Domain.Principal(e.RoleStore.NextPrincipal())
	e.RoleStore.InstallPrincipalFrontierLocked(e.RoleStore.NextPrincipal() + 1)
	roles := cloneRoleFixture(e.RoleStore.Published)

	putFixtureRole(roles, authorization.RoleDescriptor{Ref: reader, Name: "reader"})
	e.RoleStore.PublishLocked(roles)
	e.Mu.Unlock()
	auth := catalogAuthorization(e, e.CatalogRead())
	auth.State.CurrentUser = reader
	ref := e.Catalog.Domain.Ref(auth.Catalog.Epoch(), a.Address())
	if err := auth.CanUse(ref); err != nil {
		t.Fatal("default PUBLIC USAGE", err)
	}
	domainUnitDiagnostic(t, auth.Check(access.Requirement{Object: ref, OwnerOnly: true}), "42501", "must be owner of type a")
	stage := e.BeginCatalogStage(auth.Catalog)
	next := *a
	next.ACL = authorization.ACLValue{Explicit: true}
	next.ACLSyntheticDefault = false
	next.ObjectRevision++
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	auth = catalogAuthorization(e, e.CatalogRead())
	auth.State.CurrentUser = reader
	ref.Epoch = auth.Catalog.Epoch()
	domainUnitDiagnostic(t, auth.CanUse(ref), "42501", "permission denied for type a")
	withPublicSchemaUnitOwner(t, e, "reader", func() {
		auth = catalogAuthorization(e, e.CatalogRead())
		auth.State.CurrentUser = reader
		before := e.CatalogRead().State()
		denied := statement(t, "CREATE DOMAIN public.denied AS public.a").(*parser.CreateDomain)
		e.Mu.Lock()
		stage = e.BeginCatalogStage(auth.Catalog)
		err := typedef.StageCreateDomainLocked(TypeHost{TableHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}}, auth, denied, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
		e.Mu.Unlock()
		domainUnitDiagnostic(t, err, "42501", "permission denied for type a")
		if e.CatalogRead().State() != before || stage.DescriptorCount() != 0 {
			t.Fatal("denied base published")
		}
	})
	if len(descriptorReferences(a)) == 0 {
		t.Fatal("domain principal references lost")
	}
}

func TestDomainDeclarationClausesAtomicInternal(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.defaulted AS integer DEFAULT 7 NOT NULL"))
	defaulted := domainUnitType(t, e.CatalogRead(), "defaulted")
	if defaulted.DomainDefault == nil || !defaulted.DomainDefaultOwn || !defaulted.SQLType.OwnDomainNotNull() {
		t.Fatal("supported declaration lost recipe/nullability")
	}
	for _, tc := range []struct{ query, message string }{
		{"CREATE DOMAIN public.denied AS integer DEFAULT 7 DEFAULT 8", "multiple default expressions"},
		{"CREATE DOMAIN public.denied AS integer NULL NOT NULL", "conflicting NULL/NOT NULL constraints"},
	} {
		q := tc.query
		raw := statement(t, q)
		before := e.CatalogRead().State()
		e.Mu.Lock()
		_, err := stageSchemaLocked(e, op, raw, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
		e.Mu.Unlock()
		domainUnitDiagnostic(t, err, "42601", tc.message)
		if e.CatalogRead().State() != before {
			t.Fatal("invalid declaration published", q)
		}
	}
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.nullable AS integer NULL"))
	d := domainUnitType(t, e.CatalogRead(), "nullable")
	if !sqltype.Null(d.SQLType).IsNull() {
		t.Fatal("explicit NULL lost default")
	}
	auth := catalogAuthorization(e, e.CatalogRead())
	var dependencies []access.RoleDependency
	access.DescriptorRoleDependencies(d, d.Owner, &dependencies)
	if len(dependencies) == 0 || (RoleHost{core.DDLHost{E: e}}).DescribeRoleDependency(auth, dependencies[0]) != "owner of type nullable" {
		t.Fatal("domain role dependency", dependencies)
	}
}

func TestDomainUniqueASTRejectedWithoutPublication(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	before := e.CatalogRead().State()
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
			e.Mu.Lock()
			_, err := stageSchemaLocked(e, op, ast, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
			e.Mu.Unlock()
			domainUnitDiagnostic(t, err, "42601", "unique constraints not possible for domains")
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Position != "49" {
				t.Fatal("lost nested UNIQUE source position", err)
			}
			if e.CatalogRead().State() != before || len(e.CatalogRead().Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "rejected"})) != 0 {
				t.Fatal("invalid domain AST changed catalog")
			}
		}
	}
	// The rejected name remains usable for the supported unconstrained NULL case.
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.rejected AS integer NULL"))
	if domainUnitType(t, e.CatalogRead(), "rejected").SQLType.BaseType().ID() != sqltype.Int4 {
		t.Fatal("explicit NULL domain lost base identity")
	}
}
