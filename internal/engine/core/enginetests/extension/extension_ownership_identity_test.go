package extension_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
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
		catalog.VisitProcedureCapturedDefinitionDependencies(view, view, p, func(address ids.ObjectAddress) bool { dependencies = append(dependencies, address); return true })
		if name == "earth" {
			if len(dependencies) != 0 {
				t.Fatal("radius invented helper dependency")
			}
		} else if len(dependencies) != 1 || dependencies[0] != radius {
			t.Fatal("released scalar lost real radius dependency", name, dependencies)
		}
		calls := 0
		completed := catalog.VisitProcedureCapturedDefinitionDependencies(view, view, p, func(ids.ObjectAddress) bool { calls++; return false })
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

func ownershipConfigurationIdentity(t *testing.T) {
	e, ext := ownershipUnitInstall(t, "unaccent")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, `CREATE TEXT SEARCH CONFIGURATION public.eo_config (COPY=pg_catalog.simple)`)
	enginetest.RoleExecute(t, e, op, `CREATE TEXT SEARCH DICTIONARY public.eo_config_dict (TEMPLATE=pg_catalog.simple)`)
	enginetest.RoleExecute(t, e, op, `ALTER TEXT SEARCH CONFIGURATION public.eo_config ALTER MAPPING FOR asciiword WITH public.eo_config_dict`)
	before := e.CatalogRead()
	a := before.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ext.TargetNamespace, Name: "eo_config"})
	original := before.MustTSConfiguration(a)
	if len(original.Mappings) != 19 || original.Extension != (ids.ObjectAddress{}) {
		t.Fatal("unexpected genuine copied configuration", original)
	}
	closure, err := catalog.ExtensionMemberClosure(before, original)
	if err != nil || len(closure) != 1 || closure[0] != original {
		t.Fatal("mapping dependencies became direct companion membership", closure, err)
	}
	enginetest.RoleExecute(t, e, op, `ALTER EXTENSION unaccent ADD TEXT SEARCH CONFIGURATION public.eo_config`)
	attachedView := e.CatalogRead()
	attached := attachedView.MustTSConfiguration(a)
	if attached == original || attached.Extension != ext.Address() || attached.Parser != original.Parser || attached.Owner != original.Owner || &attached.Mappings[0] != &original.Mappings[0] {
		t.Fatal("membership copied/mutated immutable configuration payload")
	}
	if err := (catalog.TSConfigurationProvider{}).Validate(attachedView, attached); err != nil {
		t.Fatal(err)
	}
	candidate := catalog.CloneState(attachedView.State())
	broken := *attachedView.MustExtension(ext.Address())
	broken.Members = slices.Clone(broken.Members[:len(broken.Members)-1])
	candidate.SetDescriptor(broken.Address(), &broken)
	if err := (catalog.TSConfigurationProvider{}).Validate(attachedView.WithState(candidate), attached); err == nil {
		t.Fatal("missing reciprocal configuration member accepted")
	}
	enginetest.RoleExecute(t, e, op, `CREATE SCHEMA eo_config_destination`)
	enginetest.RoleExecute(t, e, op, `ALTER EXTENSION unaccent SET SCHEMA eo_config_destination`)
	moved := e.CatalogRead().MustTSConfiguration(a)
	if moved.Namespace() == original.Namespace() || moved.Extension != ext.Address() || moved.Parser != original.Parser || moved.Owner != original.Owner || &moved.Mappings[0] != &original.Mappings[0] || before.MustTSConfiguration(a) != original {
		t.Fatal("mechanical move changed mapping/parser/owner or captured predecessor")
	}
	dictionary := before.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ext.TargetNamespace, Name: "eo_config_dict"})
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 940001}, a: {ClassID: ids.TSConfigurationClassID, ObjectID: 940002}, moved.Namespace(): {ClassID: ids.NamespaceClassID, ObjectID: 940003}}
	mapping[dictionary] = ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 940004}
	remapped := extension.RemapDescriptor(moved, mapping).(*catalog.TSConfiguration)
	if remapped.Address() != mapping[a] || remapped.Extension != mapping[ext.Address()] || remapped.Namespace() != mapping[moved.Namespace()] || remapped.Owner != moved.Owner || remapped.Parser != moved.Parser || len(remapped.Mappings) != len(moved.Mappings) {
		t.Fatal("configuration remap lost logical membership or mapping identity")
	}
	found := false
	for i, m := range moved.Mappings {
		if remapped.Mappings[i].TokenID != m.TokenID || len(remapped.Mappings[i].Dictionaries) != len(m.Dictionaries) {
			t.Fatal("configuration remap changed mapping shape")
		}
		for j, source := range m.Dictionaries {
			want := source
			if next, ok := mapping[source]; ok {
				want = next
				found = true
			}
			if remapped.Mappings[i].Dictionaries[j] != want {
				t.Fatal("configuration remap lost ordered dictionary identity")
			}
		}
	}
	if !found || before.MustTSConfiguration(a).Mappings[0].TokenID != original.Mappings[0].TokenID {
		t.Fatal("genuine custom dictionary remap was not exercised")
	}
}

