package core_test

import (
	"slices"
	"strconv"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

func ownershipUnitInstall(t *testing.T, name string) (*core.Engine, *catalog.Extension) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	if name == "earthdistance" {
		enginetest.RoleExecute(t, e, op, "CREATE EXTENSION cube")
	}
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION "+`"`+name+`"`)
	v := e.CatalogRead()
	return e, v.MustExtension(v.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: name}))
}

func TestExtensionOwnershipIdentity(t *testing.T) {
	t.Run("canonical_shared_remap", ownershipCanonicalSharedRemap)
	t.Run("canonical_release_mutation_and_deletion", ownershipCanonicalReleaseMutation)
	t.Run("earth_scalar_real_helper_dependencies", ownershipEarthScalarDependencies)
	t.Run("earth_declaration_placement", ownershipEarthDeclarationPlacement)
	t.Run("configuration_membership_generation", ownershipConfigurationIdentity)
	t.Run("parser_membership_authority", ownershipParserMembershipAuthority)
	t.Run("parser_remap", ownershipParserRemap)
	t.Run("snowball_intrinsic_membership", ownershipSnowballIdentity)
	t.Run("tablefunc_detached_foreign_absent", ownershipTablefuncIdentity)
	t.Run("pageinspect_detached_foreign_absent", ownershipPageinspectIdentity)
	t.Run("pageinspect_canonical_shared_remap", ownershipPageinspectCanonicalRemap)
	t.Run("connectby_detached_foreign_absent", ownershipConnectbyIdentity)
	t.Run("connectby_canonical_shared_remap", ownershipConnectbyCanonicalRemap)
	t.Run("routine_carrier_membership", ownershipRoutineCarrierMembership)
	// Mutable membership order must never select a provider implementation slot.
	for _, name := range []string{"unaccent", "uuid-ossp", "fuzzystrmatch", "pgcrypto", "hstore", "cube", "earthdistance", "citext", "intarray", "intagg", "ltree", "seg", "isn", "lo", "pgrowlocks", "amcheck", "tablefunc", "pageinspect"} {
		t.Run(name, func(t *testing.T) {
			e, ext := ownershipUnitInstall(t, name)
			base := e.CatalogRead()
			recipe := ext.Installation
			if recipe == nil || !recipe.Valid() || len(recipe.Members) != len(recipe.Declarations) {
				t.Fatal("missing sealed release recipe")
			}
			for declaration, a := range recipe.Members {
				d := base.Get(a)
				origin := catalog.DescriptorOrigin(d)
				canonical := recipe.Declarations[declaration]
				if origin == nil || origin.Installation != recipe || origin.Declaration != declaration || !origin.Valid(a) || canonical == nil || canonical.Address() != a || catalog.DescriptorOrigin(canonical) != nil {
					t.Fatal("canonical declaration became mutable provider authority", declaration)
				}
			}
			for _, a := range ext.Members {
				if !slices.Contains(recipe.Members, a) && catalog.DescriptorOrigin(base.Get(a)) != nil {
					t.Fatal("generated companion acquired a manufactured script slot", a)
				}
			}
			state := catalog.CloneState(base.State())
			changed := *ext
			changed.Members = slices.Clone(ext.Members)
			slices.Reverse(changed.Members)
			state.SetDescriptor(ext.Address(), &changed)
			view := e.Catalog.Read(state)
			for _, a := range ext.Members {
				p, ok := base.Procedure(a)
				if !ok {
					continue
				}
				before, err := extension.ProcedureSymbol(base, p)
				if err != nil {
					t.Fatal("installed declaration baseline", err)
				}
				after, err := extension.ProcedureSymbol(view, p)
				if err != nil || after != before {
					t.Fatal("membership reorder changed provider declaration", a, before, after, err)
				}
				if view.MustProcedure(a).Implementation != p.Implementation || view.MustProcedure(a).Installed != p.Installed {
					t.Fatal("membership changed typed declaration")
				}
			}
			if err := (extension.CatalogProvider{Class: ids.ExtensionClassID}).Validate(view, &changed); err != nil {
				t.Fatal("valid reciprocal member bag rejected", err)
			}
			// The supplied member bag must also include every descriptor whose
			// current e-link claims this extension, independently of recipe size.
			incomplete := changed
			incomplete.Members = changed.Members[1:]
			if err := extension.ValidateCurrentInstallation(view, &incomplete, ext.Installation.Implementation); err == nil {
				t.Fatal("truncated current membership admitted")
			}
			incomplete.Members = nil
			if err := extension.ValidateCurrentInstallation(view, &incomplete, ext.Installation.Implementation); err == nil {
				t.Fatal("empty current membership admitted while reciprocal links remain")
			}
			if !slices.Equal(ext.Members, base.MustExtension(ext.Address()).Members) {
				t.Fatal("source inventory mutated")
			}
		})
	}
	t.Run("detached_renamed_reattached_and_absent_origin", func(t *testing.T) {
		e, ext := ownershipUnitInstall(t, "fuzzystrmatch")
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		enginetest.RoleExecute(t, e, op, `CREATE EXTENSION "uuid-ossp"`)
		base := e.CatalogRead()
		p := base.MustProcedure(ext.Members[0])
		symbol, err := extension.ProcedureSymbol(base, p)
		if err != nil {
			t.Fatal(err)
		}
		state := catalog.CloneState(base.State())
		detached := *p
		detached.Extension = ids.ObjectAddress{}
		detached.ObjectName = "detached_provider"
		state.UnbindName(catalog.DescriptorNameKey(p))
		state.SetDescriptor(p.Address(), &detached)
		state.BindName(catalog.DescriptorNameKey(&detached), p.Address())
		changed := *ext
		changed.Members = slices.Clone(ext.Members[1:])
		state.SetDescriptor(ext.Address(), &changed)
		view := e.Catalog.Read(state)
		if got, err := extension.ProcedureSymbol(view, &detached); err != nil || got != symbol {
			t.Fatal("detachment/rename erased origin", got, err)
		}
		other := base.MustExtension(base.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "uuid-ossp"}))
		reattachedState := catalog.CloneState(state)
		attached := detached
		attached.Extension = other.Address()
		nextOther := *other
		nextOther.Members = append(slices.Clone(other.Members), attached.Address())
		reattachedState.SetDescriptor(attached.Address(), &attached)
		reattachedState.SetDescriptor(other.Address(), &nextOther)
		if got, err := extension.ProcedureSymbol(e.Catalog.Read(reattachedState), &attached); err != nil || got != symbol {
			t.Fatal("foreign membership changed installed provider", got, err)
		}
		// The original installation descriptor is not execution authority. This
		// checks only the surviving procedure owner, not validity of unrelated
		// descriptors in this deliberately unpublished unit candidate.
		state.UnbindName(catalog.DescriptorNameKey(ext))
		state.RemoveDescriptor(ext.Address())
		if got, err := extension.ProcedureSymbol(e.Catalog.Read(state), &detached); err != nil || got != symbol {
			t.Fatal("absent installation erased declaration", got, err)
		}
		for _, mutate := range []func(*catalog.Procedure){func(p *catalog.Procedure) { p.Signature = "23" }, func(p *catalog.Procedure) { p.Implementation = extension.StockPgcrypto }} {
			bad := detached
			mutate(&bad)
			invalid := catalog.CloneState(state)
			invalid.SetDescriptor(bad.Address(), &bad)
			if _, err := extension.ProcedureSymbol(e.Catalog.Read(invalid), &bad); err == nil {
				t.Fatal("forged executable declaration accepted")
			}
		}
		if p.Extension != ext.Address() || p.Name() == detached.Name() {
			t.Fatal("detached copy mutated captured source")
		}
	})
}

