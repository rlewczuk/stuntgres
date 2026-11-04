package exec

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// RoutineInitialization identifies one actual executor-initialization instance.
// It contains inert identity values only: in particular no transaction attachment,
// statement scope, session, observer or granted permission is retained here.
// Only the actual command owner mints an instance; an entry also needs its exact
// completed callsite receipt. Copying this value never creates that receipt.
type RoutineInitialization struct {
	domain      *ids.Domain
	session     ids.SessionID
	transaction ids.TxID
	epoch       ids.CatalogEpoch
	command     ids.CommandID
	instance    uint64
}

func NewRoutineInitialization(command ids.CommandIdentity, instance uint64) RoutineInitialization {
	return RoutineInitialization{domain: command.Transaction.Domain(), session: command.Transaction.Session().ID(), transaction: command.Transaction.ID(), epoch: command.Transaction.Epoch(), command: command.Command, instance: instance}
}
func (owner RoutineInitialization) Active() bool {
	// Command zero is the genuine first statement of an admitted transaction.
	// Domain, transaction and the actual frame nonce distinguish a missing owner.
	return owner.domain != nil && owner.transaction != 0 && owner.instance != 0
}
func (owner RoutineInitialization) Instance() uint64 { return owner.instance }
func (owner RoutineInitialization) Matches(command ids.CommandIdentity) bool {
	return owner.Active() && owner == NewRoutineInitialization(command, owner.instance)
}

type RoutineInitializationPhase uint8

const (
	RetainedExpressionInitialization RoutineInitializationPhase = iota
	NativeBodyInitialization
	AcceptedDefaultInitialization
	DomainCheckInitialization
	IndexRecipeInitialization
	TriggerWhenInitialization
	GroupingEqualityInitialization
	WindowEqualityInitialization
	ExpandedNativeDeclarationInitialization
)

// RoutineInitializationSite is the selected source, not a postargument subject
// or current SQL-role lookup. The ordinal is used only by a genuine finite native
// SQL body manifest whose executor initialization has no separate Expr node.
type RoutineInitializationSite struct {
	Reference       ids.ObjectRef
	Routine         *catalog.Procedure
	Recipe          expr.RoutineRecipeReference
	Consumer        *catalog.Relation
	Body            RoutineBodyContext
	Purpose         catalog.DependencyPurpose
	Phase           RoutineInitializationPhase
	Occurrence      uint32
	Source          expr.FoldSourceOccurrence
	ManifestOrdinal uint32
	FollowIdentity  bool
	defaultCall     *acceptedDefaultCall
}

func (site RoutineInitializationSite) Valid() bool {
	if site.Routine == nil || site.Reference.Address != site.Routine.Address() || site.Reference.Address.ClassID != ids.ProcedureClassID || site.Reference.Address.SubID != 0 {
		return false
	}
	if site.ManifestOrdinal != 0 {
		return site.Body.Outer != nil
	}
	return site.Occurrence != 0 && site.Source.Frame != 0
}

type RoutineInitializationCall struct {
	Owner RoutineInitialization
	Site  RoutineInitializationSite
}

// DefaultInitializationSource names the genuine accepted immutable artifact and
// its complete source expansion; it contains no authorization or session state.
type DefaultInitializationSource struct {
	Parent     RoutineInitialization
	Artifact   *AcceptedDefault
	Occurrence DefaultOccurrence
}

func (source DefaultInitializationSource) Valid() bool {
	return source.Parent.Active() && source.Artifact.Valid() && source.Artifact.occurrence == source.Occurrence
}

// ClosedRoutineInitialization is an immutable coordinator-sealed source proof.
// Workers neither reinterpret the initializer's actor nor acquire a new SQL ACL
// or MAC grant. The command coordinator retains final role/catalog/label fences.
// Its private copied map has no live observer or session access.
type ClosedRoutineInitialization struct {
	owner  RoutineInitialization
	read   catalog.Read
	roles  authorization.Read
	record *access.RoleRecord
	calls  map[RoutineInitializationSite]authorization.PrincipalRef
	direct map[DirectRoutineInitializationSite]struct{}
}

type RoutineInitializationReceipt struct {
	Site  RoutineInitializationSite
	Actor authorization.PrincipalRef
}