func ownershipParserMembershipAuthority(t *testing.T) {
	e, ext, original, configuration := ownershipParserFixture(t)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	before := e.CatalogRead()
	provider := catalog.TSParserProvider{}
	closure, err := catalog.ExtensionMemberClosure(before, original)
	if err != nil || len(closure) != 1 || closure[0] != original {
		t.Fatal("callbacks/configuration became members", closure, err)
	}
	enginetest.RoleExecute(t, e, op, `ALTER EXTENSION unaccent ADD TEXT SEARCH PARSER public.eo_parser`)
	view := e.CatalogRead()
	d := view.MustTSParser(original.Address())
	if d == original || d.Extension != ext.Address() || d.Revision() <= original.Revision() || original.Extension != (ids.ObjectAddress{}) || before.Get(d.Address()) != original || view.Get(configuration.Address()) != configuration {
		t.Fatal("membership mutated old backing or configuration")
	}
	payload := *d
	payload.Extension, payload.ObjectRevision = original.Extension, original.ObjectRevision
	if payload != *original || d.Headline != 0 {
		t.Fatal("membership changed callback payload")
	}
	owner, acl, columns := access.DescriptorACL(d)
	if owner != (authorization.PrincipalRef{}) || acl.Explicit || len(acl.Entries) != 0 || columns != nil || access.ClassPrivileges(d) != 0 || catalog.DescriptorOrigin(d) != nil {
		t.Fatal("ordinary parser acquired owner/ACL/provenance")
	}
	copyOutside := *d
	if err := provider.Validate(view, &copyOutside); err == nil {
		t.Fatal("copied descriptor outside supplied view admitted")
	}
	if _, err := catalog.ResolveParserRuntime(view, &copyOutside); err == nil {
		t.Fatal("runtime bypassed supplied-view residency")
	}
	withoutHeader := catalog.CloneState(view.State())
	withoutHeader.RemoveDescriptor(ext.Address())
	headerless := view.WithState(withoutHeader)
	if err := provider.Validate(headerless, d); err == nil {
		t.Fatal("publication admitted a missing reciprocal header")
	}
	if _, err := catalog.ResolveParserRuntime(headerless, d); err != nil {
		t.Fatal("runtime acquired live e-header gate", err)
	}
	state := catalog.CloneState(view.State())
	resident := *d
	resident.ObjectRevision++
	state.SetDescriptor(d.Address(), &resident)
	current := view.WithState(state)
	if err := provider.Validate(current, &resident); err != nil {
		t.Fatal("genuine revised resident rejected", err)
	}
	if err := provider.Validate(current, d); err == nil {
		t.Fatal("old descriptor admitted in replacement view")
	}
	// The local detached descriptor is valid; only its unchanged parent's graph
	// is stale. Runtime admission must not grow a live extension-header gate.
	detached := resident
	detached.Extension = ids.ObjectAddress{}
	state.SetDescriptor(d.Address(), &detached)
	if err := provider.Validate(current, &detached); err != nil {
		t.Fatal("lawful local detach rejected", err)
	}
	if err := catalog.ValidateExtensionMembership(current, current.MustExtension(ext.Address())); err == nil {
		t.Fatal("stale parent reciprocal graph admitted")
	}
	if _, err := catalog.ResolveParserRuntime(current, &detached); err != nil {
		t.Fatal("runtime acquired live e-header requirement", err)
	}
	parent := *current.MustExtension(ext.Address())
	parent.Members = slices.DeleteFunc(slices.Clone(parent.Members), func(a ids.ObjectAddress) bool { return a == d.Address() })
	state.SetDescriptor(parent.Address(), &parent)
	if err := catalog.ValidateExtensionMembership(current, &parent); err != nil {
		t.Fatal("coherent detach rejected", err)
	}
	enginetest.RoleExecute(t, e, op, `CREATE EXTENSION fuzzystrmatch`)
	foreignView := e.CatalogRead()
	foreign := foreignView.MustExtension(foreignView.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "fuzzystrmatch"}))
	foreignParent := *foreign
	foreignParser := detached
	foreignParser.Extension = foreign.Address()
	foreignParent.Members = append(slices.Clone(foreign.Members), d.Address())
	state = catalog.CloneState(foreignView.State())
	current = foreignView.WithState(state)
	state.SetDescriptor(parent.Address(), &parent)
	state.SetDescriptor(foreign.Address(), &foreignParent)
	state.BindName(catalog.DescriptorNameKey(&foreignParent), foreign.Address())
	state.SetDescriptor(d.Address(), &foreignParser)
	if err := provider.Validate(current, &foreignParser); err != nil {
		t.Fatal("coherent foreign membership rejected", err)
	}
	if err := catalog.ValidateExtensionMembership(current, &foreignParent); err != nil {
		t.Fatal(err)
	}
	// Mutate a real represented callback's signature/result, not a fake loader
	// declaration. dsimple_init has exactly the lextypes SQL signature.
	for _, signature := range []bool{true, false} {
		candidate := catalog.CloneState(view.State())
		callback := *view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3725})
		bad := *d
		bad.LexType = callback.Address().ObjectID
		candidate.SetDescriptor(bad.Address(), &bad)
		candidate.SetDescriptor(callback.Address(), &callback)
		checked := view.WithState(candidate)
		if err := provider.Validate(checked, &bad); err != nil {
			t.Fatal("real callback baseline", err)
		}
		if signature {
			callback.Signature = "23"
		} else {
			callback.Result = 23
		}
		if err := provider.Validate(checked, &bad); err == nil {
			t.Fatal("corrupt genuine callback admitted")
		}
	}
	ownershipParserOwnerCodec(t, e, d)
	enginetest.RoleExecute(t, e, op, `CREATE SCHEMA eo_parser_destination`)
	enginetest.RoleExecute(t, e, op, `CREATE TEXT SEARCH PARSER eo_parser_destination.eo_parser (START=pg_catalog.prsd_start, GETTOKEN=pg_catalog.prsd_nexttoken, END=pg_catalog.prsd_end, LEXTYPES=pg_catalog.prsd_lextype)`)
	atomic := e.CatalogRead()
	err = extensionUnitExecute(t, e, op, enginetest.Statement(t, `ALTER EXTENSION unaccent SET SCHEMA eo_parser_destination`))
	enginetest.State(t, err, "42710")
	for a, old := range atomic.Descriptors() {
		if e.CatalogRead().Get(a) != old {
			t.Fatal("late parser collision published earlier moves", a)
		}
	}
	// Exercise the actual generic lower gate with a genuine nonsuperuser. The
	// same-namespace return precedes it; a collision must not mask it.
	actor := roleAttributeCreate(t, e, op, "eo_parser_actor", true, parser.RoleOptions{})
	enginetest.RoleExecute(t, e, op, `GRANT CREATE ON SCHEMA eo_parser_destination TO eo_parser_actor`)
	actorOp, actorDone := enginetest.RoleOperation(t, e, actor.Name)
	defer actorDone()
	e.Mu.Lock()
	auth, authErr := e.AuthorizationLocked(actorOp)
	if authErr != nil {
		e.Mu.Unlock()
		t.Fatal(authErr)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, actorOp.Session)
	host := schemacmds.SchemaHost{DDLHost: core.DDLHost{E: e, Op: actorOp, CatalogStage: stage}}
	_, sameErr := ddl.StageObjectNamespace(host, auth, d, d.Namespace())
	destination := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "eo_parser_destination"})
	_, moveErr := ddl.StageObjectNamespace(host, auth, d, destination)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if sameErr != nil {
		t.Fatal("same-namespace lower move was not a no-op", sameErr)
	}
	enginetest.State(t, moveErr, "42501")
	if !strings.Contains(moveErr.Error(), "must be superuser to set schema of text search parser") || stage.DescriptorCount() != 0 || e.CatalogRead().Get(d.Address()) != d {
		t.Fatal("lower authority/collision precedence or atomicity", moveErr)
	}
	if err := extensionUnitExecute(t, e, op, extensionUnitGrant(e.CatalogRead().MustDatabase(catalog.CurrentDatabaseAddress).Name(), actor.Name)); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, actorOp, `CREATE SCHEMA eo_parser_actor_schema`)
	enginetest.RoleExecute(t, e, actorOp, `CREATE EXTENSION "uuid-ossp" SCHEMA eo_parser_actor_schema`)
	enginetest.RoleExecute(t, e, op, `CREATE TEXT SEARCH PARSER eo_parser_actor_schema.owned_schema_parser (START=pg_catalog.prsd_start, GETTOKEN=pg_catalog.prsd_nexttoken, END=pg_catalog.prsd_end, LEXTYPES=pg_catalog.prsd_lextype)`)
	for _, action := range []string{"ADD", "DROP"} {
		if action == "DROP" {
			enginetest.RoleExecute(t, e, op, `ALTER EXTENSION "uuid-ossp" ADD TEXT SEARCH PARSER eo_parser_actor_schema.owned_schema_parser`)
		}
		snapshot := e.CatalogRead()
		err := extensionUnitExecute(t, e, actorOp, enginetest.Statement(t, `ALTER EXTENSION "uuid-ossp" `+action+` TEXT SEARCH PARSER eo_parser_actor_schema.owned_schema_parser`))
		enginetest.State(t, err, "42501")
		if err.Error() != "stuntgres: must be superuser" {
			t.Fatal("membership invented parser owner or ordinary CREATE diagnostic", err)
		}
		for a, old := range snapshot.Descriptors() {
			if e.CatalogRead().Get(a) != old {
				t.Fatal("denied membership published", action, a)
			}
		}
	}
	seed := e.CatalogRead().MustTSParser(parserSeedAddress)
	enginetest.RoleExecute(t, e, op, `ALTER EXTENSION unaccent ADD TEXT SEARCH PARSER pg_catalog.default`)
	defer enginetest.RoleExecute(t, e, op, `ALTER EXTENSION unaccent DROP TEXT SEARCH PARSER pg_catalog.default`)
	for _, mode := range []parser.DropMode{parser.DropRestrict, parser.DropCascade} {
		failed, err := parserStage(t, e, op, &parser.DropParser{Names: []parser.ObjectName{{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: "default"}}}}, Mode: mode})
		e.DiscardCatalogStage(failed)
		enginetest.State(t, err, "2BP01")
		if !strings.Contains(err.Error(), "because it is required by the database system") {
			t.Fatal("e membership masked pinned target rejection", err)
		}
	}
	seedMember := e.CatalogRead().MustTSParser(parserSeedAddress)
	payload = *seedMember
	payload.Extension, payload.ObjectRevision = seed.Extension, seed.ObjectRevision
	if payload != *seed || seedMember.Extension != ext.Address() {
		t.Fatal("seed membership changed fixed parser definition")
	}
}

