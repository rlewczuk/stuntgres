package exec

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
)

// Established only by BeginNativeBody/BeginExpandedNativeDeclaration after the
// original source admission. This is invocation-local lifetime derivation, not a
// catalog dependency, permission grant, or authority for a descendant body.
type nativeBodyLineage struct {
	parent RoutineInitializationCall
	owner  RoutineInitialization
	phase  RoutineInitializationPhase
	// Aggregate support borrows the original owner rather than an ordinary
	// outer-call receipt. Only its immediate body keeps this alternative.
	aggregate  *catalog.Procedure
	read       catalog.Read
	checks     []catalog.ExpressionDependency
	references []ids.ObjectRef
	recipe     bool
}

// A real unrelated recipe/default/domain/index/query phase cannot borrow the
// immediate native body's source. Owners call this on their local value copy.
func (c *Context) ClearNativeBodySource() {
	c.nativeBody = nativeBodyLineage{}
	c.nativeAggregate = nativeAggregateSource{}
	c.nativeAggregateEntered = false
	c.declarationDemand = retainedNativeDeclarationCalls
	c.RoutineBodyContext = RoutineBodyContext{}
	c.routineSource = routineSourceBinding{}
}

// Only this immutable projection can enter a consumed command program. In
// particular it excludes owner/nonces, receipts, accepted-default activations,
// and every live attachment carried by the runtime lineage.
type nativeRoutineSource struct {
	Reference       ids.ObjectRef
	Routine         *catalog.Procedure
	Recipe          expr.RoutineRecipeReference
	Consumer        *catalog.Relation
	Body            RoutineBodyContext
	Phase           RoutineInitializationPhase
	Occurrence      uint32
	Source          expr.FoldSourceOccurrence
	ManifestOrdinal uint32
	FollowIdentity  bool
}

func nativeSourceOf(site RoutineInitializationSite) nativeRoutineSource {
	return nativeRoutineSource{Reference: site.Reference, Routine: site.Routine, Recipe: site.Recipe, Consumer: site.Consumer, Body: site.Body, Phase: site.Phase, Occurrence: site.Occurrence, Source: site.Source, ManifestOrdinal: site.ManifestOrdinal, FollowIdentity: site.FollowIdentity}
}

func nativeSourceRoutineLifetime(auth RoutineAccess, source nativeRoutineSource, captured *catalog.Procedure) (ids.ObjectRef, error) {
	if source.Routine == nil || source.Reference.Address != source.Routine.Address() || !auth.Live() || source.Reference.Domain() != auth.Catalog.Domain() || source.Reference.Epoch != auth.Catalog.Epoch() {
		return ids.ObjectRef{}, ids.ErrCatalogReplaced
	}
	if _, err := auth.Object(source.Reference); err != nil {
		return ids.ObjectRef{}, err
	}
	if source.Consumer != nil && !preparation.StoredReferenceValid(auth.Catalog, source.Consumer, source.Reference) {
		return ids.ObjectRef{}, ids.ErrCatalogReplaced
	}
	if source.Recipe != nil {
		owner, presentation, err := routineRecipeParts(source.Recipe)
		if err != nil {
			return ids.ObjectRef{}, err
		}
		if owner != source.Consumer {
			return ids.ObjectRef{}, ids.ErrCatalogReplaced
		}
		if err = validateRoutineRecipeSource(auth, source.Recipe, owner, presentation); err != nil {
			return ids.ObjectRef{}, err
		}
	}
	if source.Body.Outer != nil {
		if _, err := ValidateStoredRoutineDescriptor(auth.Catalog, source.Body.Outer); err != nil {
			return ids.ObjectRef{}, err
		}
	}
	current, err := ValidateStoredRoutineDescriptor(auth.Catalog, source.Routine)
	if err != nil {
		return ids.ObjectRef{}, err
	}
	selected, err := ValidateRoutineDescriptor(auth.Catalog, auth.Roles, captured)
	if err != nil {
		return ids.ObjectRef{}, err
	}
	// Finite executable Body edges can coexist with a parsed display recipe.
	// Use the same source representation as initializeNativeBody, including its
	// represented casts and repeated manifest occurrences.
	if len(current.Body) == 0 && current.DeclarationBody != nil {
		recipe, ok := proc.RoutineDeclarationRecipe(current.DeclarationBody)
		if !ok {
			return ids.ObjectRef{}, fmt.Errorf("stuntgres: missing native declaration source")
		}
		for ordinal := range recipe.CallCount() {
			_, part := recipe.CallPart(ordinal)
			member, err := DeclarationBodyRoutine(auth.Catalog, source.Routine, part)
			if err != nil {
				return ids.ObjectRef{}, err
			}
			if member.Address() == selected.Address() && SameRoutineDeclaration(member, captured) {
				return auth.Ref(selected.Address()), nil
			}
		}
	} else {
		for _, edge := range current.Body {
			if edge.Routine != nil && edge.Routine.Address() == selected.Address() && SameRoutineDeclaration(edge.Routine, captured) {
				return auth.Ref(selected.Address()), nil
			}
		}
	}
	return ids.ObjectRef{}, fmt.Errorf("stuntgres: unrelated native body source declaration")
}

