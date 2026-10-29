package control

import (
	"context"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// Use released declarations and real registered implementations. These unit
// fixtures exercise rendering, not provider execution or PostgreSQL authority.
func replayCohort(t *testing.T, source Source, oid ids.OID, prefix string, present, deleteFirst bool) *catalog.Installation {
	t.Helper()
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90000 + oid}
	ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: oid}, ObjectName: "fuzzystrmatch", ObjectRevision: 1, Version: "1.2", Owner: source.Bootstrap, TargetNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}}
	var declarations []catalog.Descriptor
	var addresses []ids.ObjectAddress
	for i, m := range extension.FuzzystrmatchMembers {
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid + ids.OID(i) + 1}
		p := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: namespace, ObjectName: m.Name, ObjectRevision: 1, Extension: ext.Address(), Signature: m.Signature, Result: m.Result, RoutineKind: catalog.FunctionRoutine, Volatility: expr.Immutable, Strict: true, Owner: source.Bootstrap, Implementation: extension.StockFuzzystrmatch}
		addresses = append(addresses, address)
		declarations = append(declarations, p)
	}
	recipe := catalog.NewInstallation(ext.Address(), ext.Name(), ext.Version, extension.StockFuzzystrmatch, addresses)
	recipe.Declarations = declarations
	ext.Installation = recipe
	for i, d := range declarations {
		if i == 0 && deleteFirst {
			continue
		}
		p := *d.(*catalog.Procedure)
		p.ObjectNamespace = ext.TargetNamespace
		p.ObjectName = prefix + p.Name()
		p.Origin = &catalog.InstalledOrigin{Installation: recipe, Declaration: i}
		if present {
			ext.Members = append(ext.Members, p.Address())
		} else {
			p.Extension = ids.ObjectAddress{}
		}
		source.State.SetDescriptor(p.Address(), &p)
		source.State.BindName(catalog.DescriptorNameKey(&p), p.Address())
	}
	if present {
		source.State.SetDescriptor(ext.Address(), ext)
		source.State.BindName(catalog.DescriptorNameKey(ext), ext.Address())
	}
	return recipe
}

func TestInstallationReplayAbsentAndRepeatedProvider(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	source.CatalogView.Owner().Extensions = map[string]catalog.ExtensionImplementation{"fuzzystrmatch": extension.StockFuzzystrmatch, "uuid-ossp": extension.StockUUIDOssp}
	catalog.InstallBuiltinTypes(source.State, sqltype.Builtins(), source.Bootstrap)
	replayCohort(t, source, 50000, "detached_", false, true)
	replayCohort(t, source, 60000, "current_", true, false)
	script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView, OriginalCatalog: source.OriginalCatalog, DomainRoles: source.DomainRoles, OriginalRoles: source.OriginalRoles}}
	if err := script.prepareInstallationReplay(context.Background()); err != nil {
		t.Fatal(err)
	}
	text := strings.Join(script.installationStatements, "")
	firstCreate := "CREATE EXTENSION \"fuzzystrmatch\" WITH SCHEMA \"__stuntgres_install_0\" VERSION '1.2';\n"
	lastCreate := "CREATE EXTENSION \"fuzzystrmatch\" WITH SCHEMA \"__stuntgres_install_1\" VERSION '1.2';\n"
	first := strings.Index(text, firstCreate)
	removed := strings.Index(text, "DROP EXTENSION \"fuzzystrmatch\";\n")
	last := strings.Index(text, lastCreate)
	if first < 0 || removed <= first || last <= removed || strings.Count(text, "CREATE EXTENSION \"fuzzystrmatch\" ") != 2 || strings.Count(text, "DROP EXTENSION \"fuzzystrmatch\";\n") != 1 {
		t.Fatal("cohorts were not reconstructed serially through real installations", text)
	}
	if strings.Contains(text, "ALTER EXTENSION \"fuzzystrmatch\" RENAME") || strings.Contains(text, "ALTER EXTENSION \"fuzzystrmatch\" DROP FUNCTION \"__stuntgres_install_0\".\"daitch_mokotoff\"(pg_catalog.text);\n") {
		t.Fatal("fake extension rename or unwanted baseline escaped residual DROP EXTENSION cleanup", text)
	}
	finalHeader := strings.Index(text, "ALTER EXTENSION \"fuzzystrmatch\" SET SCHEMA \"public\";\n")
	firstAuthority := strings.Index(text, " OWNER TO ")
	if finalHeader <= last || firstAuthority <= finalHeader {
		t.Fatal("final header placement or captured authority barrier missing", text)
	}
	// A later independent CREATE is not a user of the old scratch schema.
	// Retirement must instead follow every actual member/header use and
	// precede the captured-authority/consumer phase, without any later reuse.
	for i, schema := range []string{"__stuntgres_install_0", "__stuntgres_install_1"} {
		createSQL := "CREATE SCHEMA \"" + schema + "\" AUTHORIZATION \"postgres\";\n"
		dropSQL := "DROP SCHEMA \"" + schema + "\";\n"
		created, retired := strings.Index(text, createSQL), strings.Index(text, dropSQL)
		createdBefore, lastHeaderUse := first, removed
		if i == 1 {
			createdBefore, lastHeaderUse = last, finalHeader
		}
		if strings.Count(text, createSQL) != 1 || strings.Count(text, dropSQL) != 1 || created >= createdBefore || retired <= lastHeaderUse || retired+len(dropSQL) >= firstAuthority {
			t.Fatal("scratch namespace creation/last-use/retirement barrier invalid", schema, text)
		}
		if strings.Contains(text[retired+len(dropSQL):], schema) {
			t.Fatal("retired scratch namespace used after its unique DROP", schema, text)
		}
	}
	if len(script.membershipStatements) != len(extension.FuzzystrmatchMembers) {
		t.Fatal("mutable membership conflated with detached recipe", script.membershipStatements)
	}
	if len(script.installationMetadata) != 3 || script.installationMetadata[2] != "COMMENT ON EXTENSION \"fuzzystrmatch\" IS NULL;\n" {
		t.Fatal("absent/present owner inventory or surviving header comment missing", script.installationMetadata)
	}
	for i, metadata := range script.installationMetadata[:2] {
		record, found, err := extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(metadata, "-- ")))
		if err != nil || !found {
			t.Fatal(found, err)
		}
		if record.Present != (i == 1) {
			t.Fatal("absent origin was resurrected", record)
		}
		for _, member := range record.Members {
			if member.Declaration == nil || member.Extension != "" && i == 0 || i == 0 && *member.Declaration == 0 {
				t.Fatal("origin, deleted declaration or detached state lost", member)
			}
		}
	}
}