func ownershipParserRemap(t *testing.T) {
	e, ext, original, configuration := ownershipParserFixture(t)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, `ALTER EXTENSION unaccent ADD TEXT SEARCH PARSER public.eo_parser`)
	enginetest.RoleExecute(t, e, op, `CREATE SCHEMA eo_parser_moved`)
	enginetest.RoleExecute(t, e, op, `ALTER EXTENSION unaccent SET SCHEMA eo_parser_moved`)
	view := e.CatalogRead()
	d := view.MustTSParser(original.Address())
	if d.Namespace() == original.Namespace() || d.Extension != ext.Address() || d.Start != original.Start || d.Token != original.Token || d.End != original.End || d.Headline != 0 || d.LexType != original.LexType || view.Get(configuration.Address()) != configuration {
		t.Fatal("mechanical move changed callbacks/configuration")
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{
		ext.Address():           {ClassID: ids.ExtensionClassID, ObjectID: 950001},
		d.Address():             {ClassID: ids.TSParserClassID, ObjectID: 950002},
		d.Namespace():           {ClassID: ids.NamespaceClassID, ObjectID: 950003},
		configuration.Address(): {ClassID: ids.TSConfigurationClassID, ObjectID: 950004},
	}
	for i, oid := range []ids.OID{d.Start, d.Token, d.End, d.LexType} {
		mapping[ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(950010 + i)}
	}
	remap := extension.DescriptorRemapper(mapping)
	mapped := remap(d).(*catalog.TSParser)
	mappedConfiguration := remap(configuration).(*catalog.TSConfiguration)
	mappedParent := remap(view.MustExtension(ext.Address())).(*catalog.Extension)
	if mapped.Address() != mapping[d.Address()] || mapped.Namespace() != mapping[d.Namespace()] || mapped.Extension != mappedParent.Address() || !slices.Contains(mappedParent.Members, mapped.Address()) || mappedConfiguration.Parser != mapped.Address() || mapped.Headline != 0 {
		t.Fatal("shared parser/member/configuration remap diverged")
	}
	for i, oid := range []ids.OID{mapped.Start, mapped.Token, mapped.End, mapped.LexType} {
		if oid != ids.OID(950010+i) {
			t.Fatal("callback identity did not follow shared map", i, oid)
		}
	}
	withHeadline := *d
	withHeadline.Headline = 3720
	mapping[ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3720}] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 950014}
	if got := extension.RemapDescriptor(&withHeadline, mapping).(*catalog.TSParser); got.Headline != 950014 {
		t.Fatal("nonzero optional callback not remapped")
	}
	fixed := extension.RemapDescriptor(d, map[ids.ObjectAddress]ids.ObjectAddress{d.Address(): mapping[d.Address()]}).(*catalog.TSParser)
	if fixed.Start != d.Start || fixed.Token != d.Token || fixed.End != d.End || fixed.LexType != d.LexType || fixed.Headline != 0 || original.Extension != (ids.ObjectAddress{}) || configuration.Parser != original.Address() {
		t.Fatal("remap mutated source or fixed callbacks")
	}
}

