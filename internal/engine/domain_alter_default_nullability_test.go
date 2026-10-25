package engine

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// These tests call private stage/validator functions, not public SQL adapters.
func domainValueUnitStage(t *testing.T, e *Engine, op *session.Operation, q string) (*CatalogStage, typedef.DomainEdit) {
	t.Helper()
	raw := statement(t, q).(*parser.AlterDomain)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := typedef.ResolveAlterDomainLocked(DDLHost{E: e, Op: op}, auth, raw.Name)
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	stage.DomainAlterState().Ctx = op.Context()
	stage.DomainBases = append(stage.DomainBases, d)
	edit, err := typedef.StageAlterDomainLocked(TypeHost{TableHost{DDLHost{E: e, Op: op, CatalogStage: stage}}}, auth, d, raw, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		e.DiscardCatalogStage(stage)
		t.Fatal(err)
	}
	return stage, edit
}

func TestDomainAlterDefaultSnapshotsInternal(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE DOMAIN public.p AS integer DEFAULT 7", "CREATE DOMAIN public.c AS public.p", "CREATE DOMAIN public.z AS public.p"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	before := e.CatalogRead()
	oldChild := domainUnitType(t, before, "c")
	s, edit := domainValueUnitStage(t, e, op, "ALTER DOMAIN public.p SET DEFAULT 11")
	if edit.Validation != typedef.DomainValidateNone || edit.Next.DomainDefault == nil || !edit.Next.DomainDefaultOwn {
		t.Fatal("wrong default stage", edit)
	}
	domainUnitPublish(t, e, s)
	child := domainUnitType(t, e.CatalogRead(), "c")
	if child.DomainDefault != oldChild.DomainDefault || child.DomainDefaultText != "7" || child.DomainDefaultOwn {
		t.Fatal("copied recipe rewritten")
	}
	if err := stored.ValidateDomainDefault(e.CatalogRead(), child); err != nil {
		t.Fatal("historical snapshot rejected", err)
	}
	for _, action := range []string{"ALTER DOMAIN public.z DROP DEFAULT", "ALTER DOMAIN public.p SET DEFAULT 13"} {
		s, _ := domainValueUnitStage(t, e, op, action)
		domainUnitPublish(t, e, s)
	}
	z := domainUnitType(t, e.CatalogRead(), "z")
	if z.DomainDefault != nil || z.DomainDefaultOwn || z.DomainDefaultText != "" {
		t.Fatal("DROP re-inherited")
	}
	if err := stored.ValidateDomainDefault(e.CatalogRead(), z); err != nil {
		t.Fatal("independent absence rejected", err)
	}
	for _, kind := range []string{"own_absence", "text", "destination", "manifest"} {
		t.Run(kind, func(t *testing.T) {
			bad := *child
			switch kind {
			case "own_absence":
				bad.DomainDefaultOwn = true
				bad.DomainDefault = nil
				bad.DomainDefaultText = ""
			case "text":
				bad.DomainDefaultText = ""
			case "destination":
				recipe := *bad.DomainDefault
				recipe.OID = 1
				bad.DomainDefault = &recipe
			case "manifest":
				recipe := *bad.DomainDefault
				recipe.Dependencies = append(slices.Clone(recipe.Dependencies), ids.ObjectRef{})
				bad.DomainDefault = &recipe
			}
			if err := stored.ValidateDomainDefault(e.CatalogRead(), &bad); err == nil {
				t.Fatal("invalid snapshot accepted", kind)
			}
		})
	}
	// c's immediate base is the domain p, so NULL is a real own conversion
	// recipe, not a final bare builtin Const. It must replace copied7.
	s, edit = domainValueUnitStage(t, e, op, "ALTER DOMAIN public.c SET DEFAULT NULL")
	if edit.Next.DomainDefault == nil || !edit.Next.DomainDefaultOwn || edit.Next.DomainDefault == child.DomainDefault || edit.Next.DomainDefaultText != "NULL::integer" || edit.Next.DomainDefault.Expression.ResultUsage() != edit.Next.SQLType.DomainBase() {
		t.Fatal("SET NULL lost immediate-domain mask or retained copied recipe")
	}
	domainUnitPublish(t, e, s)
	mask := domainUnitType(t, e.CatalogRead(), "c").DomainDefault
	// p has builtin integer as its immediate base: only this NULL is elided.
	s, edit = domainValueUnitStage(t, e, op, "ALTER DOMAIN public.p SET DEFAULT NULL")
	if edit.Next.DomainDefault != nil || edit.Next.DomainDefaultOwn || edit.Next.DomainDefaultText != "" {
		t.Fatal("bare builtin NULL retained the old recipe")
	}
	domainUnitPublish(t, e, s)
	currentChild := domainUnitType(t, e.CatalogRead(), "c")
	if currentChild.DomainDefault != mask || !currentChild.DomainDefaultOwn || currentChild.DomainDefaultText != "NULL::integer" {
		t.Fatal("parent NULL normalization rewrote the independent child mask")
	}
}