func TestExtensionOwnershipMembership(t *testing.T) {
	e, ext := ownershipUnitInstall(t, "fuzzystrmatch")
	base := e.CatalogRead()
	p := base.MustProcedure(ext.Members[0])
	state := catalog.CloneState(base.State())
	moved := *p
	// Existing schema identity, deliberately different from extnamespace.
	for _, d := range base.Descriptors() {
		if n, ok := d.(*catalog.Namespace); ok && n.Name() == "pg_catalog" {
			moved.ObjectNamespace = n.Address()
		}
	}
	state.UnbindName(catalog.DescriptorNameKey(p))
	state.SetDescriptor(p.Address(), &moved)
	state.BindName(catalog.DescriptorNameKey(&moved), p.Address())
	if err := catalog.ValidateExtensionMembership(e.Catalog.Read(state), &moved); err != nil {
		t.Fatal("different-schema reciprocal member rejected", err)
	}
	broken := *ext
	broken.Members = slices.Clone(ext.Members[1:])
	state.SetDescriptor(ext.Address(), &broken)
	if err := catalog.ValidateExtensionMembership(e.Catalog.Read(state), &moved); err == nil {
		t.Fatal("missing reciprocal member accepted")
	}
	duplicate := *ext
	duplicate.Members = append(slices.Clone(ext.Members), p.Address())
	state.SetDescriptor(ext.Address(), &duplicate)
	if err := (extension.CatalogProvider{Class: ids.ExtensionClassID}).Validate(e.Catalog.Read(state), &duplicate); err == nil {
		t.Fatal("duplicate direct member accepted")
	}
}