func ownershipSnowballIdentity(t *testing.T) {
	e, first := ownershipUnitInstall(t, "unaccent")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	before := e.CatalogRead()
	dictionary := snowballUnitDictionary(t, e)
	template, ok := before.TSTemplate(dictionary.Template)
	if !ok {
		t.Fatal("missing genuine Snowball template")
	}
	configuration := before.MustTSConfiguration(before.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: dictionary.Namespace(), Name: "english"}))
	addresses := []ids.ObjectAddress{template.Init, template.Lexize, template.Address(), dictionary.Address(), configuration.Address()}
	for _, sql := range []string{
		`ALTER EXTENSION unaccent ADD FUNCTION pg_catalog.dsnowball_init(internal)`,
		`ALTER EXTENSION unaccent ADD TEXT SEARCH DICTIONARY pg_catalog.english_stem`,
		`ALTER EXTENSION plpgsql ADD TEXT SEARCH TEMPLATE pg_catalog.snowball`,
		`ALTER EXTENSION plpgsql ADD TEXT SEARCH CONFIGURATION pg_catalog.english`,
	} {
		enginetest.RoleExecute(t, e, op, sql)
	}
	view := e.CatalogRead()
	second := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "plpgsql"}))
	for _, a := range addresses {
		old, current := before.Get(a), view.Get(a)
		if stored.DependPinned(view, nil, a) || catalog.DescriptorOrigin(current) != nil {
			t.Fatal("membership replaced intrinsic seed authority", a)
		}
		if err := catalog.Validate(view.Owner().Provider(a.ClassID), view, current); err != nil {
			t.Fatal("lawful distinct receiver/detached seed rejected", a, err)
		}
		if a != template.Lexize && (current == old || current.Revision() == old.Revision()) {
			t.Fatal("membership did not publish a new immutable generation", a)
		}
		if catalog.ExtensionMembership(old) != (ids.ObjectAddress{}) {
			t.Fatal("membership mutated captured seed", a)
		}
	}
	for _, sql := range []string{
		`DROP FUNCTION pg_catalog.dsnowball_init(internal) CASCADE`,
		`DROP FUNCTION pg_catalog.dsnowball_lexize(internal,internal,internal,internal) RESTRICT`,
		`DROP TEXT SEARCH TEMPLATE pg_catalog.snowball CASCADE`,
		`DROP TEXT SEARCH DICTIONARY pg_catalog.english_stem CASCADE`,
		`DROP TEXT SEARCH CONFIGURATION pg_catalog.english CASCADE`,
	} {
		err := extensionUnitExecute(t, e, op, enginetest.Statement(t, sql))
		enginetest.State(t, err, "2BP01")
		if strings.Contains(err.Error(), "required by the database system") {
			t.Fatal("allocated Snowball seed incorrectly pinned", err)
		}
		for a, old := range view.Descriptors() {
			if e.CatalogRead().Get(a) != old {
				t.Fatal("restricted drop changed immutable source", a)
			}
		}
	}
	if view.MustTSDictionary(dictionary.Address()).Runtime != dictionary.Runtime || &view.MustTSDictionary(dictionary.Address()).Options[0] != &dictionary.Options[0] || &view.MustTSConfiguration(configuration.Address()).Mappings[0] != &configuration.Mappings[0] {
		t.Fatal("membership replaced immutable runtime/options/mapping backing")
	}
	// Intrinsic execution authority ignores mutable e; class admission still
	// rejects dangling parents and missing reverse membership.
	for _, a := range []ids.ObjectAddress{template.Init, template.Address(), dictionary.Address(), configuration.Address()} {
		state := catalog.CloneState(view.State())
		parent := *view.MustExtension(catalog.ExtensionMembership(view.Get(a)))
		parent.Members = slices.DeleteFunc(slices.Clone(parent.Members), func(member ids.ObjectAddress) bool { return member == a })
		state.SetDescriptor(parent.Address(), &parent)
		if err := catalog.Validate(view.Owner().Provider(a.ClassID), view.WithState(state), state.Get(a)); err == nil {
			t.Fatal("missing reverse current parent admitted", a)
		}
		state.RemoveDescriptor(parent.Address())
		if err := catalog.Validate(view.Owner().Provider(a.ClassID), view.WithState(state), state.Get(a)); err == nil {
			t.Fatal("dangling current parent admitted", a)
		}
	}
	// One memo remaps both receivers and every intrinsic link; runtime and
	// immutable option backing stay catalog-free rather than being serialized.
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{}
	for i, a := range append(slices.Clone(addresses), first.Address(), second.Address()) {
		mapping[a] = ids.ObjectAddress{ClassID: a.ClassID, ObjectID: ids.OID(970000 + i)}
	}
	remap := extension.DescriptorRemapper(mapping)
	state := catalog.CloneState(view.State())
	for a, d := range view.Descriptors() {
		next := remap(d)
		if next != d {
			state.UnbindName(catalog.DescriptorNameKey(d))
			state.RemoveDescriptor(a)
			state.SetDescriptor(next.Address(), next)
			state.BindName(catalog.DescriptorNameKey(next), next.Address())
		}
	}
	mapped := view.WithState(state)
	for _, a := range addresses {
		d := mapped.Get(mapping[a])
		if err := catalog.Validate(mapped.Owner().Provider(a.ClassID), mapped, d); err != nil {
			t.Fatal("remapped intrinsic graph rejected", a, err)
		}
		if stored.DependPinned(mapped, nil, d.Address()) {
			t.Fatal("allocated remapped seed acquired pin", a)
		}
	}
	md := mapped.MustTSDictionary(mapping[dictionary.Address()])
	mt, ok := mapped.TSTemplate(mapping[template.Address()])
	if !ok {
		t.Fatal("missing remapped Snowball template")
	}
	mc := mapped.MustTSConfiguration(mapping[configuration.Address()])
	if md.Template != mt.Address() || mt.Init != mapping[template.Init] || mt.Lexize != mapping[template.Lexize] || md.Runtime != dictionary.Runtime || mc.Mapping(1)[0] != md.Address() {
		t.Fatal("remap lost helper/template/dictionary/configuration links")
	}
	for _, parent := range []*catalog.Extension{view.MustExtension(first.Address()), second} {
		text, err := extension.OwnerMetadata(view, e.Roles(), parent)
		if err != nil {
			t.Fatal(err)
		}
		record, found, err := extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(text, "-- ")))
		if err != nil || !found {
			t.Fatal(found, err)
		}
		candidate := catalog.CloneState(view.State())
		if changed, err := extension.ImportOwners(context.Background(), view.WithState(candidate), e.Roles(), []extension.OwnerRecord{record}); changed || err != nil {
			t.Fatal("ordinary intrinsic membership import", changed, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		canceled := catalog.CloneState(view.State())
		if changed, err := extension.ImportOwners(ctx, view.WithState(canceled), e.Roles(), []extension.OwnerRecord{record}); changed || err != context.Canceled {
			t.Fatal("canceled intrinsic membership import", changed, err)
		}
		for a, old := range view.Descriptors() {
			if canceled.Get(a) != old {
				t.Fatal("canceled import changed immutable source", a)
			}
		}
		for i, member := range record.Members {
			if member.Declaration != nil {
				continue
			}
			if member.Version != "" || member.Owner != "" || member.ACL != nil {
				t.Fatal("intrinsic seed manufactured installed origin", member)
			}
			for _, field := range []string{"version", "owner", "acl", "declaration"} {
				bad := record
				bad.Members = slices.Clone(record.Members)
				switch field {
				case "version":
					bad.Members[i].Version = parent.Version
				case "owner":
					bad.Members[i].Owner = "postgres"
				case "acl":
					bad.Members[i].ACL = &extension.MemberACL{}
				case "declaration":
					n := 999
					bad.Members[i].Declaration, bad.Members[i].Version = &n, parent.Version
				}
				data, err := json.Marshal(bad)
				if err != nil {
					t.Fatal(err)
				}
				_, _, decodeErr := extension.DecodeOwners(extension.OwnersMarker + " " + base64.StdEncoding.EncodeToString(data))
				if field != "declaration" && decodeErr == nil {
					t.Fatal("ordinary-v3 forged authority decoded", field)
				}
				candidate := catalog.CloneState(view.State())
				if changed, err := extension.ImportOwners(context.Background(), view.WithState(candidate), e.Roles(), []extension.OwnerRecord{bad}); changed || err == nil {
					t.Fatal("ordinary-v3 forged authority imported", field, changed, err)
				}
				for a, old := range view.Descriptors() {
					if candidate.Get(a) != old {
						t.Fatal("rejected import changed backing", a)
					}
				}
			}
		}
	}
	enginetest.RoleExecute(t, e, op, `ALTER EXTENSION unaccent DROP FUNCTION pg_catalog.dsnowball_init(internal)`)
	if current := e.CatalogRead().MustProcedure(template.Init); current.Extension != (ids.ObjectAddress{}) || catalog.ValidateSnowballHelper(e.CatalogRead(), current) != nil || current.SnowballHelper != catalog.SnowballInit {
		t.Fatal("lawful local clearing lost helper authority")
	}
}