func TestDomainAlterNotNullMetadataInternal(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.required AS integer CONSTRAINT required_check NOT NULL CHECK(VALUE>0)"))
	d := domainUnitType(t, e.CatalogRead(), "required")
	if d.DomainNotNull == nil || d.DomainNotNull.Name != "required_check" || d.DomainChecks[0].Name != "required_check1" {
		t.Fatal("cross-kind generated name reservation lost", d.DomainNotNull, d.DomainChecks)
	}
	// Definition installation follows raw declaration order, but CHECK
	// evaluation remains name-sorted with original raw-list ordinals.
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.ordinal AS integer DEFAULT 7 CONSTRAINT z_last CHECK(VALUE>0) NOT NULL CONSTRAINT a_first CHECK(VALUE>10)"))
	ordered := domainUnitType(t, e.CatalogRead(), "ordinal")
	if len(ordered.DomainChecks) != 2 || ordered.DomainChecks[0].Name != "a_first" || ordered.DomainChecks[0].Ordinal != 3 || ordered.DomainChecks[1].Name != "z_last" || ordered.DomainChecks[1].Ordinal != 1 {
		t.Fatal("source-order installation changed CHECK ordinals/evaluation order", ordered.DomainChecks)
	}
	if !catalog.DefinitionOIDUsed(d, d.DomainNotNull.OID) {
		t.Fatal("NN identity is not owned")
	}
	reserved := map[ids.OID]bool{}
	catalog.ReserveDefinitionOIDs(reserved, d)
	if !reserved[d.DomainNotNull.OID] {
		t.Fatal("NN identity not reserved")
	}
	bad := *d
	bad.DomainNotNull = nil
	if err := stored.ValidateDomainCheckDefinitions(e.CatalogRead(), &bad); err == nil {
		t.Fatal("own flag/record mismatch accepted")
	}
	bad = *d
	record := *d.DomainNotNull
	record.Name = d.DomainChecks[0].Name
	bad.DomainNotNull = &record
	if err := stored.ValidateDomainCheckDefinitions(e.CatalogRead(), &bad); err == nil {
		t.Fatal("cross-kind duplicate accepted")
	}
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.only_nn AS integer NOT NULL"))
	nn := domainUnitType(t, e.CatalogRead(), "only_nn")
	candidate := catalog.CloneState(e.Catalog.State)
	collision := *nn
	record = *nn.DomainNotNull
	record.OID = d.DomainNotNull.OID
	collision.DomainNotNull = &record
	candidate.SetDescriptor(nn.Address(), &collision)
	view := e.Catalog.Read(candidate)
	if err := catalog.ValidateDefinitionOIDs(view); err == nil {
		t.Fatal("definition collision accepted")
	}
	next := ids.OID(90000)
	if err := control.RemapDefinitionOIDs(view, func() (ids.OID, error) { next++; return next, nil }); err != nil {
		t.Fatal(err)
	}
	if err := catalog.ValidateDefinitionOIDs(view); err != nil {
		t.Fatal("NN-only remap not selected", err)
	}
	if view.MustType(nn.Address()).DomainNotNull.OID == d.DomainNotNull.OID {
		t.Fatal("NN-only collision retained")
	}
	if len(view.MustType(d.Address()).DomainChecks) != 1 {
		t.Fatal("NN remap pruned CHECK definitions")
	}
	s, edit := domainValueUnitStage(t, e, op, "ALTER DOMAIN public.required SET NOT NULL")
	if !edit.Skipped {
		t.Fatal("repeat SET must not validate or remint")
	}
	e.DiscardCatalogStage(s)
	s, edit = domainValueUnitStage(t, e, op, "ALTER DOMAIN public.required DROP NOT NULL")
	if edit.Validation != typedef.DomainValidateNone || edit.Next.DomainNotNull != nil || edit.Next.SQLType.OwnDomainNotNull() {
		t.Fatal("DROP changed wrong authority")
	}
	domainUnitPublish(t, e, s)
}

