package explain

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

func (r *Renderer) foreignOptions() catalog.ForeignExplainOptions {
	return catalog.ForeignExplainOptions{Verbose: r.options.Verbose, Costs: r.options.Costs}
}

func (r *Renderer) foreignProperties(tree *Tree, properties []catalog.ForeignExplainProperty) error {
	for _, property := range properties {
		if strings.TrimSpace(property.Name) == "" || strings.ContainsAny(property.Name, "\r\n:") || strings.ContainsAny(property.Value, "\r\n") {
			return fmt.Errorf("stuntgres: invalid foreign EXPLAIN property")
		}
		tree.details = append(tree.details, property.Name+": "+property.Value)
		if r.options.Structured() {
			r.repeated(tree, "Provider Properties", map[string]any{"Name": property.Name, "Value": property.Value})
		}
	}
	return nil
}

// Entered with Mu; callbacks run with neither engine nor replacement locks.
// Every occurrence captures its own principal and actual provider plan.
func (r *Renderer) foreignScan(tree *Tree, node *plan.SourceNode, relation *catalog.Relation) (float64, error) {
	if err := r.host.CheckRestoreEffect(); err != nil {
		return 0, err
	}
	principal := r.auth.State.CurrentUser
	mark := r.inheritedMark
	var uses []int
	if node.Occurrence >= 0 && node.Occurrence < len(r.leaves) {
		leaf := r.leaves[node.Occurrence]
		if leaf.Ref.Address == node.Ref.Address {
			mark = exec.MergeQueryRowMark(mark, leaf.Mark)
			uses = leaf.Uses
			if uses == nil {
				uses = []int{}
			}
		}
		if leaf.Ref.Address == node.Ref.Address && leaf.Principal != (authorization.PrincipalRef{}) {
			principal = leaf.Principal
		}
	}
	if node.Occurrence < 0 && node.Ref.Address == r.policyTarget && r.policyPrincipal != (authorization.PrincipalRef{}) {
		principal = r.policyPrincipal
	}
	mark = exec.EffectiveQueryRowMark(relation, mark)
	handler, request, err := fdw.ScanRequest(r.auth.View, principal, relation, uses, r.auth.RestrictedRelations())
	if err != nil {
		return 0, err
	}
	request.Parameters = r.parameters
	if node.Occurrence < 0 && node.Ref.Address == r.policyTarget {
		request.Identity = r.modifyIdentity
	}
	source := &exec.ForeignQuerySource{Wrapper: handler.Wrapper, Handler: handler, Selection: exec.SelectForeignHandler(r.auth.Catalog, ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(request.HandlerOID)}), Request: request}
	captured := r.auth.Catalog.State()
	r.host.ReleaseEngineLocked()
	r.host.ReleaseReplacementRead()
	if err := source.InitializeHandler(r.host); err != nil {
		r.host.ReacquireEngine()
		return 0, err
	}
	var execution exec.ForeignExecution
	if r.rowMarked {
		request, err = exec.PlanForeignRowMark(r.host.Context(), source.Wrapper, request, mark)
	}
	if err == nil {
		err = source.Plan(r.host.Context(), request, &execution)
	}
	var properties []catalog.ForeignExplainProperty
	if err == nil {
		if provider, ok := source.Wrapper.(catalog.ForeignExplainScanProvider); ok {
			path, planned := execution.Path, execution.Plan
			path.Attributes = slices.Clone(path.Attributes)
			planned.Attributes = slices.Clone(planned.Attributes)
			properties, err = provider.ExplainForeignScan(r.host.Context(), catalog.ForeignExplainScanRequest{ForeignRequest: exec.CopyForeignRequest(request), Size: execution.Size, Path: path, Plan: planned, Options: r.foreignOptions()})
			err = exec.ForeignCallbackError(err)
		}
	}
	// Copy properties before returning to mutable engine ownership.
	properties = slices.Clone(properties)
	r.host.ReacquireEngine()
	if err != nil {
		return 0, err
	}
	if err = r.ForeignFence(captured); err != nil {
		return 0, err
	}
	if err = r.foreignProperties(tree, properties); err != nil {
		return 0, err
	}
	if r.options.Costs {
		tree.details = append(tree.details, fmt.Sprintf("Provider estimates: startup cost=%.2f total cost=%.2f rows=%.0f width=%.0f", execution.Path.StartupCost, execution.Path.TotalCost, execution.Size.Rows, execution.Size.Width))
		if r.options.Structured() {
			r.property(tree, "Provider Estimates", map[string]float64{"Startup Cost": execution.Path.StartupCost, "Total Cost": execution.Path.TotalCost, "Plan Rows": execution.Size.Rows, "Plan Width": execution.Size.Width})
		}
	}
	return execution.Size.Rows, nil
}

func (r *Renderer) ForeignFence(captured *catalog.State) error {
	if err := r.host.Context().Err(); err != nil {
		return err
	}
	current, err := r.host.CurrentAccessLocked()
	if err != nil {
		return err
	}
	if current.Catalog.State() != captured || current.Roles != r.auth.Roles {
		return ids.ErrCatalogReplaced
	}
	return nil
}

func (r *Renderer) foreignModify(tree *Tree, relation *catalog.Relation, operation catalog.ForeignModifyOperation) error {
	if err := r.host.CheckRestoreEffect(); err != nil {
		return err
	}
	handler, request, err := fdw.ScanRequest(r.auth.View, r.policyPrincipal, relation, nil, r.auth.RestrictedRelations())
	if err != nil {
		return err
	}
	request.Parameters = r.parameters
	source := &exec.ForeignQuerySource{Wrapper: handler.Wrapper, Handler: handler, Selection: exec.SelectForeignHandler(r.auth.Catalog, ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(request.HandlerOID)}), Request: request}
	captured := r.auth.Catalog.State()
	r.host.ReleaseEngineLocked()
	r.host.ReleaseReplacementRead()
	if err := source.InitializeHandler(r.host); err != nil {
		r.host.ReacquireEngine()
		return err
	}
	modification, err := fdw.PlanModification(r.host.Context(), source, operation, false)
	var properties []catalog.ForeignExplainProperty
	if err == nil {
		if provider, ok := source.Wrapper.(catalog.ForeignExplainModifyProvider); ok {
			properties, err = provider.ExplainForeignModify(r.host.Context(), catalog.ForeignExplainModifyRequest{ForeignModifyRequest: modification.CallbackRequest(), Options: r.foreignOptions()})
			err = exec.ForeignCallbackError(err)
		}
	}
	properties = slices.Clone(properties)
	r.host.ReacquireEngine()
	if err != nil {
		return err
	}
	if err = r.ForeignFence(captured); err != nil {
		return err
	}
	targets := modification.Request.Targets
	targets.Names = slices.Clone(targets.Names)
	r.modifyIdentity = &targets
	return r.foreignProperties(tree, properties)
}
