package schemacmds_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func collationUnitPublish(t *testing.T, e *core.Engine, stage *core.CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}

func collationUnitDefinition(e *core.Engine, id ids.OID, name string) *catalog.Collation {
	owner, _ := e.Roles().Lookup("postgres")
	return &catalog.Collation{OID: id, ObjectName: name, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 1, Owner: owner.Ref, Provider: "c", Deterministic: true, Encoding: -1, Collate: catalog.CollationText("C"), CType: catalog.CollationText("C"), Semantics: 950}
}

func TestCollationCatalogAuthority(t *testing.T) {
	e := engine.New()
	d := collationUnitDefinition(e, 90000, "registered")
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	view := e.CatalogRead()
	ref, err := d.Usage()
	text, _ := view.LookupType(sqltype.Text)
	usage := text.Usage()
	usage.Collation = ref
	if err != nil || !usage.Valid() || stored.ValidateUsageCollation(view, usage) != nil {
		t.Fatal(ref, usage, err)
	}
	if stored.ValidateUsageCollation(base, usage) == nil {
		t.Fatal("capability admitted without live identity")
	}
	for _, corrupt := range []func(*catalog.Collation){
		func(d *catalog.Collation) { d.Semantics = 6411 }, func(d *catalog.Collation) { d.Provider = "i" },
		func(d *catalog.Collation) { d.Encoding = 8 }, func(d *catalog.Collation) { d.Rules = catalog.CollationText("") },
		func(d *catalog.Collation) { d.Version = catalog.CollationText("1") },
	} {
		bad := *d
		corrupt(&bad)
		if err := (catalog.CollationProvider{}).Validate(view, &bad); err == nil {
			t.Fatal("invalid descriptor admitted", bad)
		}
	}
	foreignOwner := *d
	foreignOwner.Owner = engine.New().RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	foreignOwner.ObjectRevision++
	stage = e.BeginCatalogStage(view)
	if err := stage.Replace(&foreignOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil {
		t.Fatal("foreign owner admitted by synchronized stage validation")
	}
	next := *d
	next.ObjectName = "renamed"
	next.ObjectRevision++
	stage = e.BeginCatalogStage(view)
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	if typedef.CollationSQLName(view, 90000) != `"public"."registered"` || typedef.CollationSQLName(e.CatalogRead(), 90000) != `"public"."renamed"` {
		t.Fatal("snapshot names lost identity")
	}
	if current, err := next.Usage(); err != nil || current != ref {
		t.Fatal("rename changed capability", current, ref, err)
	}
	before := e.CatalogRead()
	stage = e.BeginCatalogStage(before)
	added := collationUnitDefinition(e, 90001, "canceled")
	if err := stage.Create(added); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: before.Epoch()}); !errors.Is(err, context.Canceled) || e.CatalogRead().State() != before.State() {
		t.Fatal("cancellation published a collation", err)
	}
}

func TestCollationDumpLosslessBootstrapSelection(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	for _, tc := range []struct {
		provider, locale, category string
		encoding                   int32
		mode                       uint32
		version                    *string
		fragment                   string
	}{
		{"c", "", "C", -1, 950, nil, `FROM "pg_catalog"."C"`},
		{"c", "", "POSIX", -1, 950, nil, `FROM "pg_catalog"."POSIX"`},
		{"c", "", "C", 6, 950, nil, "PROVIDER = libc"},
		{"b", "C", "", 6, 950, catalog.CollationText("1"), `FROM "pg_catalog"."ucs_basic"`},
		{"b", "C", "", -1, 950, catalog.CollationText("future!?"), "VERSION = 'future!?'"},
		{"b", "C.UTF-8", "", 6, 811, catalog.CollationText("1"), "LOCALE = 'C.UTF-8'"},
		{"b", "PG_UNICODE_FAST", "", 6, 6411, catalog.CollationText("1"), "LOCALE = 'PG_UNICODE_FAST'"},
		{"b", "PG_UNICODE_FAST", "", 6, 6411, catalog.CollationText("future!?"), "VERSION = 'future!?'"},
	} {
		d := collationUnitDefinition(e, 90000, "dumped")
		d.Provider, d.Encoding, d.Semantics, d.Version = tc.provider, tc.encoding, tc.mode, tc.version
		if tc.provider == "b" {
			d.Collate, d.CType = nil, nil
			d.Locale = catalog.CollationText(tc.locale)
		} else {
			d.Collate, d.CType = catalog.CollationText(tc.category), catalog.CollationText(tc.category)
		}
		stage := e.BeginCatalogStage(view)
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
		snapshot := e.StageRead(stage)
		statements, err := typedef.CollationDumpStatements(context.Background(), snapshot, e.Roles(), []*catalog.Collation{d})
		if err != nil || len(statements) != 1 || !strings.Contains(statements[0].SQL, tc.fragment) {
			t.Fatal(tc, statements, err)
		}
		bad := *d
		bad.Encoding = 7
		if _, err = typedef.CollationDumpStatements(context.Background(), snapshot, e.Roles(), []*catalog.Collation{&bad}); err == nil {
			t.Fatal("lossy encoding accepted")
		}
		if tc.provider == "b" && tc.locale != "C" {
			bad.Encoding = -1
			if _, err = typedef.CollationDumpStatements(context.Background(), snapshot, e.Roles(), []*catalog.Collation{&bad}); err == nil {
				t.Fatal("Unicode provider encoding cannot be reproduced", tc)
			}
		}
	}
}