func ownershipCanonicalSharedRemap(t *testing.T) {
	e, ext := ownershipUnitInstall(t, "cube")
	view := e.CatalogRead()
	recipe := ext.Installation
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 930000}, ext.TargetNamespace: {ClassID: ids.NamespaceClassID, ObjectID: 930001}}
	for i, a := range ext.Members {
		mapping[a] = ids.ObjectAddress{ClassID: a.ClassID, ObjectID: ids.OID(930100 + i)}
	}
	remap := extension.DescriptorRemapper(mapping)
	mapped := remap(ext).(*catalog.Extension)
	next := mapped.Installation
	if next == recipe || !next.Valid() || next.Cohort != mapping[ext.Address()] || next.Implementation != recipe.Implementation || next.Version != recipe.Version {
		t.Fatal("logical cohort remap lost sealed release")
	}
	base := view.MustType(recipe.Members[0])
	mappedBase := mapping[base.Address()]
	if canonical := next.Declarations[0].(*catalog.Type); canonical.Address() != mappedBase || canonical.SQLType.ID() != sqltype.ID(mappedBase.ObjectID) || canonical.Array != sqltype.ID(mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)}].ObjectID) || canonical.Namespace() != mapping[ext.TargetNamespace] || canonical.Origin != nil {
		t.Fatal("canonical nominal graph was not remapped", canonical)
	}
	output := false
	for i, a := range recipe.Members {
		d := remap(view.Get(a))
		origin := catalog.DescriptorOrigin(d)
		if origin == nil || origin.Installation != next || origin.Declaration != i || !origin.Valid(mapping[a]) || next.Members[i] != mapping[a] || next.Declarations[i].Address() != mapping[a] || catalog.DescriptorOrigin(next.Declarations[i]) != nil {
			t.Fatal("one graph acquired per-object or incoherent canonical recipes", i)
		}
		if p, ok := next.Declarations[i].(*catalog.Procedure); ok && p.Name() == "cube_out" {
			output = true
			if p.Signature != catalog.ProcedureSignature(strconv.FormatUint(uint64(mappedBase.ObjectID), 10)) {
				t.Fatal("canonical input signature retained source type", p.Signature)
			}
		}
		if recipe.Declarations[i].Address() != a || catalog.DescriptorOrigin(recipe.Declarations[i]) != nil || catalog.DescriptorOrigin(view.Get(a)).Installation != recipe {
			t.Fatal("remapping mutated source recipe", i)
		}
	}
	if !output {
		t.Fatal("missing genuine output declaration")
	}
	if extension.DescriptorRemapper(nil)(ext) != ext {
		t.Fatal("identity remap copied immutable backing")
	}
	forged := *recipe
	if forged.Valid() {
		t.Fatal("copied release record forged a seal")
	}
	invalid := *base
	origin := *base.Origin
	origin.Installation = &forged
	invalid.Origin = &origin
	mappedInvalid := remap(&invalid)
	if catalog.DescriptorOrigin(mappedInvalid).Valid(mappedInvalid.Address()) {
		t.Fatal("identity remap minted authority from an unsealed source")
	}
}

