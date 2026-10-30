package stored

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// ValidateRoutineCallIdentities checks frozen calls against current declarations
// without replacing their captured handles. A handle and its lifetime manifest
// belong to the same original binding; relabeling must not mix generations.
func ValidateRoutineCallIdentities(read catalog.Read, b analyze.Binding) error {
	mapper := NewRuleTreeMapper(read, nil, 0, nil, RuleTreeMapping{Leaf: func(node expr.Expr) (expr.Expr, bool, error) {
		call, ok := node.(plan.SQLRoutineExpr)
		if !ok {
			return node, false, nil
		}
		_, err := currentRoutineCall(read, call)
		return node, false, err
	}})
	_, err := MapRuleBindingRecipes(b, mapper)
	return err
}

func currentRoutineCall(read catalog.Read, call plan.SQLRoutineExpr) (*catalog.Procedure, error) {
	if call.Procedure == nil || call.Ref.Address != call.Procedure.Address() || read.Owner() == nil || call.Ref.Domain() != read.Owner().Domain || call.Ref.Epoch != read.Epoch() {
		return nil, ids.ErrCatalogReplaced
	}
	current, ok := read.Procedure(call.Ref.Address)
	if !ok || current.RoutineDefinition() == nil || !exec.SameRoutineDeclaration(current, call.Procedure) || current.RoutineKind != call.Procedure.RoutineKind || !call.Signature.ValidFor(current.RoutineDefinition()) {
		return nil, ids.ErrCatalogReplaced
	}
	derived, err := call.Collate(func(info expr.Info, inputs ...expr.Expr) (expr.Info, error) {
		return analyze.CollatedInfo(read, info, inputs...)
	})
	if err != nil {
		return nil, err
	}
	if derived.InputCollation != call.InputCollation {
		return nil, fmt.Errorf("stuntgres: mismatched SQL routine input collation")
	}
	if call.InputCollation.ID != 0 {
		if err := ValidateUsageCollation(read, sqltype.Usage{Collation: call.InputCollation}); err != nil {
			return nil, err
		}
	}
	return current, nil
}

// Type evolution keeps logical identities and references, but every affected
// declaration and capture must own the destination descriptor generation.
type routineTypeDependent struct {
	source   *catalog.Procedure
	defaults bool
}

// This is the refresh owner's reverse index, not the semantic DROP graph.
// Support-only handles must refresh even though an aggregate has no SQL body.
func routineTypeDependents(state *catalog.State) map[ids.ObjectAddress][]routineTypeDependent {
	reverse := make(map[ids.ObjectAddress][]routineTypeDependent)
	for _, descriptor := range state.Descriptors() {
		d, ok := descriptor.(*catalog.Procedure)
		if !ok || d.RoutineDefinition() == nil {
			continue
		}
		add := func(dependencies []catalog.ExpressionDependency, defaults bool) {
			for _, dependency := range dependencies {
				if dependency.Ref.Address.ClassID == ids.ProcedureClassID {
					edge := routineTypeDependent{source: d, defaults: defaults}
					duplicate := false
					for _, old := range reverse[dependency.Ref.Address] {
						duplicate = duplicate || old == edge
					}
					if !duplicate {
						reverse[dependency.Ref.Address] = append(reverse[dependency.Ref.Address], edge)
					}
				}
			}
		}
		if d.SQLDefaults != nil {
			add(d.SQLDefaults.Dependencies(), true)
		}
		if d.SQLBody != nil {
			add(d.SQLBody.Dependencies(), false)
		}
	}
	return reverse
}

// Unchanged declarations and payloads retain their immutable pointer identity.
func reownRoutineTypes(view catalog.Read, state *catalog.State, typ func(*sqltype.Descriptor) *sqltype.Descriptor, remap *catalog.RoutineRemap) error {
	return reownRoutineDeclarations(view,state,typ,remap,nil)
}

// transformRoutineRefresh is a same-catalog definition operation, never a
// registration/type import. Only its closed seed/dependent set gets new stamps.
type transformRevisionChange struct { before,after ids.DefinitionRevision }
type transformRoutineRefresh struct {
	pair *catalog.Transform
	revisions map[ids.ObjectAddress]transformRevisionChange
	revision func()(ids.DefinitionRevision,error)
}