func SealClosedRoutineInitialization(owner RoutineInitialization, read catalog.Read, record *access.RoleRecord, receipts []RoutineInitializationReceipt, direct []DirectRoutineInitializationSite) (ClosedRoutineInitialization, error) {
	if !owner.Active() || read.State() == nil || read.Owner() == nil || read.Owner().Sepgsql == nil || read.Owner().Sepgsql.AnyTrustedTransitions() {
		return ClosedRoutineInitialization{}, fmt.Errorf("stuntgres: invalid closed routine initialization source")
	}
	roles := access.RoleRead(read.Owner(), read.Owner().Principals, read, record)
	proof := ClosedRoutineInitialization{owner: owner, read: read, roles: roles, record: record, calls: make(map[RoutineInitializationSite]authorization.PrincipalRef, len(receipts))}
	for _, receipt := range receipts {
		if !receipt.Site.Valid() {
			return ClosedRoutineInitialization{}, fmt.Errorf("stuntgres: invalid closed routine initialization callsite")
		}
		if err := roles.ValidatePrincipal(receipt.Actor); err != nil {
			return ClosedRoutineInitialization{}, err
		}
		if previous, exists := proof.calls[receipt.Site]; exists && previous != receipt.Actor {
			return ClosedRoutineInitialization{}, fmt.Errorf("stuntgres: conflicting initialized routine actor")
		}
		proof.calls[receipt.Site] = receipt.Actor
	}
	if len(direct) != 0 {
		proof.direct = make(map[DirectRoutineInitializationSite]struct{}, len(direct))
		for _, site := range direct {
			if !site.Valid() {
				return ClosedRoutineInitialization{}, fmt.Errorf("stuntgres: invalid closed direct routine source")
			}
			proof.direct[site] = struct{}{}
		}
	}
	return proof, nil
}

// checkSource authenticates only the sealed owner and exact selected read.
// Native aggregate kernels have aggregate membership, not scalar call receipts.
func (proof ClosedRoutineInitialization) checkSource(owner RoutineInitialization, read catalog.Read) error {
	if !owner.Active() || owner != proof.owner || read.Owner() != proof.read.Owner() || read.Domain() != proof.read.Domain() || read.Epoch() != proof.read.Epoch() || read.State() != proof.read.State() || read.Owner() == nil || read.Owner().Sepgsql == nil || read.Owner().Sepgsql.AnyTrustedTransitions() {
		return fmt.Errorf("stuntgres: missing sealed routine initialization source")
	}
	return nil
}

func (proof ClosedRoutineInitialization) Check(owner RoutineInitialization, site RoutineInitializationSite, read catalog.Read) error {
	if !site.Valid() || proof.checkSource(owner, read) != nil {
		return fmt.Errorf("stuntgres: missing sealed routine initialization source")
	}
	actor, ok := proof.calls[site]
	if !ok {
		return fmt.Errorf("stuntgres: missing initialized routine callsite")
	}
	if err := proof.roles.ValidatePrincipal(actor); err != nil {
		return err
	}
	auth := RoutineAccess{}
	auth.Owner, auth.Catalog, auth.PairedCatalog, auth.Roles = read.Owner(), read, read, proof.roles
	auth.RoleRecord = proof.record
	auth.State.CurrentUser = actor
	dependency := [1]catalog.ExpressionDependency{{Ref: site.Reference, Revision: site.Routine.Revision(), Purpose: site.Purpose}}
	_, err := routineDescriptorLifetime(auth, dependency[:], site.Routine, site.Consumer, site.FollowIdentity, site.Purpose)
	return err
}

// BeginRoutineInitialization belongs to a genuine initializer before it makes
// recipe/default/body context copies. State.Evaluation never calls it on pulls.
func BeginRoutineInitialization(c *Context) error {
	if c == nil {
		return fmt.Errorf("stuntgres: missing routine initialization context")
	}
	if c.routineSourceError != nil {
		return c.routineSourceError
	}
	if c.ControlLiteralAuthority.present() {
		_, _, err := c.controlLiteralAccess()
		return err
	}
	if c.Catalog.Owner() == nil {
		var auth RoutineAccess
		if c.RoutineAuthority != nil {
			auth = c.RoutineAuthority.AdmissionAccess()
		} else if host := c.LiveHost(); host != nil {
			var err error
			auth, _, err = host.RoutineAdmission()
			if err != nil {
				return err
			}
		}
		if auth.Owner == nil || auth.Owner.Sepgsql == nil {
			return nil
		}
		c.Catalog = auth.Catalog
	}
	if c.Catalog.Owner().Sepgsql == nil {
		return nil
	}
	if c.RoutineInitialization.Active() {
		return nil
	}
	if c.nativeBody.parent.Site.Routine != nil {
		return fmt.Errorf("stuntgres: missing derived native body initialization owner")
	}
	if c.Query != nil || c.ClosedEvaluation != nil {
		return fmt.Errorf("stuntgres: missing retained execution initialization owner")
	}
	host := c.LiveHost()
	if host == nil {
		return fmt.Errorf("stuntgres: missing actual routine initializer")
	}
	owner, err := host.NewRoutineInitialization()
	if err != nil {
		return err
	}
	c.RoutineInitialization = owner
	return nil
}