func TestProviderReplayMethodIdentityCycles(t *testing.T) {
	first := providerReplay{original: providerKey{"source", "first", "btree"}, current: providerKey{"source", "second", "btree"}, arguments: "btree", objectKind: "OPERATOR FAMILY"}
	second := providerReplay{original: providerKey{"source", "second", "btree"}, current: providerKey{"source", "first", "btree"}, arguments: "btree", objectKind: "OPERATOR FAMILY"}
	statements, err := planProviderReplay([]providerReplay{first, second})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(statements, "")
	want := "ALTER OPERATOR FAMILY \"source\".\"first\" USING \"btree\" RENAME TO \"__stuntgres_routine_1\";\n" +
		"ALTER OPERATOR FAMILY \"source\".\"second\" USING \"btree\" RENAME TO \"first\";\n" +
		"ALTER OPERATOR FAMILY \"source\".\"__stuntgres_routine_1\" USING \"btree\" RENAME TO \"second\";\n"
	if text != want {
		t.Fatal("method identity bypassed real family owner", text)
	}
}

func TestInstallationReplaySeededAbsence(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	source.CatalogView.Owner().Extensions = map[string]catalog.ExtensionImplementation{"plpgsql": extension.StockPLpgSQL}
	script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView, OriginalCatalog: source.OriginalCatalog, DomainRoles: source.DomainRoles, OriginalRoles: source.OriginalRoles}}
	if err := script.prepareInstallationReplay(context.Background()); err != nil {
		t.Fatal(err)
	}
	if text := strings.Join(script.installationStatements, ""); text != "DROP EXTENSION plpgsql;\n" {
		t.Fatal("canonical seed resurrected source-absent installation", text)
	}
	if len(script.installationMetadata) != 0 || len(script.membershipStatements) != 0 {
		t.Fatal("absent seed manufactured owners or membership")
	}
}