// RefreshTransformRoutines uses the existing stabilized declaration/capture
// refresh owner and stored-expression owner. Every returned descriptor and mapped
// payload is private; callers publish them together or discard the candidate.
func RefreshTransformRoutines(source,target catalog.Read,pair *catalog.Transform,revision func()(ids.DefinitionRevision,error))([]catalog.Descriptor,error) {
	if source.Owner()==nil || source.Owner()!=target.Owner() || source.Epoch()!=target.Epoch() { return nil,ids.ErrCatalogReplaced }
	if pair==nil { return nil,ids.ErrCatalogReplaced }
	previous,ok:=source.Get(pair.Address()).(*catalog.Transform)
	if !ok || previous.Type!=pair.Type || previous.Language!=pair.Language || previous.SQLType!=pair.SQLType || previous.Runtime!=pair.Runtime { return nil,ids.ErrCatalogReplaced }
	if err:=(catalog.TransformProvider{}).Validate(target,pair);err!=nil { return nil,err }
	state:=catalog.CloneState(target.State())
	view:=target.WithState(state)
	operation:=&transformRoutineRefresh{pair:pair,revisions:make(map[ids.ObjectAddress]transformRevisionChange),revision:revision}
	var validated map[*catalog.Procedure]bool
	remap:=newRoutineRemap(view,nil,nil,nil,true,func(p *catalog.Procedure)error{
		// Validate the original against the immutable pre-mutation source.
		// This never substitutes a current body for a retained executable.
		old,ok:=source.Procedure(p.Address())
		if !ok || p.Revision()==0 || !exec.SameRoutineDeclaration(old,p) { return fmt.Errorf("stuntgres: stale original routine descriptor") }
		if !validated[old] {
			if err:=(ProcedureProvider{}).Validate(source,old);err!=nil { return err }
			if validated==nil { validated=make(map[*catalog.Procedure]bool) }
			validated[old]=true
		}
		return nil
	},func(p *catalog.Procedure,hostFresh bool)error{
		change,affected:=operation.revisions[p.Address()]
		if !affected { return nil }
		// Compatible historical COW captures retain their own body/revision.
		if p.Revision()==change.before { p.ObjectRevision=change.after }
		if p.Host!=nil && hostFresh {
			layout:=*p.Host.Layout
			if err:=layout.SetFormals(p.Host.Layout.Formals);err!=nil { return err }
			resolved,err:=catalog.ResolveHostRoutine(view,&layout,p.Host.Program,p.Language,p.Transforms)
			if err!=nil { return err }
			// The existing hostDeclarations memo owns this unpublished copy.
			// Mutating only it preserves sharing and maps each original once.
			*p.Host=*resolved
		}
		return nil
	})
	remap.RefreshDependency=func(dependency catalog.ExpressionDependency)(catalog.ExpressionDependency,error){
		if dependency.Ref.Domain()!=source.Owner().Domain || dependency.Ref.Epoch!=source.Epoch() { return catalog.ExpressionDependency{},ids.ErrCatalogReplaced }
		if change,ok:=operation.revisions[dependency.Ref.Address];ok && dependency.Revision==change.before { dependency.Revision=change.after }
		if dependency.Ref.Address==pair.Address() && dependency.Revision==previous.Revision() { dependency.Revision=pair.Revision() }
		return dependency,nil
	}
	if err:=reownRoutineDeclarations(view,state,func(t *sqltype.Descriptor)*sqltype.Descriptor{return t},remap,operation);err!=nil { return nil,err }
	if err:=reownExpressions(view,state,nil,true,remap,false,source);err!=nil { return nil,err }
	var result []catalog.Descriptor
	for address,descriptor:=range state.Descriptors() {
		if target.Get(address)==descriptor { continue }
		provider:=view.Owner().Provider(address.ClassID)
		if provider==nil { return nil,ids.ErrCatalogReplaced }
		if err:=provider.Validate(view,descriptor);err!=nil { return nil,err }
		result=append(result,descriptor)
	}
	return result,nil
}