func TestCollationOwnerMembershipRequiresDestinationCreate(t *testing.T) {
	e := engine.New()
	admin, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, name := range []string{"actor", "target"} {
		if _, err := e.Execute(admin, &parser.CreateRole{Name: parser.Identifier{Text: name}, DefaultLogin: true}, 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	actor, _ := e.Roles().Lookup("actor")
	target, _ := e.Roles().Lookup("target")
	bootstrap, _ := e.Roles().Lookup("postgres")
	if err := grantMembership(t, e, authorization.Membership{Member: actor.Ref, Role: target.Ref, Grantor: bootstrap.Ref, Set: true}); err != nil {
		t.Fatal(err)
	}
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90000}, ObjectName: "private", ObjectRevision: 1, Owner: actor.Ref}
	d := collationUnitDefinition(e, 90001, "owned")
	d.ObjectNamespace, d.Owner = ns.Address(), actor.Ref
	for _, descriptor := range []catalog.Descriptor{ns, d} {
		if err := stage.Create(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	collationUnitPublish(t, e, stage)
	auth, err := core.OperationAuthorization(admin)
	if err != nil {
		t.Fatal(err)
	}
	auth.State.CurrentUser = actor.Ref
	stage = e.BeginCatalogStage(auth.Catalog)
	change := &parser.AlterCollation{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "private"}, {Text: "owned"}}}, Action: "owner", Target: parser.Identifier{Text: "target"}}
	e.Mu.Lock()
	err = typedef.StageAlterCollationLocked(engine.TypeHost{TableHost: tablecmds.TableHost{DDLHost: core.DDLHost{E: e, Op: admin, CatalogStage: stage}}}, auth, change)
	e.Mu.Unlock()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for schema private" || stage.DescriptorCount() != 0 {
		t.Fatal("membership waived target CREATE", err)
	}
	auth.State.CurrentUser = bootstrap.Ref
	stage = e.BeginCatalogStage(auth.Catalog)
	e.Mu.Lock()
	err = typedef.StageAlterCollationLocked(engine.TypeHost{TableHost: tablecmds.TableHost{DDLHost: core.DDLHost{E: e, Op: admin, CatalogStage: stage}}}, auth, change)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal("superuser failed owner transfer", err)
	}
	changed := stage.Get(d.Address()).(*catalog.Collation)
	if changed.Owner != target.Ref || changed.OID != d.OID {
		t.Fatal("owner transfer lost identity", changed)
	}
	var dependencies []access.RoleDependency
	access.DescriptorRoleDependencies(changed, target.Ref, &dependencies)
	if len(dependencies) != 1 {
		t.Fatal("missing owner dependency", dependencies)
	}
}

// Fixtures use the real private command planner, with checked identities and
// explicit options; there is no second append-only membership publication path.
func grantMembership(t *testing.T, e *core.Engine, m authorization.Membership) error {
	t.Helper()
	r := e.Roles()
	names := make([]string, 3)
	for i, ref := range []authorization.PrincipalRef{m.Role, m.Member, m.Grantor} {
		d, err := r.Descriptor(ref)
		if err != nil {
			return err
		}
		names[i] = d.Name
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	spec := func(name string) parser.RoleSpec { return parser.RoleSpec{Name: name} }
	g := spec(names[2])
	stmt := &parser.RoleMembership{Targets: []parser.RoleSpec{spec(names[0])}, Members: []parser.RoleSpec{spec(names[1])}, Grantor: &g, Options: []parser.MembershipOption{{Name: "admin", Value: m.Admin}, {Name: "inherit", Value: m.Inherit}, {Name: "set", Value: m.Set}}}
	_, err := e.Execute(op, stmt, 0, nil)
	return err
}