func routineInitializationSite(c *Context, auth RoutineAccess, dependencies []catalog.ExpressionDependency, declaration *catalog.Procedure, info expr.Info, ordinal uint32, purpose catalog.DependencyPurpose) (RoutineInitializationSite, error) {
	ref, accepted, err := recipeRoutineLifetime(c, auth, dependencies, declaration, purpose)
	if err != nil {
		return RoutineInitializationSite{}, err
	}
	if accepted {
		ref = auth.View.Ref(declaration.Address())
	}
	if err := validateNativeBodyCallsite(c, auth, declaration, info, ordinal); err != nil {
		return RoutineInitializationSite{}, err
	}
	var recipe expr.RoutineRecipeReference
	if c.RoutineGraft {
		recipe = c.RoutineGraftSource
	}
	site := RoutineInitializationSite{Reference: ref, Routine: declaration, Recipe: recipe, Consumer: c.RoutineConsumer, Body: c.RoutineBodyContext, Purpose: purpose, Phase: c.InitializationPhase, Occurrence: info.Occurrence, Source: info.Source, ManifestOrdinal: ordinal, FollowIdentity: c.RoutineChecks != nil || c.RoutineReferences != nil, defaultCall: c.defaultCall}
	if !site.Valid() {
		return RoutineInitializationSite{}, fmt.Errorf("stuntgres: missing genuine routine initialization occurrence")
	}
	return site, nil
}

// initializeRoutineExpression is used only by a real retained expression/body
// initializer. Descriptor metadata checks and postargument entry never call it.
func initializeRoutineExpression(c *Context, auth RoutineAccess, dependencies []catalog.ExpressionDependency, declaration *catalog.Procedure, info expr.Info, ordinal uint32) error {
	if err := BeginRoutineInitialization(c); err != nil {
		return err
	}
	if c.ClosedEvaluation != nil {
		return fmt.Errorf("stuntgres: closed evaluator cannot initialize a routine")
	}
	site, err := routineInitializationSite(c, auth, dependencies, declaration, info, ordinal, catalog.DefinitionDependency)
	if err != nil {
		return err
	}
	host := c.LiveHost()
	if host == nil {
		return fmt.Errorf("stuntgres: missing actual routine initializer")
	}
	if err = host.InitializeRoutineCall(c.RoutineInitialization, site, auth.State.CurrentUser); err != nil {
		return err
	}
	if c.RoutineProgram != nil {
		return c.RoutineProgram.Record(c, dependencies, declaration)
	}
	return nil
}

func invocationRoutineInitializationSite(c *Context, declaration *catalog.Procedure, info expr.Info, purpose catalog.DependencyPurpose) RoutineInitializationSite {
	var recipe expr.RoutineRecipeReference
	if c.RoutineGraft {
		recipe = c.RoutineGraftSource
	}
	if c.Catalog.Owner() == nil {
		return RoutineInitializationSite{}
	}
	return RoutineInitializationSite{Reference: c.Catalog.Owner().Domain.Ref(c.Catalog.Epoch(), declaration.Address()), Routine: declaration, Recipe: recipe, Consumer: c.RoutineConsumer, Body: c.RoutineBodyContext, Purpose: purpose, Phase: c.InitializationPhase, Occurrence: info.Occurrence, Source: info.Source, FollowIdentity: c.RoutineChecks != nil || c.RoutineReferences != nil, defaultCall: c.defaultCall}
}