func nativeBodyRoutineLifetime(c *Context, auth RoutineAccess, captured *catalog.Procedure, purpose catalog.DependencyPurpose) (ids.ObjectRef, bool, error) {
	source := c.nativeBody
	outer := source.parent.Site.Routine
	if outer == nil {
		return ids.ObjectRef{}, false, nil
	}
	invalid := func() (ids.ObjectRef, bool, error) {
		return ids.ObjectRef{}, true, fmt.Errorf("stuntgres: foreign native body source lineage")
	}
	if purpose != catalog.DefinitionDependency || c.RoutineBodyContext.Outer != outer || c.InitializationPhase != source.phase || source.phase != NativeBodyInitialization && source.phase != ExpandedNativeDeclarationInitialization || !source.parent.Site.Valid() {
		return invalid()
	}
	if !auth.Live() || source.parent.Site.Reference.Domain() != auth.Catalog.Domain() || source.parent.Site.Reference.Epoch != auth.Catalog.Epoch() {
		return ids.ObjectRef{}, true, ids.ErrCatalogReplaced
	}
	if auth.Owner.Sepgsql != nil {
		if !source.owner.Active() || c.RoutineInitialization != source.owner {
			return invalid()
		}
		host := c.LiveHost()
		if host == nil {
			return invalid()
		}
		if source.aggregate != nil {
			if err := host.ValidateRoutineInitializationOwner(source.parent.Owner, source.read); err != nil {
				return ids.ObjectRef{}, true, err
			}
		} else if _, err := host.ValidateRoutineInitialization(source.parent.Owner, source.parent.Site); err != nil {
			return ids.ObjectRef{}, true, err
		}
	}
	if source.aggregate != nil {
		if source.parent.Owner != source.owner || c.RoutineInitialization != source.owner || source.parent.Site.Purpose != catalog.AggregateSupportDependency ||
			source.read.Owner() != c.Catalog.Owner() || source.read.Domain() != c.Catalog.Domain() || source.read.Epoch() != c.Catalog.Epoch() || source.read.State() != c.Catalog.State() {
			return invalid()
		}
		if _, _, err := ValidateAggregateSupportMembership(auth.Catalog, auth.Roles, source.aggregate, outer); err != nil {
			return ids.ObjectRef{}, true, err
		}
		// Revalidate only the original parent's recipe. The child lineage
		// cannot recursively supply its own parent or a root-wide grant.
		parent := Context{
			EvalStatement: EvalStatement{Context: c.Context, Host: c.LiveHost(), Catalog: source.read},
			EvalAdmission: EvalAdmission{
				RoutineAuthority: c.RoutineAuthority, RoutineInitialization: source.parent.Owner,
				InitializationPhase: source.parent.Site.Phase, RoutineBodyContext: source.parent.Site.Body,
				RoutineGraft: source.parent.Site.Recipe != nil, RoutineGraftSource: source.parent.Site.Recipe,
				RoutineRecipe: source.recipe, RoutineChecks: source.checks, RoutineReferences: source.references,
				RoutineConsumer: source.parent.Site.Consumer,
			},
		}
		_, dependencies, err := RoutineAdmissionAuthority(&parent)
		if err != nil {
			return ids.ObjectRef{}, true, err
		}
		ref, _, err := recipeRoutineLifetime(&parent, auth, dependencies, outer, catalog.AggregateSupportDependency)
		if err != nil {
			return ids.ObjectRef{}, true, err
		}
		if ref != source.parent.Site.Reference {
			return invalid()
		}
	}
	ref, err := nativeSourceRoutineLifetime(auth, nativeSourceOf(source.parent.Site), captured)
	return ref, true, err
}

// Identity membership alone cannot change the actual source occurrence. Parsed
// declarations use the immutable CallPart index; finite Body recipes use their
// actual manifest ordinal. This runs before initialization or entry can grant.
func validateNativeBodyCallsite(c *Context, auth RoutineAccess, captured *catalog.Procedure, info expr.Info, ordinal uint32) error {
	outer := c.nativeBody.parent.Site.Routine
	if outer == nil {
		return nil
	}
	if len(outer.Body) == 0 && outer.DeclarationBody != nil {
		recipe, ok := proc.RoutineDeclarationRecipe(outer.DeclarationBody)
		if !ok {
			return fmt.Errorf("stuntgres: missing native declaration source")
		}
		if ordinal != 0 || info.Source != c.nativeBody.parent.Site.Source {
			return fmt.Errorf("stuntgres: foreign native declaration occurrence")
		}
		for call := range recipe.CallCount() {
			index, part := recipe.CallPart(call)
			if uint32(index+1) != info.Occurrence {
				continue
			}
			member, err := DeclarationBodyRoutine(auth.Catalog, outer, part)
			if err != nil {
				return err
			}
			if member.Address() == captured.Address() && SameRoutineDeclaration(member, captured) {
				return nil
			}
		}
		return fmt.Errorf("stuntgres: foreign native declaration call ordinal")
	}
	if info.Source != c.nativeBody.parent.Site.Source {
		return fmt.Errorf("stuntgres: foreign native body expression source")
	}
	if ordinal == 0 {
		// A real retained child expression carries its Body ordinal; the finite
		// initializer instead supplies that ordinal separately with outer Info.
		ordinal = info.Occurrence
	} else if info.Occurrence != c.nativeBody.parent.Site.Occurrence {
		return fmt.Errorf("stuntgres: foreign native body initialization occurrence")
	}
	if ordinal == 0 || int(ordinal) > len(outer.Body) {
		return fmt.Errorf("stuntgres: missing native body manifest ordinal")
	}
	edge := outer.Body[ordinal-1]
	if edge.Routine == nil || edge.Routine.Address() != captured.Address() || !SameRoutineDeclaration(edge.Routine, captured) {
		return fmt.Errorf("stuntgres: foreign native body manifest ordinal")
	}
	return nil
}