func ownershipTablefuncIdentity(t *testing.T) {
	e, ext := ownershipUnitInstall(t, "tablefunc")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	before := e.CatalogRead()
	original := before.MustProcedure(ext.Installation.Members[0])
	recipe := original.Origin.Installation
	for _, q := range []string{`ALTER EXTENSION tablefunc DROP FUNCTION public.normal_rand(integer,double precision,double precision)`, `ALTER FUNCTION public.normal_rand(integer,double precision,double precision) RENAME TO eo_normal`, `CREATE EXTENSION "uuid-ossp"`, `ALTER EXTENSION "uuid-ossp" ADD FUNCTION public.eo_normal(integer,double precision,double precision)`, `DROP EXTENSION tablefunc`, `ALTER EXTENSION "uuid-ossp" DROP FUNCTION public.eo_normal(integer,double precision,double precision)`} {
		enginetest.RoleExecute(t, e, op, q)
		view := e.CatalogRead()
		p := view.MustProcedure(original.Address())
		if p.Origin.Installation != recipe || p.Implementation != original.Implementation || p.Installed != original.Installed || p.Owner != original.Owner {
			t.Fatal("SQL membership/identity changes rewrote normal provider declaration", q)
		}
		if symbol, err := extension.ProcedureSymbol(view, p); err != nil || symbol != "normal_rand" {
			t.Fatal("normal provider lost immutable symbol", q, symbol, err)
		}
		if err := extension.StockTablefunc.ValidateProcedure(view, p); err != nil {
			t.Fatal("genuine normal procedure rejected", q, err)
		}
	}
	view := e.CatalogRead()
	p := view.MustProcedure(original.Address())
	if view.Get(ext.Address()) != nil || catalog.ExtensionMembership(p) != (ids.ObjectAddress{}) || before.MustProcedure(original.Address()) != original || original.Extension != ext.Address() {
		t.Fatal("absent/detached membership mutated immutable predecessor")
	}
	candidate := catalog.CloneState(view.State())
	forged := *p
	recipeCopy := *recipe
	origin := *p.Origin
	origin.Installation = &recipeCopy
	forged.Origin = &origin
	candidate.SetDescriptor(p.Address(), &forged)
	if err := extension.StockTablefunc.ValidateProcedure(view.WithState(candidate), &forged); err == nil {
		t.Fatal("normal provider accepted unsealed copied recipe")
	}
}

func ownershipPageinspectIdentity(t *testing.T) {
	e, ext := ownershipUnitInstall(t, "pageinspect")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	before := e.CatalogRead()
	original := before.MustProcedure(ext.Installation.Members[0])
	recipe := original.Origin.Installation
	originals := make([]*catalog.Procedure, len(recipe.Members))
	for i, address := range recipe.Members {
		originals[i] = before.MustProcedure(address)
	}
	symbols := [...]string{"brin_page_type", "brin_metapage_info", "brin_revmap_data", "brin_page_items", "bt_page_items", "fsm_page_contents", "gin_metapage_info", "gin_page_opaque_info", "gin_leafpage_items", "page_header", "page_checksum", "hash_page_type", "hash_page_stats", "hash_page_items", "hash_metapage_info", "heap_page_items", "heap_tuple_infomask_flags", "tuple_data_split", "tuple_data_split", "heap_page_item_attrs", "heap_page_item_attrs"}
	if len(originals) != len(symbols) {
		t.Fatal("incomplete released pageinspect inventory")
	}
	for _, q := range []string{
		`CREATE FUNCTION public.eo_extra(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'`,
		`ALTER EXTENSION pageinspect ADD FUNCTION public.eo_extra(integer)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.brin_page_type(bytea)`,
		`ALTER FUNCTION public.brin_page_type(bytea) RENAME TO eo_page_type`,
		`CREATE EXTENSION "uuid-ossp"`,
		`ALTER EXTENSION "uuid-ossp" ADD FUNCTION public.eo_page_type(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.eo_extra(integer)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.brin_metapage_info(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.brin_revmap_data(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.brin_page_items(bytea,regclass)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.bt_page_items(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.fsm_page_contents(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.gin_metapage_info(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.gin_page_opaque_info(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.gin_leafpage_items(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.page_header(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.page_checksum(bytea,bigint)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.hash_page_type(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.hash_page_stats(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.hash_page_items(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.hash_metapage_info(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.heap_page_items(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.heap_tuple_infomask_flags(integer,integer)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.tuple_data_split(oid,bytea,integer,integer,text)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.tuple_data_split(oid,bytea,integer,integer,text,boolean)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.heap_page_item_attrs(bytea,regclass,boolean)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.heap_page_item_attrs(bytea,regclass)`,
		`DROP EXTENSION pageinspect`,
		`ALTER EXTENSION "uuid-ossp" DROP FUNCTION public.eo_page_type(bytea)`,
	} {
		enginetest.RoleExecute(t, e, op, q)
		view := e.CatalogRead()
		p := view.MustProcedure(original.Address())
		if p.Origin.Installation != recipe || p.Implementation != original.Implementation || p.Installed != original.Installed || p.Display != original.Display || p.Owner != original.Owner {
			t.Fatal("membership/identity changes rewrote pageinspect release declaration", q)
		}
		if symbol, err := extension.ProcedureSymbol(view, p); err != nil || symbol != "brin_page_type" {
			t.Fatal("pageinspect lost immutable symbol", q, symbol, err)
		}
		if err := extension.StockPageinspect.ValidateProcedure(view, p); err != nil {
			t.Fatal("genuine pageinspect procedure rejected", q, err)
		}
		if current, err := exec.ValidateStoredRoutineDescriptor(view, original); err != nil || current != p {
			t.Fatal("membership invalidated retained pageinspect carrier", q, err)
		}
		for index, captured := range originals {
			current := view.MustProcedure(captured.Address())
			if current.Origin.Installation != recipe || current.Display != captured.Display || current.Installed != captured.Installed {
				t.Fatal("pageinspect mutation changed a released declaration", q)
			}
			if symbol, err := extension.ProcedureSymbol(view, current); err != nil || symbol != symbols[index] {
				t.Fatal("pageinspect release symbol changed", q, symbol, err)
			}
			if _, err := exec.ValidateStoredRoutineDescriptor(view, captured); err != nil {
				t.Fatal("pageinspect retained capture rejected", q, err)
			}
		}
		if current, ok := view.Extension(ext.Address()); ok {
			if err := extension.StockPageinspect.ValidateExtension(view, current); err != nil {
				t.Fatal("mutable pageinspect member count rejected", q, err)
			}
		}
	}
	view := e.CatalogRead()
	p := view.MustProcedure(original.Address())
	if view.Get(ext.Address()) != nil || catalog.ExtensionMembership(p) != (ids.ObjectAddress{}) || before.MustProcedure(original.Address()) != original || original.Extension != ext.Address() {
		t.Fatal("absent/detached membership mutated immutable predecessor")
	}
	for _, corrupt := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Origin = nil },
		func(p *catalog.Procedure) {
			origin := *p.Origin
			copied := *recipe
			origin.Installation = &copied
			p.Origin = &origin
		},
		func(p *catalog.Procedure) {
			origin := *p.Origin
			origin.Declaration = 1
			p.Origin = &origin
		},
		func(p *catalog.Procedure) { p.Signature = "23" },
		func(p *catalog.Procedure) { p.Installed.Parallel = 'u' },
		func(p *catalog.Procedure) { p.Display.Source = "brin_metapage_info" },
	} {
		candidate := catalog.CloneState(view.State())
		forged := *p
		corrupt(&forged)
		candidate.SetDescriptor(p.Address(), &forged)
		if err := extension.StockPageinspect.ValidateProcedure(view.WithState(candidate), &forged); err == nil {
			t.Fatal("pageinspect accepted forged release declaration")
		}
	}
}