func ownershipCanonicalReleaseMutation(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, `CREATE EXTENSION fuzzystrmatch VERSION '1.1'`)
	before := e.CatalogRead()
	a := before.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "fuzzystrmatch"})
	ext := before.MustExtension(a)
	recipe := ext.Installation
	p := before.MustProcedure(before.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ext.TargetNamespace, Name: "levenshtein", Signature: "25,25"}))
	declaration := p.Origin.Declaration
	canonical := recipe.Declarations[declaration].(*catalog.Procedure)
	for _, q := range []string{`CREATE ROLE eo_canonical_owner`, `ALTER EXTENSION fuzzystrmatch DROP FUNCTION levenshtein(text,text)`, `ALTER FUNCTION levenshtein(text,text) RENAME TO eo_survivor`, `ALTER FUNCTION eo_survivor(text,text) OWNER TO eo_canonical_owner`, `CREATE FUNCTION eo_extra(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'`, `ALTER EXTENSION fuzzystrmatch ADD FUNCTION eo_extra(integer)`, `ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2'`} {
		enginetest.RoleExecute(t, e, op, q)
	}
	current := e.CatalogRead()
	updated := current.MustExtension(a)
	survivor := current.MustProcedure(p.Address())
	// Declaration slots belong to each release recipe: 1.2 prepends its new
	// function, while the survivor retains its exact 1.1 recipe and slot.
	var released catalog.Descriptor
	for i, address := range updated.Installation.Members {
		if address == p.Address() {
			released = updated.Installation.Declarations[i]
		}
	}
	if updated.Version != "1.2" || updated.Installation == recipe || updated.Installation.Cohort != recipe.Cohort || released != canonical || canonical.Name() != "levenshtein" || canonical.Owner != p.Owner || canonical.Namespace() != p.Namespace() || canonical.Origin != nil {
		t.Fatal("UPDATE rewrote canonical pre-origin metadata")
	}
	if survivor.Name() != "eo_survivor" || survivor.Owner == canonical.Owner || survivor.Extension != (ids.ObjectAddress{}) || survivor.Origin.Installation != recipe {
		t.Fatal("UPDATE reclaimed detached member or current owner")
	}
	if symbol, err := extension.ProcedureSymbol(current, survivor); err != nil || symbol != "levenshtein" {
		t.Fatal("detached prior release lost executable declaration", symbol, err)
	}
	extra := current.MustProcedure(current.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ext.TargetNamespace, Name: "eo_extra", Signature: "23"}))
	if extra.Origin != nil || extra.Extension != a {
		t.Fatal("ordinary UPDATE member acquired provider origin")
	}
	enginetest.RoleExecute(t, e, op, `DROP EXTENSION fuzzystrmatch`)
	after := e.CatalogRead()
	if after.Get(a) != nil || after.MustProcedure(p.Address()) != survivor || recipe.Declarations[declaration] != canonical || before.MustProcedure(p.Address()) != p {
		t.Fatal("retirement resurrected origin or mutated captured recipe")
	}
	if symbol, err := extension.ProcedureSymbol(after, survivor); err != nil || symbol != "levenshtein" {
		t.Fatal("deleted installation became execution authority", symbol, err)
	}
}

func ownershipEarthScalarDependencies(t *testing.T) {
	e, ext := ownershipUnitInstall(t, "earthdistance")
	view := e.CatalogRead()
	radius := view.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ext.TargetNamespace, Name: "earth"})
	if radius == (ids.ObjectAddress{}) {
		t.Fatal("missing genuine radius")
	}
	for _, name := range []string{"earth", "sec_to_gc", "gc_to_sec"} {
		signature := ""
		if name != "earth" {
			signature = "701"
		}
		p := view.MustProcedure(view.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ext.TargetNamespace, Name: name, Signature: signature}))
		if symbol, err := extension.ProcedureSymbol(view, p); err != nil || symbol != name {
			t.Fatal("scalar lost immutable typed declaration", name, symbol, err)
		}
		var dependencies []ids.ObjectAddress
		catalog.VisitProcedureCapturedDefinitionDependencies(view, p, func(address ids.ObjectAddress) bool { dependencies = append(dependencies, address); return true })
		if name == "earth" {
			if len(dependencies) != 0 {
				t.Fatal("radius invented helper dependency")
			}
		} else if len(dependencies) != 1 || dependencies[0] != radius {
			t.Fatal("released scalar lost real radius dependency", name, dependencies)
		}
		calls := 0
		completed := catalog.VisitProcedureCapturedDefinitionDependencies(view, p, func(ids.ObjectAddress) bool { calls++; return false })
		if name == "earth" {
			if !completed || calls != 0 {
				t.Fatal("radius invented visited dependency")
			}
		} else if completed || calls != 1 {
			t.Fatal("released helper visitor ignored early stop", name, completed, calls)
		}
	}
}