func TestDomainAlterDefaultNullabilityPublication(t *testing.T) {
	for _, action := range []string{"SET DEFAULT 11", "DROP DEFAULT", "SET NOT NULL", "DROP NOT NULL"} {
		kinds := []string{"success", "unrelated", "stale_domain", "owner", "provider", "epoch", "cancel"}
		if action == "SET NOT NULL" {
			kinds = append(kinds, "late_use", "root", "length", "generation", "ends")
		}
		for _, kind := range kinds {
			t.Run(action+"/"+kind, func(t *testing.T) {
				e := New()
				op, done := roleOperation(t, e, "postgres")
				defer done()
				declaration := "CREATE DOMAIN public.d AS integer DEFAULT 7"
				if action == "DROP NOT NULL" {
					declaration += " NOT NULL"
				}
				domainUnitPublish(t, e, domainUnitStage(t, e, op, declaration))
				table := domainUnitTable(t, e, op, "CREATE TABLE public.stored(v public.d)")
				old := domainUnitType(t, e.CatalogRead(), "d")
				s, edit := domainValueUnitStage(t, e, op, "ALTER DOMAIN public.d "+action)
				defer e.DiscardCatalogStage(s)
				if action == "SET NOT NULL" {
					if edit.Validation != typedef.DomainValidateNulls {
						t.Fatal("missing NULL-only validation")
					}
					s.DomainAlterState().Validation = domainAlterUnitFence(t, e, e.CatalogRead(), old.Address())
				} else if edit.Validation != typedef.DomainValidateNone {
					t.Fatal("unexpected historical validation")
				}
				prepared, err := e.ValidateCatalogStage(context.Background(), s)
				if err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "unrelated":
					domainUnitTable(t, e, op, "CREATE TABLE public.unrelated(v integer)")
				case "late_use":
					domainUnitTable(t, e, op, "CREATE TABLE public.late(v public.d)")
				case "stale_domain", "owner":
					state := catalog.CloneState(e.Catalog.State)
					copy := *old
					copy.ObjectRevision++
					if kind == "owner" {
						aclUnitRoles(e, "domain_nn_owner")
						copy.Owner = e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("domain_nn_owner")
					}
					state.SetDescriptor(old.Address(), &copy)
					e.Catalog.PublishLocked(state)
				case "provider":
					address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)}
					s.DomainBases = append(s.DomainBases, e.Catalog.State.Get(address))
					state := catalog.CloneState(e.Catalog.State)
					copy := *state.MustType(address)
					copy.ObjectRevision++
					state.SetDescriptor(address, &copy)
					e.Catalog.PublishLocked(state)
				case "epoch":
					state := catalog.CloneState(e.Catalog.State)
					state.SetEpoch(state.Epoch() + 1)
					e.Catalog.PublishLocked(state)
				case "cancel":
					ctx, cancel := context.WithCancel(context.Background())
					cancel()
					SetPreparedCatalogPublicationCtx(prepared, ctx)
				case "root", "length", "generation", "ends":
					copy := *e.Storage.Relations[table.Address()]
					switch kind {
					case "length":
						copy.Versions = append(slices.Clone(copy.Versions), storage.RowVersion{Row: 1, Version: 1})
					case "generation":
						copy.Generation++
					case "ends":
						copy.Ends = &storage.DeletionRoot{}
					}
					e.Mu.Lock()
					e.Storage.InstallRelationLocked(table.Address(), &copy)
					e.Mu.Unlock()
				}
				before := e.Catalog.State
				stores := maps.Clone(e.Storage.Relations)
				roots := maps.Clone(e.Storage.IndexRoots)
				_, err = publishCatalog(e, prepared, PublicationToken{Epoch: s.Base.Epoch()})
				if kind == "success" || kind == "unrelated" {
					if err != nil || domainUnitType(t, e.CatalogRead(), "d") == old {
						t.Fatal("action not published", err)
					}
				} else {
					if err == nil || e.Catalog.State != before || !maps.Equal(stores, e.Storage.Relations) || !maps.Equal(roots, e.Storage.IndexRoots) {
						t.Fatal("veto partially published", err)
					}
					if kind == "cancel" && !errors.Is(err, context.Canceled) {
						t.Fatal("wrong cancellation", err)
					}
				}
			})
		}
	}
}

func TestDomainAlterDefaultReplayInternal(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.p AS integer DEFAULT 7"))
	raw := statement(t, "CREATE DOMAIN public.p AS integer DEFAULT 7").(*parser.CreateDomain)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	d := domainUnitType(t, auth.Catalog, "p")
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	h := TypeHost{TableHost{DDLHost{E: e, Op: op, CatalogStage: stage}}}
	record := typedef.DomainDefaultSnapshot{Schema: "public", Domain: "p", Own: true, Usage: &typedef.DomainDefaultUsage{Type: []string{"pg_catalog", "int4"}, Typmod: -1}}
	u, err := typedef.SnapshotUsage(h, auth, record)
	if err != nil || u != d.SQLType.DomainBase() {
		t.Fatal("genuine usage resolution", u, err)
	}
	record.Usage.Typmod = 123
	if _, err := typedef.SnapshotUsage(h, auth, record); err == nil {
		t.Fatal("invalid typmod accepted")
	}
	record.Usage.Typmod = -1
	record.Usage.Type = []string{"public", "absent"}
	if _, err := typedef.SnapshotUsage(h, auth, record); err == nil {
		t.Fatal("unresolved identity accepted")
	}
	record.Usage.Type = []string{"pg_catalog", "int4"}
	OperationReplay(op).DomainDefault = &DomainDefaultReplayCapture{Statement: raw, Record: record}
	defer func() { OperationReplay(op).DomainDefault = nil }()
	copy := *d
	handled, err := TypeHostBindRestoredDomainDefault(h, auth, &copy, raw.Constraints[0].Default, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if !handled || err == nil || copy.DomainDefault != d.DomainDefault || e.Catalog.State != auth.Catalog.State() {
		t.Fatal("ordinary engine gained snapshot import authority", handled, err)
	}
}