func ownershipPageinspectCanonicalRemap(t *testing.T) {
	e, ext := ownershipUnitInstall(t, "pageinspect")
	view := e.CatalogRead()
	recipe := ext.Installation
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{
		ext.Address():       {ClassID: ids.ExtensionClassID, ObjectID: 940000},
		ext.TargetNamespace: {ClassID: ids.NamespaceClassID, ObjectID: 940001},
	}
	for i, a := range recipe.Members {
		mapping[a] = ids.ObjectAddress{ClassID: a.ClassID, ObjectID: ids.OID(940100 + i)}
	}
	remap := extension.DescriptorRemapper(mapping)
	mapped := remap(ext).(*catalog.Extension)
	next := mapped.Installation
	if next == recipe || !next.Valid() || next.Cohort != mapping[ext.Address()] || next.Implementation != recipe.Implementation || next.Version != "1.13" || len(next.Declarations) != 21 {
		t.Fatal("pageinspect remap lost sealed release")
	}
	for i, a := range recipe.Members {
		original := view.MustProcedure(a)
		p := remap(original).(*catalog.Procedure)
		canonical := next.Declarations[i].(*catalog.Procedure)
		if p.Origin.Installation != next || p.Origin.Declaration != i || !p.Origin.Valid(mapping[a]) || next.Members[i] != mapping[a] || canonical.Address() != mapping[a] || canonical.Namespace() != mapping[ext.TargetNamespace] || canonical.Origin != nil {
			t.Fatal("pageinspect remap split canonical release authority", i)
		}
		if p.Installed != original.Installed || canonical.Installed != original.Installed || canonical.Signature != original.Signature || canonical.Result != original.Result || canonical.Display != original.Display {
			t.Fatal("pageinspect remap changed released formals or immutable OUT payload", i)
		}
		if recipe.Declarations[i].Address() != a || original.Origin.Installation != recipe {
			t.Fatal("pageinspect remap mutated source recipe", i)
		}
	}
	forged := *recipe
	if forged.Valid() {
		t.Fatal("copied pageinspect recipe forged a seal")
	}
}

func ownershipConnectbyIdentity(t *testing.T) {
	for _, variant := range []struct {
		name, args, signature string
		declaration           int
	}{
		{"branch", "'missing','k','p','root',0,'~'", "text,text,text,text,integer,text", 1},
		{"plain", "'missing','k','p','root',0", "text,text,text,text,integer", 2},
		{"ordered_branch", "'missing','k','p','ord','root',0,'~'", "text,text,text,text,text,integer,text", 3},
		{"ordered", "'missing','k','p','ord','root',0", "text,text,text,text,text,integer", 4},
	} {
		t.Run(variant.name, func(t *testing.T) {
			e, ext := ownershipUnitInstall(t, "tablefunc")
			before := e.CatalogRead()
			recipe := ext.Installation
			original := before.MustProcedure(recipe.Members[variant.declaration])
			b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			b.Clause.AggregateClause = "SELECT"
			raw := enginetest.Statement(t, "SELECT connectby("+variant.args+")").(*parser.Select).Targets[0].(parser.FunctionExpr)
			bound, err := b.BindFunctionProvider(raw)
			if err != nil {
				t.Fatal(err)
			}
			n := bound.(plan.InstalledConnectbyExpr)
			if n.Procedure != original || n.RoutineReference != original {
				t.Fatal("wrong released overload carrier")
			}
			defs := []parser.FunctionColumnDefinition{
				{Name: parser.Identifier{Text: "node"}, Type: parser.TypeName{Name: "text"}},
				{Name: parser.Identifier{Text: "parent"}, Type: parser.TypeName{Name: "text"}},
				{Name: parser.Identifier{Text: "depth"}, Type: parser.TypeName{Name: "integer"}},
			}
			if n.Branch {
				defs = append(defs, parser.FunctionColumnDefinition{Name: parser.Identifier{Text: "branch"}, Type: parser.TypeName{Name: "text"}})
			}
			if n.Ordered {
				defs = append(defs, parser.FunctionColumnDefinition{Name: parser.Identifier{Text: "serial"}, Type: parser.TypeName{Name: "integer"}})
			}
			completed, err := b.CompleteFunctionDefinitions(raw, n, defs)
			if err != nil {
				t.Fatal(err)
			}
			n = completed.(plan.InstalledConnectbyExpr)
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			enginetest.RoleExecute(t, e, op, `CREATE ROLE eo_connectby_owner`)
			enginetest.RoleExecute(t, e, op, `CREATE EXTENSION "uuid-ossp"`)
			enginetest.RoleExecute(t, e, op, `CREATE FUNCTION eo_extra(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'`)
			enginetest.RoleExecute(t, e, op, `ALTER EXTENSION tablefunc ADD FUNCTION eo_extra(integer)`)
			populated := e.CatalogRead()
			currentExtension := populated.MustExtension(ext.Address())
			if len(currentExtension.Members) != 18 || len(recipe.Declarations) != 17 {
				t.Fatal("ordinary membership rewrote release recipe")
			}
			if err := extension.StockTablefunc.ValidateExtension(populated, currentExtension); err != nil {
				t.Fatal("ordinary member extended current inventory without changing the sixteen-declaration recipe", err)
			}
			emptyState := catalog.CloneState(populated.State())
			emptyExtension := *currentExtension
			emptyExtension.Members = nil
			emptyState.SetDescriptor(ext.Address(), &emptyExtension)
			for _, address := range currentExtension.Members {
				d := populated.Get(address)
				detached, err := catalog.WithExtensionMembership(d, ids.ObjectAddress{}, d.Revision())
				if err != nil {
					t.Fatal(err)
				}
				emptyState.SetDescriptor(address, detached)
			}
			empty := populated.WithState(emptyState)
			if err := extension.StockTablefunc.ValidateExtension(empty, &emptyExtension); err != nil {
				t.Fatal("empty reciprocal inventory rejected", err)
			}
			if err := stored.ValidateInstalledConnectby(n, empty); err != nil {
				t.Fatal("empty current inventory erased genuine capability", err)
			}
			sig := "(" + variant.signature + ")"
			for _, q := range []string{
				`ALTER EXTENSION tablefunc DROP FUNCTION public.connectby` + sig,
				`ALTER FUNCTION public.connectby` + sig + ` RENAME TO eo_walk`,
				`ALTER FUNCTION public.eo_walk` + sig + ` OWNER TO eo_connectby_owner`,
				`REVOKE EXECUTE ON FUNCTION public.eo_walk` + sig + ` FROM PUBLIC`,
				`ALTER EXTENSION "uuid-ossp" ADD FUNCTION public.eo_walk` + sig,
				`DROP EXTENSION tablefunc`,
				`ALTER EXTENSION "uuid-ossp" DROP FUNCTION public.eo_walk` + sig,
			} {
				enginetest.RoleExecute(t, e, op, q)
				view := e.CatalogRead()
				p := view.MustProcedure(original.Address())
				if p.Origin.Installation != recipe || p.Origin.Declaration != variant.declaration || !p.Origin.Valid(p.Address()) || p.Installed != original.Installed || p.Display != original.Display || p.Implementation != original.Implementation {
					t.Fatal("membership rewrote released declaration", q)
				}
				if symbol, err := extension.ProcedureSymbol(view, p); err != nil || symbol != "connectby" {
					t.Fatal("lost immutable connectby symbol", q, symbol, err)
				}
				if current, err := exec.ValidateStoredRoutineDescriptor(view, original); err != nil || current != p {
					t.Fatal("retained carrier lost current authority", q, err)
				}
				if err := stored.ValidateInstalledConnectby(n, view); err != nil {
					t.Fatal("typed carrier rejected", q, err)
				}
				if current, ok := view.Extension(ext.Address()); ok {
					if err := extension.StockTablefunc.ValidateExtension(view, current); err != nil {
						t.Fatal("mutable member bag rejected", q, err)
					}
				}
			}
			view := e.CatalogRead()
			p := view.MustProcedure(original.Address())
			recipient, ok := e.Roles().Lookup("eo_connectby_owner")
			if !ok || p.Owner != recipient.Ref || !p.ACL.Explicit || reflect.DeepEqual(p.ACL, original.ACL) || view.Get(ext.Address()) != nil || p.Extension != (ids.ObjectAddress{}) || before.MustProcedure(original.Address()) != original || original.Extension != ext.Address() {
				t.Fatal("current owner/ACL, absence, or immutable predecessor lost")
			}
			for _, corrupt := range []func(*catalog.Procedure){
				func(p *catalog.Procedure) { p.Origin = nil },
				func(p *catalog.Procedure) {
					origin := *p.Origin
					copied := *recipe
					origin.Installation = &copied
					p.Origin = &origin
				},
				func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration = 0; p.Origin = &origin },
				func(p *catalog.Procedure) { p.Signature = "23" },
				func(p *catalog.Procedure) { p.Installed.ReturnsSet = false },
				func(p *catalog.Procedure) { p.Display.Source = "normal_rand" },
			} {
				forged := *p
				corrupt(&forged)
				candidate := catalog.CloneState(view.State())
				candidate.SetDescriptor(p.Address(), &forged)
				invalid := view.WithState(candidate)
				if err := extension.StockTablefunc.ValidateProcedure(invalid, &forged); err == nil {
					t.Fatal("forged current declaration accepted")
				}
				if err := stored.ValidateInstalledConnectby(n, invalid); err == nil {
					t.Fatal("typed carrier accepted forged current authority")
				}
				bad := n
				bad.Procedure, bad.RoutineReference = &forged, &forged
				if err := stored.ValidateInstalledConnectby(bad, view); err == nil {
					t.Fatal("forged retained carrier accepted")
				}
			}
			stale := n
			stale.Ref.Epoch++
			if err := stored.ValidateInstalledConnectby(stale, view); err == nil {
				t.Fatal("stale epoch carrier accepted")
			}
			foreign := n
			foreign.Ref = (&ids.Domain{}).Ref(view.Epoch(), original.Address())
			if err := stored.ValidateInstalledConnectby(foreign, view); err == nil {
				t.Fatal("foreign domain carrier accepted")
			}
			mapping := map[ids.ObjectAddress]ids.ObjectAddress{
				ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 960000},
				p.Address():   {ClassID: ids.ProcedureClassID, ObjectID: 960001},
			}
			candidate := catalog.CloneState(view.State())
			candidate.UnbindName(catalog.DescriptorNameKey(p))
			candidate.RemoveDescriptor(p.Address())
			remapDescriptor := extension.DescriptorRemapper(mapping)
			mapped := remapDescriptor(p).(*catalog.Procedure)
			candidate.SetDescriptor(mapped.Address(), mapped)
			candidate.BindName(catalog.DescriptorNameKey(mapped), mapped.Address())
			candidate.SetEpoch(view.Epoch() + 1)
			destination := view.WithState(candidate)
			ref := func(r ids.ObjectRef) ids.ObjectRef {
				r.Epoch = destination.Epoch()
				if a, ok := mapping[r.Address]; ok {
					r.Address = a
				}
				return r
			}
			mapper := stored.ViewQueryRemapper{Catalog: destination, Ref: ref, Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: extension.ProcedureRemapper(remapDescriptor, nil, nil)}
			query := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "walk", Usage: n.ResultUsage()}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{n}}
			remapped, err := mapper.Query(query)
			if err != nil {
				t.Fatal("absent-origin retained carrier remap", err)
			}
			m := remapped.(*plan.ProjectNode).Expressions[0].(plan.InstalledConnectbyExpr)
			if m.Ref != ref(n.Ref) || m.RoutineReference != m.Procedure || m.Procedure == mapped || !exec.SameRoutineDeclaration(m.Procedure, mapped) || !sqltype.SameRecordUsage(m.Usage, n.Usage) || m.Ordered != n.Ordered || m.Branch != n.Branch || !m.Defined || destination.Get(mapping[ext.Address()]) != nil {
				t.Fatal("remap lost retained origin, current authority, record shape or flags")
			}
		})
	}
}