func ownershipEarthDeclarationPlacement(t *testing.T) {
	e, ext := ownershipUnitInstall(t, "earthdistance")
	view := e.CatalogRead()
	recipe := ext.Installation
	schema, cube, err := extension.EarthInstallationCubePlacement(recipe)
	if err != nil || schema != "public" || cube != view.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ext.TargetNamespace, Name: "cube"}) {
		t.Fatal("genuine declaration lost identity-bound placement", schema, cube, err)
	}
	copied := *recipe
	if _, _, err := extension.EarthInstallationCubePlacement(&copied); err == nil {
		t.Fatal("copied recipe granted declaration placement")
	}
	p := view.MustProcedure(recipe.Members[2+int(sqltype.EarthLL)])
	for _, corruption := range []string{"body_pointer", "copied_origin", "missing_origin"} {
		state := catalog.CloneState(view.State())
		current := *p
		switch corruption {
		case "body_pointer":
			retained, ok := proc.RoutineDeclarationRecipe(p.DeclarationBody)
			if !ok {
				t.Fatal("missing genuine typed declaration recipe")
			}
			foreign := proc.DeclarationRecipe{Return: retained.Return(), Parts: make([]proc.DeclarationPart, retained.Len())}
			for i := range retained.Len() {
				foreign.Parts[i] = retained.At(i)
			}
			current.DeclarationBody, err = proc.NewRoutineDeclarationBody(foreign)
			if err != nil {
				t.Fatal(err)
			}
			if current.DeclarationBody == p.DeclarationBody {
				t.Fatal("foreign declaration reused genuine owner")
			}
			originalTree, originalErr := p.DeclarationBody.StructuralOutput(t.Context())
			foreignTree, foreignErr := current.DeclarationBody.StructuralOutput(t.Context())
			if originalErr != nil || foreignErr != nil || originalTree != foreignTree {
				t.Fatal("foreign-owner witness changed declaration structure", originalErr, foreignErr)
			}
		case "copied_origin":
			origin := *p.Origin
			origin.Installation = &copied
			current.Origin = &origin
		case "missing_origin":
			current.Origin = nil
		}
		state.SetDescriptor(current.Address(), &current)
		if _, err := extension.ProcedureSymbol(view.WithState(state), &current); err == nil {
			t.Fatal("declaration placement weakened immutable authority", corruption)
		}
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE SCHEMA cube_current")
	enginetest.RoleExecute(t, e, op, "ALTER EXTENSION cube SET SCHEMA cube_current")
	after, sameCube, err := extension.EarthInstallationCubePlacement(recipe)
	if err != nil || after != schema || sameCube != cube {
		t.Fatal("mutable placement rewrote captured declaration", after, sameCube, err)
	}
}

func ownershipRoutineCarrierMembership(t *testing.T) {
	e, ext := ownershipUnitInstall(t, "intagg")
	base := e.CatalogRead()
	a := base.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ext.TargetNamespace, Name: "int_array_enum", Signature: "1007"})
	captured := base.MustProcedure(a)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{
		`ALTER EXTENSION intagg DROP FUNCTION int_array_enum(integer[])`,
		`CREATE EXTENSION "uuid-ossp"`,
		`ALTER EXTENSION "uuid-ossp" ADD FUNCTION int_array_enum(integer[])`,
	} {
		enginetest.RoleExecute(t, e, op, query)
		current := e.CatalogRead()
		if p, err := exec.ValidateStoredRoutineDescriptor(current, captured); err != nil || p != current.MustProcedure(a) {
			t.Fatal("current membership invalidated original routine declaration", err)
		}
	}
	current := e.CatalogRead().MustProcedure(a)
	originCopy := *current.Origin
	equivalent := *current
	equivalent.Origin = &originCopy
	if !exec.SameRoutineDeclaration(current, &equivalent) {
		t.Fatal("equivalent carrier lost the same release authority")
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 900000}}
	first := extension.DescriptorRemapper(mapping)(current).(*catalog.Procedure)
	second := extension.DescriptorRemapper(mapping)(current).(*catalog.Procedure)
	if first.Origin.Installation == second.Origin.Installation || !exec.SameRoutineDeclaration(first, second) {
		t.Fatal("independent genuine remaps lost logical release identity")
	}
	for _, corrupt := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Origin = nil },
		func(p *catalog.Procedure) {
			origin := *p.Origin
			forged := *origin.Installation
			origin.Installation = &forged
			p.Origin = &origin
		},
		func(p *catalog.Procedure) {
			origin := *p.Origin
			origin.Declaration = (origin.Declaration + 1) % len(origin.Installation.Members)
			p.Origin = &origin
		},
		func(p *catalog.Procedure) {
			origin := *p.Origin
			old := origin.Installation
			other := catalog.NewInstallation(old.Cohort, old.Provider, "other-release", old.Implementation, old.Members)
			other.Declarations = old.Declarations
			origin.Installation = other
			p.Origin = &origin
		},
	} {
		forged := *current
		corrupt(&forged)
		if exec.SameRoutineDeclaration(current, &forged) {
			t.Fatal("carrier comparison accepted changed released authority")
		}
	}
	if base.MustProcedure(a) != captured || captured.Extension != ext.Address() {
		t.Fatal("membership changed captured declaration")
	}
}