func TestInstallationReplayIndependentCompanionMembership(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	source.CatalogView.Owner().Extensions = map[string]catalog.ExtensionImplementation{"fuzzystrmatch": extension.StockFuzzystrmatch}
	catalog.InstallBuiltinTypes(source.State, sqltype.Builtins(), source.Bootstrap)
	recipe := replayCohort(t, source, 50000, "", true, false)
	integer, ok := source.CatalogView.LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("missing builtin")
	}
	domain, err := sqltype.Domain(70000, "member_domain", "public.member_domain", integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.Array(70001, "_member_domain", domain)
	if err != nil {
		t.Fatal(err)
	}
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	root := &catalog.Type{SQLType: domain, ObjectNamespace: namespace, ObjectRevision: 1, Owner: source.Bootstrap, Array: array.ID(), Extension: recipe.Cohort}
	companion := &catalog.Type{SQLType: array, ObjectNamespace: namespace, ObjectRevision: 1, Owner: source.Bootstrap}
	for _, d := range []catalog.Descriptor{root, companion} {
		source.State.SetDescriptor(d.Address(), d)
		source.State.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	ext := *source.CatalogView.MustExtension(recipe.Cohort)
	ext.Members = append(ext.Members, root.Address())
	source.State.SetDescriptor(ext.Address(), &ext)
	script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView}}
	statements, err := script.finalMembershipReplay()
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(statements, "")
	addSQL := "ALTER EXTENSION \"fuzzystrmatch\" ADD TYPE \"public\".\"member_domain\";\n"
	detachSQL := "ALTER EXTENSION \"fuzzystrmatch\" DROP TYPE \"public\".\"_member_domain\";\n"
	add := strings.Index(text, addSQL)
	detach := strings.Index(text, detachSQL)
	if add < 0 || detach <= add || strings.Count(text, addSQL) != 1 || strings.Count(text, detachSQL) != 1 || strings.Contains(text, "ADD TYPE \"public\".\"_member_domain\";\n") {
		t.Fatal("root ADD overwrote independent detached companion membership", text)
	}
}

func TestInstallationReplayUnchangedSeed(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	catalog.InstallBuiltinTypes(source.State, sqltype.Builtins(), source.Bootstrap)
	owner := *source.CatalogView.Owner()
	owner.Extensions = map[string]catalog.ExtensionImplementation{"plpgsql": extension.StockPLpgSQL}
	// Changed-seed replay validates every surviving declaration through its
	// real class provider, including the global Language. Identity-only
	// fixtures intentionally do not supply that engine registry.
	owner.Providers = map[ids.OID]catalog.Provider{
		ids.ProcedureClassID: stored.ProcedureProvider{},
		ids.LanguageClassID:  catalog.LanguageProvider{},
	}
	store := catalog.NewStore(owner, 100000, 2)
	store.PublishLocked(source.State)
	if err := store.InstallBuiltinLanguagesLocked(source.Bootstrap); err != nil {
		t.Fatal(err)
	}
	source.CatalogView = store.Current()
	script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView, OriginalCatalog: source.CatalogView, DomainRoles: source.DomainRoles, OriginalRoles: source.OriginalRoles}}
	if err := script.prepareInstallationReplay(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(script.installationStatements) != 0 || len(script.membershipStatements) != 0 || len(script.installationMetadata) != 0 || len(script.preservedSeedMembership) != 4 {
		t.Fatal("unchanged genuine seed emitted replay deltas", script.installationStatements, script.membershipStatements, script.installationMetadata)
	}
	items, err := script.installations()
	if err != nil || len(items) != 1 {
		t.Fatal("genuine seed cohort missing", items, err)
	}
	item := items[0]
	for _, tc := range []struct {
		name    string
		comment string
		want    string
	}{
		{"custom", "seed 雪 'quote'", "COMMENT ON EXTENSION \"plpgsql\" IS E'seed 雪 ''quote''';\n"},
		{"removed", "", "COMMENT ON EXTENSION \"plpgsql\" IS NULL;\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := catalog.CloneState(source.State)
			ext := *item.ext
			ext.Comment = tc.comment
			next.SetDescriptor(ext.Address(), &ext)
			view := source.CatalogView.WithState(next)
			changed := &dumpScript{Capture: &Capture{CatalogView: view, OriginalCatalog: view, DomainRoles: source.DomainRoles, OriginalRoles: source.OriginalRoles}}
			if err := changed.prepareInstallationReplay(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(changed.installationStatements) != 0 || len(changed.membershipStatements) != 0 || len(changed.preservedSeedMembership) != 4 || !slices.Equal(changed.installationMetadata, []string{tc.want}) {
				t.Fatal("comment delta must replay without reconstructing genuine seed", changed.installationStatements, changed.membershipStatements, changed.installationMetadata)
			}
		})
	}
	t.Run("remapped_live_authority", func(t *testing.T) {
		target := new(authorization.Domain)
		roles := authorization.Seed(target)
		state := catalog.CloneState(source.State)
		principal := func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
			return target.Principal(ref.ID()), nil
		}
		if err := catalog.RemapPrincipals(state, catalog.PrincipalRemap{
			Principal: principal,
			SeededOwner: func(ref authorization.PrincipalRef, _ bool) (authorization.PrincipalRef, error) {
				return principal(ref)
			},
		}); err != nil {
			t.Fatal(err)
		}
		mapped := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView.WithState(state), DomainRoles: roles.Read(target.Principal(10))}}
		current := item
		current.ext = state.Get(item.ext.Address()).(*catalog.Extension)
		if !mapped.unchangedSeedInstallation(current) {
			t.Fatal("historical recipe owner domain became current bootstrap authority")
		}
		if item.recipe.Declarations[0].(*catalog.Procedure).Owner != source.Bootstrap {
			t.Fatal("principal mapping rewrote immutable historical declaration")
		}
	})
	handler := source.CatalogView.Get(item.recipe.Members[0]).(*catalog.Procedure)
	for _, change := range []string{"detach", "rename", "owner", "acl", "extra_member", "absent"} {
		t.Run(change, func(t *testing.T) {
			next := catalog.CloneState(source.State)
			current := *handler
			ext := *item.ext
			candidate := item
			switch change {
			case "detach":
				current.Extension = ids.ObjectAddress{}
			case "rename":
				current.ObjectName = "retained_handler"
			case "owner":
				current.Owner = source.DomainRoles.LookupRef("pg_monitor")
			case "acl":
				current.ACL.Explicit = true
			case "extra_member":
				ext.Members = append(slices.Clone(ext.Members), ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 200000})
			case "absent":
				candidate.ext = nil
			}
			next.SetDescriptor(current.Address(), &current)
			if change != "absent" {
				candidate.ext = &ext
			}
			changed := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView.WithState(next), DomainRoles: source.DomainRoles}}
			if changed.unchangedSeedInstallation(candidate) {
				t.Fatal("actual seed delta mistaken for unchanged bootstrap", change)
			}
		})
	}
	for _, declaration := range item.recipe.Declarations {
		handler, ok := declaration.(*catalog.Procedure)
		if !ok {
			continue
		}
		for _, field := range []string{"parallel", "cost"} {
			t.Run(handler.Name()+"/"+field, func(t *testing.T) {
				next := catalog.CloneState(source.CatalogView.State())
				current := *source.CatalogView.MustProcedure(handler.Address())
				suffix := " PARALLEL RESTRICTED;\n"
				if field == "parallel" {
					current.Parallel = 'r'
				} else {
					current.Cost = 9.25
					suffix = " COST 9.25;\n"
				}
				next.SetDescriptor(current.Address(), &current)
				changed := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView.WithState(next), OriginalCatalog: source.CatalogView, DomainRoles: source.DomainRoles, OriginalRoles: source.OriginalRoles}}
				if changed.unchangedSeedInstallation(item) {
					t.Fatal("current handler estimates mistaken for unchanged seed")
				}
				if err := changed.prepareInstallationReplay(context.Background()); err != nil {
					t.Fatal(err)
				}
				text := strings.Join(changed.installationStatements, "")
				identity, err := extension.MemberSQL(source.CatalogView, &current)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(text, "ALTER "+identity+suffix) {
					t.Fatal("current seeded handler estimate omitted", text)
				}
				if handler.Parallel != 0 || handler.Cost != 0 || handler.Rows != 0 {
					t.Fatal("replay changed immutable seed defaults", handler)
				}
			})
		}
	}
}