func ownershipConnectbyCanonicalRemap(t *testing.T) {
	e, ext := ownershipUnitInstall(t, "tablefunc")
	view := e.CatalogRead()
	recipe := ext.Installation
	if len(recipe.Members) != 17 || len(recipe.Declarations) != 17 {
		t.Fatal("complete tablefunc recipe lost normal_rand, connectby or crosstab declarations")
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 950000}, ext.TargetNamespace: {ClassID: ids.NamespaceClassID, ObjectID: 950001}}
	for i, a := range recipe.Members {
		mapping[a] = ids.ObjectAddress{ClassID: a.ClassID, ObjectID: ids.OID(950100 + i)}
	}
	remap := extension.DescriptorRemapper(mapping)
	next := remap(ext).(*catalog.Extension).Installation
	if !next.Valid() || next == recipe || next.Cohort != mapping[ext.Address()] || next.Version != "1.0" {
		t.Fatal("remapped sealed release lost")
	}
	for i, a := range recipe.Members {
		original := view.Get(a)
		canonical := next.Declarations[i]
		if next.Members[i] != mapping[a] || canonical.Address() != mapping[a] || canonical.Namespace() != mapping[ext.TargetNamespace] || catalog.DescriptorOrigin(canonical) != nil {
			t.Fatal("remap split the complete canonical release", i)
		}
		if procedure, ok := original.(*catalog.Procedure); ok {
			p := remap(procedure).(*catalog.Procedure)
			declaration := canonical.(*catalog.Procedure)
			result := procedure.Result
			if mapped, ok := mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: result}]; ok {
				result = mapped.ObjectID
			}
			if p.Origin.Installation != next || p.Origin.Declaration != i || !p.Origin.Valid(mapping[a]) || declaration.Signature != procedure.Signature || declaration.Installed != procedure.Installed || declaration.Result != result || declaration.Display != procedure.Display || p.Result != result {
				t.Fatal("remap split or changed released procedure declaration", i)
			}
		}
		if recipe.Declarations[i].Address() != a || catalog.DescriptorOrigin(original).Installation != recipe {
			t.Fatal("remap mutated source release", i)
		}
	}
}

func ownershipParserFixture(t *testing.T) (*core.Engine, *catalog.Extension, *catalog.TSParser, *catalog.TSConfiguration) {
	t.Helper()
	e, ext := ownershipUnitInstall(t, "unaccent")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	stage, err := parserStage(t, e, op, parserCreateNode("eo_parser"))
	if err != nil {
		t.Fatal(err)
	}
	enginetest.PublishOperationStage(t, e, op, stage)
	enginetest.RoleExecute(t, e, op, `CREATE TEXT SEARCH CONFIGURATION public.eo_parser_config (PARSER=public.eo_parser)`)
	enginetest.RoleExecute(t, e, op, `ALTER TEXT SEARCH CONFIGURATION public.eo_parser_config ADD MAPPING FOR asciiword WITH pg_catalog.simple`)
	view := e.CatalogRead()
	d := view.MustTSParser(view.NameAddress(ids.NameKey{ClassID: ids.TSParserClassID, Namespace: ext.TargetNamespace, Name: "eo_parser"}))
	configuration := view.MustTSConfiguration(view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ext.TargetNamespace, Name: "eo_parser_config"}))
	return e, ext, d, configuration
}

