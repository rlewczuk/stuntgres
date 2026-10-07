package engine

import (
	"fmt"
	"sort"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func viewDepends(d *relationDescriptor, target ObjectAddress) bool {
	if d.view == nil {
		return false
	}
	if target.SubID == 0 && d.view.source.Address == target {
		return true
	}
	for _, dependency := range d.view.dependencies {
		a := dependency.ref.Address
		if a.ClassID == target.ClassID && a.ObjectID == target.ObjectID && (target.SubID == 0 || a.SubID == target.SubID) {
			return true
		}
	}
	return false
}

type viewDependencyEdge struct {
	view   *relationDescriptor
	target ObjectAddress
}

func viewDependencyClosure(catalog CatalogRead, targets []ObjectAddress) []viewDependencyEdge {
	var views []*relationDescriptor
	for _, descriptor := range catalog.state.descriptors {
		if d, ok := descriptor.(*relationDescriptor); ok && d.view != nil {
			views = append(views, d)
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].address.ObjectID < views[j].address.ObjectID })
	seen := make(map[ObjectAddress]bool, len(targets))
	for _, target := range targets {
		if target.SubID == 0 {
			seen[target] = true
		}
	}
	var edges []viewDependencyEdge
	var visit func(ObjectAddress)
	visit = func(target ObjectAddress) {
		for _, view := range views {
			if !seen[view.address] && viewDepends(view, target) {
				seen[view.address] = true
				edges = append(edges, viewDependencyEdge{view, target})
				visit(view.address)
			}
		}
	}
	for _, target := range targets {
		visit(target)
	}
	return edges
}

func dependencyObject(catalog CatalogRead, target ObjectAddress) string {
	base := target
	base.SubID = 0
	d := catalog.state.descriptors[base].(*relationDescriptor)
	object := relationDiagnosticKind(d) + " " + d.name
	if target.SubID != 0 {
		for i, attribute := range d.attributes {
			if attribute == target.SubID {
				return "column " + d.columns[i].Name + " of " + object
			}
		}
	}
	return object
}

func viewRestrictError(catalog CatalogRead, edges []viewDependencyEdge) error {
	object := dependencyObject(catalog, edges[0].target)
	details := make([]string, len(edges))
	for i, edge := range edges {
		details[i] = "view " + edge.view.name + " depends on " + dependencyObject(catalog, edge.target)
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop " + object + " because other objects depend on it", Detail: strings.Join(details, "\n"), Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
}

func stageViewDependents(stage *CatalogStage, targets []ObjectAddress, mode parser.DropMode) error {
	edges := viewDependencyClosure(stage.base, targets)
	if len(edges) == 0 {
		return nil
	}
	if mode == parser.DropRestrict {
		return viewRestrictError(stage.base, edges)
	}
	for _, edge := range edges {
		d := edge.view
		if _, exists := stage.removals[d.address]; exists {
			continue
		}
		if err := stage.Remove(ObjectRef{owner: stage.owner.owner, Epoch: stage.base.Epoch(), Address: d.address}); err != nil {
			return err
		}
		stage.viewCascadeNames = append(stage.viewCascadeNames, "drop cascades to view "+d.name)
	}
	return nil
}

func emitViewCascade(op *Operation, details []string) {
	if len(details) == 0 {
		return
	}
	message, detail := details[0], ""
	if len(details) > 1 {
		message = fmt.Sprintf("drop cascades to %d other objects", len(details))
		detail = strings.Join(details, "\n")
	}
	op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Detail: detail})
}

func validateViewDefinition(v CatalogRead, d *relationDescriptor) error {
	definition := d.view
	if definition == nil || definition.syntax == nil || definition.query == nil || definition.query.Root == nil || definition.rule == 0 || len(definition.query.Root.Schema()) != len(d.columns) {
		return fmt.Errorf("stuntgres: invalid view definition")
	}
	if err := definition.validateLiterals(v); err != nil {
		return err
	}
	if definition.source != (ObjectRef{}) {
		source, ok := v.state.descriptors[definition.source.Address].(*relationDescriptor)
		if !ok || definition.source.owner != v.owner.owner || definition.source.Epoch != v.Epoch() || source.address == d.address {
			return fmt.Errorf("stuntgres: invalid view source identity")
		}
		for _, attribute := range definition.reads {
			if !source.hasAttribute(attribute) {
				return fmt.Errorf("stuntgres: missing view dependency attribute")
			}
		}
		cursor := source
		terminated := false
		for range len(v.state.descriptors) {
			if cursor.address == d.address {
				return fmt.Errorf("stuntgres: cyclic view source")
			}
			if cursor.view == nil || cursor.view.source == (ObjectRef{}) {
				terminated = true
				break
			}
			next, ok := v.state.descriptors[cursor.view.source.Address].(*relationDescriptor)
			if !ok {
				return fmt.Errorf("stuntgres: missing view source identity")
			}
			cursor = next
		}
		if !terminated {
			return fmt.Errorf("stuntgres: cyclic view source")
		}
	}
	for _, dependency := range definition.dependencies {
		ref := dependency.ref
		address := ref.Address
		address.SubID = 0
		if ref.owner != v.owner.owner || ref.Epoch != v.Epoch() {
			return fmt.Errorf("stuntgres: invalid view dependency identity")
		}
		object, ok := v.state.descriptors[address]
		if !ok {
			return fmt.Errorf("stuntgres: missing view dependency")
		}
		if ref.Address.SubID != 0 {
			relation, ok := object.(*relationDescriptor)
			if !ok || !relation.hasAttribute(ref.Address.SubID) {
				return fmt.Errorf("stuntgres: missing view dependency attribute")
			}
		}
	}
	return nil
}
