package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func collationUnitPublish(t *testing.T, e *Engine, stage *CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}

func collationUnitDefinition(e *Engine, id ids.OID, name string) *collationDescriptor {
	owner, _ := e.Roles().Lookup("postgres")
	return &collationDescriptor{oid: id, name: name, namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, revision: 1, owner: owner.Ref, provider: "c", deterministic: true, encoding: -1, collate: collationText("C"), ctype: collationText("C"), semantics: 950}
}

func TestCollationCatalogAuthority(t *testing.T) {
	e := New()
	d := collationUnitDefinition(e, 90000, "registered")
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	view := e.CatalogRead()
	ref, err := d.usage()
	text, _ := view.LookupType(sqltype.Text)
	usage := text.Usage()
	usage.Collation = ref
	if err != nil || !usage.Valid() || validateUsageCollation(view, usage) != nil {
		t.Fatal(ref, usage, err)
	}
	if validateUsageCollation(base, usage) == nil {
		t.Fatal("capability admitted without live identity")
	}
	for _, corrupt := range []func(*collationDescriptor){
		func(d *collationDescriptor) { d.semantics = 6411 }, func(d *collationDescriptor) { d.provider = "i" },
		func(d *collationDescriptor) { d.encoding = 8 }, func(d *collationDescriptor) { d.rules = collationText("") },
		func(d *collationDescriptor) { d.version = collationText("1") },
	} {
		bad := *d
		corrupt(&bad)
		if err := (collationProvider{}).Validate(view, &bad); err == nil {
			t.Fatal("invalid descriptor admitted", bad)
		}
	}
	foreignOwner := *d
	foreignOwner.owner = New().roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	foreignOwner.revision++
	stage = e.BeginCatalogStage(view)
	if err := stage.Replace(&foreignOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil {
		t.Fatal("foreign owner admitted by synchronized stage validation")
	}
	next := *d
	next.name = "renamed"
	next.revision++
	stage = e.BeginCatalogStage(view)
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	if collationSQLName(view, 90000) != `"public"."registered"` || collationSQLName(e.CatalogRead(), 90000) != `"public"."renamed"` {
		t.Fatal("snapshot names lost identity")
	}
	if current, err := next.usage(); err != nil || current != ref {
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
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: before.Epoch()}); !errors.Is(err, context.Canceled) || e.CatalogRead().state != before.state {
		t.Fatal("cancellation published a collation", err)
	}
}

func TestCollationDumpLosslessBootstrapSelection(t *testing.T) {
	e := New()
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
		{"b", "C", "", 6, 950, collationText("1"), `FROM "pg_catalog"."ucs_basic"`},
		{"b", "C", "", -1, 950, collationText("future!?"), "VERSION = 'future!?'"},
		{"b", "C.UTF-8", "", 6, 811, collationText("1"), "LOCALE = 'C.UTF-8'"},
		{"b", "PG_UNICODE_FAST", "", 6, 6411, collationText("1"), "LOCALE = 'PG_UNICODE_FAST'"},
		{"b", "PG_UNICODE_FAST", "", 6, 6411, collationText("future!?"), "VERSION = 'future!?'"},
	} {
		d := collationUnitDefinition(e, 90000, "dumped")
		d.provider, d.encoding, d.semantics, d.version = tc.provider, tc.encoding, tc.mode, tc.version
		if tc.provider == "b" {
			d.collate, d.ctype = nil, nil
			d.locale = collationText(tc.locale)
		} else {
			d.collate, d.ctype = collationText(tc.category), collationText(tc.category)
		}
		stage := e.BeginCatalogStage(view)
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
		snapshot := e.stageRead(stage)
		statements, err := collationDumpStatements(context.Background(), snapshot, e.Roles(), []*collationDescriptor{d})
		if err != nil || len(statements) != 1 || !strings.Contains(statements[0].sql, tc.fragment) {
			t.Fatal(tc, statements, err)
		}
		bad := *d
		bad.encoding = 7
		if _, err = collationDumpStatements(context.Background(), snapshot, e.Roles(), []*collationDescriptor{&bad}); err == nil {
			t.Fatal("lossy encoding accepted")
		}
		if tc.provider == "b" && tc.locale != "C" {
			bad.encoding = -1
			if _, err = collationDumpStatements(context.Background(), snapshot, e.Roles(), []*collationDescriptor{&bad}); err == nil {
				t.Fatal("Unicode provider encoding cannot be reproduced", tc)
			}
		}
	}
}

func TestCollationOwnerMembershipRequiresDestinationCreate(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
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
	ns := &namespaceDescriptor{address: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90000}, name: "private", revision: 1, owner: actor.Ref}
	d := collationUnitDefinition(e, 90001, "owned")
	d.namespace, d.owner = ns.address, actor.Ref
	for _, descriptor := range []Descriptor{ns, d} {
		if err := stage.Create(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	collationUnitPublish(t, e, stage)
	auth, err := admin.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	auth.state.CurrentUser = actor.Ref
	stage = e.BeginCatalogStage(auth.catalog)
	change := &parser.AlterCollation{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "private"}, {Text: "owned"}}}, Action: "owner", Target: parser.Identifier{Text: "target"}}
	e.mu.Lock()
	err = e.stageAlterCollationLocked(admin, auth, stage, change)
	e.mu.Unlock()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for schema private" || len(stage.descriptors) != 0 {
		t.Fatal("membership waived target CREATE", err)
	}
	auth.state.CurrentUser = bootstrap.Ref
	stage = e.BeginCatalogStage(auth.catalog)
	e.mu.Lock()
	err = e.stageAlterCollationLocked(admin, auth, stage, change)
	e.mu.Unlock()
	if err != nil {
		t.Fatal("superuser failed owner transfer", err)
	}
	changed := stage.descriptors[d.Address()].(*collationDescriptor)
	if changed.owner != target.Ref || changed.oid != d.oid {
		t.Fatal("owner transfer lost identity", changed)
	}
	var dependencies []roleDependency
	descriptorRoleDependencies(changed, target.Ref, &dependencies)
	if len(dependencies) != 1 {
		t.Fatal("missing owner dependency", dependencies)
	}
}