func validateRoutineExpressionInitialization(c *Context, declaration *catalog.Procedure, info expr.Info) error {
	if !c.RoutineInitialization.Active() {
		return fmt.Errorf("stuntgres: missing actual routine initialization owner")
	}
	site := invocationRoutineInitializationSite(c, declaration, info, catalog.DefinitionDependency)
	if c.ClosedEvaluation != nil {
		return c.ClosedEvaluation.Routines.Check(c.RoutineInitialization, site, c.Catalog)
	}
	auth, dependencies, err := RoutineAdmissionAuthority(c)
	if err != nil {
		return err
	}
	if !auth.Live() {
		return fmt.Errorf("stuntgres: missing initialized routine source authority")
	}
	site, err = routineInitializationSite(c, auth, dependencies, declaration, info, 0, catalog.DefinitionDependency)
	if err != nil {
		return err
	}
	host := c.LiveHost()
	if host == nil {
		return fmt.Errorf("stuntgres: missing initialized routine invocation owner")
	}
	_, err = host.ValidateRoutineInitialization(c.RoutineInitialization, site)
	return err
}

// BeginNativeBody is called only after the genuine outer invocation has entered.
// The returned value is retained by the existing body/source owner across rows.
func BeginNativeBody(c *Context, outer *catalog.Procedure, info expr.Info) (Context, error) {
	return beginNativeBodyInitialization(c, outer, info, NativeBodyInitialization, nil)
}

// BeginExpandedNativeDeclaration owns the retained expressions of the selected
// expanded declaration when its strict outer call is suppressed. It authenticates
// the original call receipt, but neither enters the outer body nor changes the
// caller's subject. The real source adapter still owns each demanded inner call.
func BeginExpandedNativeDeclaration(c *Context, outer *catalog.Procedure, info expr.Info) (Context, error) {
	if outer == nil || outer.DeclarationBody == nil {
		return Context{}, fmt.Errorf("stuntgres: missing expanded native declaration")
	}
	body, err := beginNativeBodyInitialization(c, outer, info, ExpandedNativeDeclarationInitialization, nil)
	if err != nil {
		return Context{}, err
	}
	if err = initializeNativeDeclarationBody(&body, outer, info, retainedNativeDeclarationCalls); err != nil {
		return Context{}, err
	}
	return body, nil
}

func beginNativeBodyInitialization(c *Context, outer *catalog.Procedure, info expr.Info, phase RoutineInitializationPhase, aggregateParent *nativeAggregateSource) (Context, error) {
	if c == nil || outer == nil {
		return Context{}, fmt.Errorf("stuntgres: missing entered native SQL body")
	}
	active, err := routineSimulationActive(c)
	if err != nil {
		return Context{}, err
	}
	body := *c
	body.RoutineBodyContext = RoutineBodyContext{Outer: outer, Statement: !c.ConstantPlanning || bodyErrorContext(outer, c.ConstantPlanning).Statement}
	body.declarationDemand = retainedNativeDeclarationCalls
	body.InitializationPhase = phase
	body.AcceptedDefault, body.defaultActivation, body.defaultCall = nil, nil, nil
	body.RoutineInitialization = RoutineInitialization{}
	body.nativeBody = nativeBodyLineage{}
	body.nativeAggregate = nativeAggregateSource{}
	body.nativeAggregateEntered = false
	body.routineSource = routineSourceBinding{}
	auth, dependencies, err := RoutineAdmissionAuthority(c)
	if err != nil {
		return Context{}, err
	}
	if auth.Live() {
		purpose := catalog.DefinitionDependency
		if aggregateParent != nil {
			if phase != NativeBodyInitialization || aggregateParent.self != nil ||
				aggregateParent.owner != c.RoutineInitialization || aggregateParent.support == nil || aggregateParent.reference != outer ||
				aggregateParent.support != outer && !SameRoutineDeclaration(aggregateParent.support, outer) ||
				aggregateParent.occurrence != info.Occurrence || aggregateParent.source != info.Source || aggregateParent.purpose != catalog.AggregateSupportDependency ||
				aggregateParent.read.Owner() != c.Catalog.Owner() || aggregateParent.read.Domain() != c.Catalog.Domain() || aggregateParent.read.Epoch() != c.Catalog.Epoch() || aggregateParent.read.State() != c.Catalog.State() {
				return Context{}, fmt.Errorf("stuntgres: foreign native aggregate body parent")
			}
			purpose = catalog.AggregateSupportDependency
		}
		site, err := routineInitializationSite(c, auth, dependencies, outer, info, 0, purpose)
		if err != nil {
			return Context{}, err
		}
		body.Catalog = auth.Catalog // The authenticated source's snapshot, not a retained presentation root.
		source := RoutineInitializationCall{Owner: c.RoutineInitialization, Site: site}
		body.nativeBody = nativeBodyLineage{parent: source, phase: phase}
		if aggregateParent != nil {
			if c.ClosedEvaluation != nil {
				return Context{}, fmt.Errorf("stuntgres: native SQL body requires serial initialization")
			}
			if site.Recipe != aggregateParent.recipe {
				return Context{}, fmt.Errorf("stuntgres: foreign native aggregate body recipe")
			}
			body.Catalog = aggregateParent.read
			body.RoutineInitialization = aggregateParent.owner
			body.nativeBody.owner = aggregateParent.owner
			body.nativeBody.aggregate, body.nativeBody.read = aggregateParent.aggregate, aggregateParent.read
			body.nativeBody.checks, body.nativeBody.references, body.nativeBody.recipe = c.RoutineChecks, c.RoutineReferences, c.RoutineRecipe
			return body, nil
		}
		if active {
			if c.ClosedEvaluation != nil {
				return Context{}, fmt.Errorf("stuntgres: native SQL body requires serial initialization")
			}
			host := c.LiveHost()
			if host == nil {
				return Context{}, fmt.Errorf("stuntgres: missing entered native SQL initializer")
			}
			body.RoutineInitialization, err = host.NativeBodyRoutineInitialization(source, phase)
			if err != nil {
				return Context{}, err
			}
			body.nativeBody.owner = body.RoutineInitialization
		}
	} else if active {
		return Context{}, fmt.Errorf("stuntgres: missing original native SQL source authority")
	}
	return body, nil
}