func ownershipParserOwnerCodec(t *testing.T, e *core.Engine, d *catalog.TSParser) {
	t.Helper()
	view := e.CatalogRead()
	ext := view.MustExtension(d.Extension)
	text, err := extension.OwnerMetadata(view, e.Roles(), ext)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(text, "-- ")))
	if err != nil || !found {
		t.Fatal(found, err)
	}
	index := -1
	for i, member := range record.Members {
		if member.Kind == "parser" {
			index = i
			if member.Schema != "public" || member.Name != d.Name() || member.Signature != "" || member.Declaration != nil || member.Version != "" || member.Owner != "" || member.ACL != nil {
				t.Fatal("ordinary parser acquired installed authority", member)
			}
		}
	}
	if index < 0 {
		t.Fatal("ordinary parser omitted")
	}
	if sql, err := extension.MemberSQL(view, d); err != nil || sql != `TEXT SEARCH PARSER "public"."eo_parser"` {
		t.Fatal(sql, err)
	}
	if changed, err := extension.ImportOwners(context.Background(), view.WithState(catalog.CloneState(view.State())), e.Roles(), []extension.OwnerRecord{record}); changed || err != nil {
		t.Fatal("genuine ordinary import", changed, err)
	}
	for _, field := range []string{"declaration", "version", "owner", "acl", "signature", "identity", "missing"} {
		bad := record
		bad.Members = slices.Clone(record.Members)
		m := &bad.Members[index]
		switch field {
		case "declaration":
			n := 999
			m.Declaration, m.Version = &n, ext.Version
		case "version":
			m.Version = ext.Version
		case "owner":
			m.Owner = "postgres"
		case "acl":
			m.ACL = &extension.MemberACL{}
		case "signature":
			m.Signature = "internal"
		case "identity":
			m.Name = "missing_parser"
		case "missing":
			bad.Members = slices.Delete(bad.Members, index, index+1)
		}
		data, err := json.Marshal(bad)
		if err != nil {
			t.Fatal(err)
		}
		_, _, decodeErr := extension.DecodeOwners(extension.OwnersMarker + " " + base64.StdEncoding.EncodeToString(data))
		if field == "version" || field == "owner" || field == "acl" || field == "signature" {
			if decodeErr == nil {
				t.Fatal("forged ordinary metadata decoded", field)
			}
		}
		candidate := catalog.CloneState(view.State())
		if changed, err := extension.ImportOwners(context.Background(), view.WithState(candidate), e.Roles(), []extension.OwnerRecord{bad}); changed || err == nil {
			t.Fatal("forged ordinary parser authority imported", field, changed, err)
		}
		for a, old := range view.Descriptors() {
			if candidate.Get(a) != old {
				t.Fatal("failed import mutated candidate", field, a)
			}
		}
	}
	candidate := catalog.CloneState(view.State())
	parent := *ext
	parent.Members = slices.DeleteFunc(slices.Clone(parent.Members), func(a ids.ObjectAddress) bool { return a == d.Address() })
	candidate.SetDescriptor(parent.Address(), &parent)
	if changed, err := extension.ImportOwners(context.Background(), view.WithState(candidate), e.Roles(), []extension.OwnerRecord{record}); changed || err == nil {
		t.Fatal("import admitted nonreciprocal parent", changed, err)
	}
	parent.Members = append(slices.Clone(ext.Members), d.Address())
	if changed, err := extension.ImportOwners(context.Background(), view.WithState(candidate), e.Roles(), []extension.OwnerRecord{record}); changed || err == nil {
		t.Fatal("import admitted duplicate parent member", changed, err)
	}
}

func extensionUnitExecute(t *testing.T, e *core.Engine, op *session.Operation, stmt parser.Statement) error {
	t.Helper()
	scope, err := e.StartStatement(op)
	if err != nil {
		return err
	}
	finished := false
	defer func() {
		// A malformed unit AST or an unexpected engine panic must not leave
		// statement admission held while roleOperation closes the session.
		// Preserve the panic; this defer only performs the normal scope unwind.
		if !finished {
			_ = scope.Finish(errors.New("stuntgres: extension unit statement unwound"))
		}
	}()
	_, err = scope.Execute(stmt, 0, nil)
	finished = true
	return scope.Finish(err)
}

func roleAttributeCreate(t *testing.T, e *core.Engine, op *session.Operation, name string, login bool, o parser.RoleOptions) authorization.RoleDescriptor {
	t.Helper()
	roleAttributeApply(t, e, op, &parser.CreateRole{Name: parser.Identifier{Text: name}, DefaultLogin: login, Options: o})
	d, ok := e.Roles().Lookup(name)
	if !ok {
		t.Fatal("missing role", name)
	}
	return d
}

func extensionUnitGrant(name string, role string) *parser.ObjectGrant {
	return &parser.ObjectGrant{Kind: "database", Privileges: []parser.GrantPrivilege{{Name: "create"}}, Relations: []parser.ObjectName{{Parts: []parser.Identifier{{Text: name}}}}, Grantees: []parser.GrantGrantee{{Role: parser.RoleSpec{Name: role}}}}
}

var parserSeedAddress = ids.ObjectAddress{ClassID: ids.TSParserClassID, ObjectID: 3722}

func parserStage(t *testing.T, e *core.Engine, op *session.Operation, statement parser.Statement) (*core.CatalogStage, error) {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return nil, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	host := schemacmds.SchemaHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}
	switch s := statement.(type) {
	case *parser.CreateParser:
		err = ddl.StageCreateParser(host, auth, s)
	case *parser.AlterParser:
		err = ddl.StageAlterParser(host, auth, s)
	case *parser.DropParser:
		err = ddl.StageDropParser(host, auth, s)
	}
	return stage, err
}

func snowballUnitDictionary(t *testing.T, e interface{ CatalogRead() catalog.Read }) *catalog.TSDictionary {
	t.Helper()
	v := e.CatalogRead()
	a := v.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Name: "english_stem"})
	d, ok := v.TSDictionary(a)
	if !ok {
		t.Fatal("missing genuine English seed")
	}
	return d
}

func parserCreateNode(name string) *parser.CreateParser {
	s := &parser.CreateParser{Name: configName(name)}
	for _, method := range []struct{ name, routine string }{{"start", "prsd_start"}, {"gettoken", "prsd_nexttoken"}, {"end", "prsd_end"}, {"lextypes", "prsd_lextype"}} {
		s.Options = append(s.Options, parser.ConfigurationOption{Name: parser.Identifier{Text: method.name}, Value: configName(method.routine), Supplied: true})
	}
	return s
}

func roleAttributeApply(t *testing.T, e *core.Engine, op *session.Operation, s parser.Statement) {
	t.Helper()
	r, err := e.Execute(op, s, 0, nil)
	if err != nil || r.Count != 0 || r.Rows != nil {
		t.Fatalf("attribute utility: result=%#v error=%v", r, err)
	}
}

func configName(name string) parser.ObjectName {
	return parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}
}