func TestInstallationReplayRawCastOrdering(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	catalog.InstallBuiltinTypes(source.State, sqltype.Builtins(), source.Bootstrap)
	recipe := replayCohort(t, source, 50000, "", true, false)
	cast := &catalog.Cast{OID: 70000, ObjectRevision: 1, Source: sqltype.Xid, Target: sqltype.Float4, Context: "e", Method: "b"}
	source.State.SetDescriptor(cast.Address(), cast)
	installation := dump.Event{OID: recipe.Cohort.ObjectID}
	declaration := dump.Event{OID: cast.OID}
	consumer := dump.Event{OID: 80000}
	graph := dump.Graph{installation: nil, declaration: {installation}, consumer: {installation}}
	script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView, Graph: graph}, rawFloat4: true}
	if err := script.prepareCastWindows(); err != nil {
		t.Fatal(err)
	}
	if len(graph[installation]) != 0 || !slices.Contains(graph[consumer], declaration) {
		t.Fatal("raw codec dependency inverted the installation prefix or missed its consumer", graph)
	}
	order, err := dump.Order(context.Background(), graph, nil)
	if err != nil {
		t.Fatal("real prefix and raw CAST produced an artificial cycle", err)
	}
	if slices.Index(order, installation) >= slices.Index(order, declaration) || slices.Index(order, declaration) >= slices.Index(order, consumer) {
		t.Fatal("installation, raw codec and consumer phases reordered", order)
	}
}