func reownRoutineDeclarations(view catalog.Read,state *catalog.State,typ func(*sqltype.Descriptor)*sqltype.Descriptor,remap *catalog.RoutineRemap,transform *transformRoutineRefresh)error {
	type refresh struct {
		source         *catalog.Procedure
		definition     *proc.Definition
		defaults, body bool
	}
	var changes map[ids.ObjectAddress]*refresh
	var queue []ids.ObjectAddress
	for address, descriptor := range state.Descriptors() {
		if pair, ok := descriptor.(*catalog.Transform); ok {
			mapped := typ(pair.SQLType)
			if mapped != pair.SQLType {
				if mapped == nil {
					return fmt.Errorf("stuntgres: missing host pair generation")
				}
				copy := *pair
				copy.SQLType = mapped
				copy.Type.ObjectID = ids.OID(mapped.ID())
				state.SetDescriptor(address, &copy)
			}
			continue
		}
		d, ok := descriptor.(*catalog.Procedure)
		if !ok {
			continue
		}
		if transform!=nil {
			selected:=false
			for i:=range d.Transforms.Len() { selected=selected || d.Transforms.At(i).Pair==transform.pair.Address() }
			if selected {
				if changes==nil { changes=make(map[ids.ObjectAddress]*refresh) }
				changes[address]=&refresh{source:d,definition:d.RoutineDefinition(),defaults:d.SQLDefaults!=nil,body:d.SQLBody!=nil}
				queue=append(queue,address)
			}
			continue
		}
		if d.HostConversion != nil {
			if typ(d.HostConversion.Type) != d.HostConversion.Type {
				if changes == nil {
					changes = make(map[ids.ObjectAddress]*refresh)
				}
				changes[address] = &refresh{source: d}
				queue = append(queue, address)
			}
			continue
		}
		if d.RangeConstructor != nil {
			if typ(d.RangeConstructor.Type) != d.RangeConstructor.Type {
				if changes == nil {
					changes = make(map[ids.ObjectAddress]*refresh)
				}
				changes[address] = &refresh{source: d}
				queue = append(queue, address)
			}
			continue
		}
		if d.RoutineDefinition() == nil {
			continue
		}
		definition, err := remap.Definition(d.RoutineDefinition())
		if err != nil {
			return err
		}
		defaults, body := false, false
		associations := false
		for i := range d.Transforms.Len() {
			entry := d.Transforms.At(i)
			associations = associations || typ(entry.Type) != entry.Type
		}
		if d.SQLDefaults != nil {
			payload, ok := d.SQLDefaults.(*RoutineDefaults)
			if !ok {
				return fmt.Errorf("stuntgres: invalid SQL routine default payload")
			}
			for _, node := range payload.entries {
				defaults = defaults || routineExpressionChanged(view, node, typ)
			}
		}
		if d.SQLBody != nil {
			payload, err := RoutineProgramOf(d)
			if err != nil {
				return err
			}
			for _, action := range payload.actions {
				body = body || routinePresentationChanged(view, action.presentation, typ)
				for _, presentation := range action.modifierPresentations {
					body = body || routinePresentationChanged(view, presentation, typ)
				}
			}
		}
		if definition != d.RoutineDefinition() || defaults || body || associations {
			if changes == nil {
				changes = make(map[ids.ObjectAddress]*refresh)
			}
			changes[address] = &refresh{source: d, definition: definition, defaults: defaults, body: body}
			queue = append(queue, address)
		}
	}
	if len(changes) == 0 {
		return nil
	}
	// Propagate through complete runtime capture manifests, including aggregate
	// support. Aggregate descriptors have SQL=nil and cannot bridge this walk.
	reverse := routineTypeDependents(state)
	// The queue grows while visiting dependents, so its bound is not fixed.
	for i := 0; i < len(queue); i++ {
		for _, edge := range reverse[queue[i]] {
			address := edge.source.Address()
			change := changes[address]
			if change == nil {
				change = &refresh{source: edge.source, definition: edge.source.RoutineDefinition()}
				changes[address] = change
				queue = append(queue, address)
			}
			if edge.defaults {
				change.defaults = true
			} else {
				change.body = true
			}
		}
	}
	if transform!=nil {
		for address,change:=range changes {
			stamp,err:=transform.revision();if err!=nil { return err }
			transform.revisions[address]=transformRevisionChange{before:change.source.Revision(),after:stamp}
		}
	}
	// Stabilize all affected callee handles before remapping either payload.
	for address, change := range changes {
		mapped, err := remap.Procedure(change.source)
		if err != nil {
			return err
		}
		if change.source.RangeConstructor != nil || change.source.HostConversion != nil {
			state.SetDescriptor(address, mapped)
			continue
		}
		// Keep the mapper's unpublished handle through deferred attachments.
		next := mapped
		if mapped.Host != nil && transform == nil {
			host := *mapped.Host
			if err := host.SetLayout(change.definition); err != nil { return err }
			next.Host = &host
		} else if mapped.Host == nil {
			next.SQL = change.definition
		}
		state.SetDescriptor(address, next)
	}
	if err := remap.Declarations(); err != nil {
		return err
	}
	for address, change := range changes {
		current, _ := state.Procedure(address)
		if change.defaults {
			payload, err := remap.Defaults(change.source.SQLDefaults)
			if err != nil {
				return err
			}
			current.SQLDefaults = payload
		}
		if change.body {
			payload, err := remap.Body(change.source.SQLBody)
			if err != nil {
				return err
			}
			current.SQLBody = payload
		}
	}
	return remap.Finish()
}