// InitializeNativeBodyCall initializes a real selected expression in an entered
// native SQL wrapper. Its original occurrence is also used at actual leaf entry.
func InitializeNativeBodyCall(c *Context, inner *catalog.Procedure, info expr.Info) error {
	if c == nil || c.RoutineBodyContext.Outer == nil {
		return fmt.Errorf("stuntgres: missing native SQL body source")
	}
	auth, dependencies, err := RoutineInvocationAuthority(c)
	if err != nil {
		return err
	}
	if auth.Owner == nil || auth.Owner.Sepgsql == nil {
		if _, err := routineInitializationSite(c, auth, dependencies, inner, info, 0, catalog.DefinitionDependency); err != nil {
			return err
		}
		return AdmitRecipeRoutineDescriptor(c, auth, dependencies, inner)
	}
	return initializeRoutineExpression(c, auth, dependencies, inner, info, 0)
}

type DirectRoutinePurpose uint8

const (
	DirectGroupingEqual DirectRoutinePurpose = iota + 1
	DirectHash
	DirectOrdering
	DirectIndexSupport
	DirectAccessMethod
	DirectSample
	DirectForeign
)

type DirectRoutineInitializationSite struct {
	Routine          *catalog.Procedure
	Reference        ids.ObjectRef
	Purpose          DirectRoutinePurpose
	Source           ids.ObjectRef
	Ordinal          uint16
	Occurrence       uint32
	ExpressionSource expr.FoldSourceOccurrence
}

func (site DirectRoutineInitializationSite) Valid() bool {
	return site.Routine != nil && site.Reference.Address == site.Routine.Address() && site.Reference.Address.ClassID == ids.ProcedureClassID && site.Reference.Address.SubID == 0 && site.Purpose >= DirectGroupingEqual && site.Purpose <= DirectForeign && (site.Source.Address.ObjectID != 0 || site.Occurrence != 0 && site.ExpressionSource.Frame != 0)
}

type DirectRoutineInitializationCall struct {
	Owner RoutineInitialization
	Site  DirectRoutineInitializationSite
}