func reownRoutineDefinition(source *proc.Definition, usage func(sqltype.Usage) (sqltype.Usage, error), programs map[*proc.PLProgram]*proc.PLProgram) (*proc.Definition, error) {
	if source.PL != nil {
		if err := source.PL.ValidateDefinition(source); err != nil {
			return nil, err
		}
	}
	next := source
	detach := func() {
		if next == source {
			copy := *source
			next = &copy
		}
	}
	for i, formal := range source.Formals {
		mapped, err := usage(formal.Usage)
		if err != nil {
			return nil, err
		}
		if !proc.DeclarationUsage(mapped) {
			return nil, fmt.Errorf("stuntgres: missing SQL routine formal generation")
		}
		if mapped != formal.Usage {
			detach()
			if next.Formals == nil || &next.Formals[0] == &source.Formals[0] {
				next.Formals = slices.Clone(source.Formals)
			}
			next.Formals[i].Usage = mapped
		}
	}
	for i, formal := range source.InputFormals {
		if next.Formals[formal].Usage != source.Inputs[i] {
			detach()
			if &next.Inputs[0] == &source.Inputs[0] {
				next.Inputs = slices.Clone(source.Inputs)
			}
			next.Inputs[i] = next.Formals[formal].Usage
		}
	}
	// Map the original occurrence through the operation-wide record memo.
	// Never reconstruct a competing descriptor or map an already mapped usage.
	result, err := usage(source.Result)
	if err != nil {
		return nil, err
	}
	if source.DerivedResult() && !next.MatchesOutputTuple(result.Type) {
		return nil, fmt.Errorf("stuntgres: mapped routine output shape disagrees with formals")
	}
	if !proc.DeclarationUsage(result) {
		return nil, fmt.Errorf("stuntgres: missing SQL routine result generation")
	}
	if result != source.Result {
		detach()
		next.Result = result
	}
	if source.PL != nil {
		program, found := programs[source.PL]
		if !found {
			program = source.PL
			for i, variable := range source.PL.Variables {
				if variable.Record && variable.Usage.Type == nil {
					continue
				}
				mapped, err := usage(variable.Usage)
				if err != nil {
					return nil, err
				}
				if !proc.DeclarationUsage(mapped) {
					return nil, fmt.Errorf("stuntgres: missing PL local type generation")
				}
				if mapped != variable.Usage {
					if program == source.PL {
						copy := *source.PL
						copy.Variables = slices.Clone(source.PL.Variables)
						program = &copy
					}
					program.Variables[i].Usage = mapped
				}
			}
			programs[source.PL] = program
		}
		if program != source.PL {
			detach()
			next.PL = program
		}
	}
	if next.PL != nil {
		if err := next.PL.ValidateDefinition(next); err != nil {
			return nil, err
		}
	}
	return next, nil
}

func routineExpressionChanged(view catalog.Read, node expr.Expr, typ func(*sqltype.Descriptor) *sqltype.Descriptor) bool {
	if node == nil {
		return false
	}
	changed := false
	analyze.WalkQueryArgumentExpression(node, func(child expr.Expr) {
		changed = changed || child.ResultUsage().Type != typ(child.ResultUsage().Type)
		if earth, ok := child.(plan.InstalledEarthExpr); ok {
			changed = changed || earth.Plan.Earth() != nil && earth.Plan.Earth() != typ(earth.Plan.Earth())
		}
		captured, err := exec.ExpressionRoutineDescriptor(child)
		if err != nil {
			changed = true
		} else if captured != nil {
			current, _ := view.Procedure(captured.Address())
			changed = changed || !exec.SameRoutineDeclaration(current, captured)
		}
	})
	return changed
}

func routinePresentationChanged(view catalog.Read, presentation *View, typ func(*sqltype.Descriptor) *sqltype.Descriptor) bool {
	if presentation == nil || presentation.query == nil {
		return true
	}
	if analyze.QueryDescriptorsChanged(presentation.query.Root, typ) || analyze.QueryRoutineDeclarationsChanged(view, presentation.query.Root) {
		return true
	}
	for _, usage := range presentation.routineInputs {
		if usage.Type != typ(usage.Type) {
			return true
		}
	}
	for _, usage := range presentation.castUsages {
		if usage.Type != typ(usage.Type) {
			return true
		}
	}
	for _, composite := range presentation.composites {
		if composite.Base.Type != typ(composite.Base.Type) {
			return true
		}
	}
	return false
}