func BeginDirectRoutineInitialization(c *Context, site DirectRoutineInitializationSite) error {
	if c == nil {
		return fmt.Errorf("stuntgres: missing direct routine initializer")
	}
	if err := BeginRoutineInitialization(c); err != nil {
		return err
	}
	if owner := c.Catalog.Owner(); owner == nil || owner.Sepgsql == nil {
		return nil
	}
	if c.ClosedEvaluation != nil {
		return c.ClosedEvaluation.Routines.CheckDirect(c.RoutineInitialization, site, c.Catalog)
	}
	host := c.LiveHost()
	if host == nil {
		return fmt.Errorf("stuntgres: missing direct routine source owner")
	}
	return host.InitializeDirectRoutine(c.RoutineInitialization, site)
}
func (c *Context) EnterInitializedDirectRoutine(site DirectRoutineInitializationSite) (sqltype.RoutineBodyToken, error) {
	if c == nil {
		return sqltype.RoutineBodyToken{}, fmt.Errorf("stuntgres: missing direct routine invocation owner")
	}
	active, err := routineSimulationActive(c)
	if err != nil {
		return sqltype.RoutineBodyToken{}, err
	}
	if !active {
		return sqltype.RoutineBodyToken{}, nil
	}
	if c.ClosedEvaluation != nil {
		return sqltype.RoutineBodyToken{}, c.ClosedEvaluation.Routines.CheckDirect(c.RoutineInitialization, site, c.Catalog)
	}
	host := c.LiveHost()
	if host == nil {
		return sqltype.RoutineBodyToken{}, fmt.Errorf("stuntgres: missing direct routine invocation owner")
	}
	if err := host.ValidateDirectRoutineInitialization(c.RoutineInitialization, site); err != nil {
		return sqltype.RoutineBodyToken{}, err
	}
	return host.EnterDirectRoutine(site.Routine, site.Reference)
}
func (proof ClosedRoutineInitialization) CheckDirect(owner RoutineInitialization, site DirectRoutineInitializationSite, read catalog.Read) error {
	if !site.Valid() || proof.checkSource(owner, read) != nil {
		return fmt.Errorf("stuntgres: missing sealed direct routine source")
	}
	if _, ok := proof.direct[site]; !ok {
		return fmt.Errorf("stuntgres: missing initialized direct routine source")
	}
	if site.Reference.Domain() != read.Domain() || site.Reference.Epoch != read.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if _, err := ValidateRoutineDescriptor(read, proof.roles, site.Routine); err != nil {
		return err
	}
	if site.Source.Address.ObjectID != 0 && (site.Source.Domain() != read.Domain() || site.Source.Epoch != read.Epoch() || read.Get(site.Source.Address) == nil) {
		return ids.ErrCatalogReplaced
	}
	return nil
}

// Entry may diagnose missing copied authority, never adopt a current catalog or
// mint an initializer merely because the live host belongs to an active driver.
func routineSimulationActive(c *Context) (bool, error) {
	if c.routineSourceError != nil {
		return false, c.routineSourceError
	}
	if owner := c.Catalog.Owner(); owner != nil {
		return owner.Sepgsql != nil, nil
	}
	if c.RoutineInitialization.Active() {
		return false, fmt.Errorf("stuntgres: missing initialized routine catalog authority")
	}
	auth, _, err := RoutineAdmissionAuthority(c)
	if err != nil {
		return false, err
	}
	if auth.Owner != nil && auth.Owner.Sepgsql != nil {
		return false, fmt.Errorf("stuntgres: missing initialized routine catalog authority")
	}
	return false, nil
}

// Validate the actual owner/header/View/lexical association once at source
// opening. Evaluators also retain its error rather than adopting ambient Scope
// authority if a caller constructs a detached frame directly.
func (s *State) validateRoutineSourceCatalog() error {
	owner := s.RoutineInitialization
	var header, view, outer catalog.Read
	if s.Source != nil {
		if s.Source.Authority != nil {
			authority := s.Source.Authority
			if owner.Active() && authority.RoutineInitialization.Active() && owner != authority.RoutineInitialization {
				return fmt.Errorf("stuntgres: conflicting routine source initialization owners")
			}
			if !owner.Active() {
				owner = authority.RoutineInitialization
			}
			header = authority.RoutineCatalog
		}
		if s.Source.View != nil {
			view = s.Source.View.Catalog()
		}
	}
	if s.Outer != nil {
		outer = s.Outer.Catalog
	}
	active := owner.Active() || s.ClosedEvaluation != nil && s.ClosedEvaluation.Routines.owner.Active()
	for _, read := range [3]catalog.Read{header, view, outer} {
		if read.Owner() != nil && read.Owner().Sepgsql != nil {
			active = true
		}
	}
	if !active {
		return nil
	}
	if !owner.Active() {
		return fmt.Errorf("stuntgres: missing explicit routine source initialization owner")
	}
	found := false
	var catalogOwner *catalog.Owner
	for _, read := range [3]catalog.Read{header, view, outer} {
		if read.Owner() == nil {
			continue
		}
		if catalogOwner != nil && catalogOwner != read.Owner() {
			return ids.ErrCatalogReplaced
		}
		catalogOwner = read.Owner()
		found = true
		if owner.domain != read.Domain() || owner.epoch != read.Epoch() {
			return ids.ErrCatalogReplaced
		}
	}
	if !found {
		return fmt.Errorf("stuntgres: missing explicit routine source catalog")
	}
	return nil
}
