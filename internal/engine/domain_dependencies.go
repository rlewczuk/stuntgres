package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type domainDependency struct {
	address, target, root ObjectAddress
	oid                   OID
	ordinal               int32
	label                 string
	relation              *relationDescriptor
	column                string
}

// PostgreSQL dependency traversal visits a dependent domain's descendants before
// resuming its parent's remaining dependents. Stable attributes are separate
// dependents; a containing table is never a domain-drop target.
func domainDependencyClosure(ctx context.Context, stage *CatalogStage) ([]domainDependency, error) {
	domains := make(map[ObjectAddress]bool, len(stage.domainTargets))
	for _, a := range stage.domainTargets {
		domains[a] = true
	}
	for changed := true; changed; {
		changed = false
		for a, d := range stage.base.state.descriptors {
			if t, ok := d.(*typeDescriptor); ok && t.typ.IsDomain() && !domains[a] && domains[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(t.typ.DomainBase().Type.ID())}] {
				domains[a] = true
				changed = true
			}
		}
	}
	droppedColumns := make(map[ObjectAddress]bool)
	for a, d := range stage.base.state.descriptors {
		r, ok := d.(*relationDescriptor)
		if !ok || r.relationKind != tableRelation {
			continue
		}
		for i, c := range r.columns {
			if domains[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(c.Usage.Type.ID())}] {
				column := a
				column.SubID = r.attributes[i]
				droppedColumns[column] = true
			}
		}
	}
	seen := make(map[ObjectAddress]bool)
	for _, a := range stage.domainTargets {
		seen[a] = true
	}
	var result []domainDependency
	var visit func(ObjectAddress, ObjectAddress) error
	visit = func(target, root ObjectAddress) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var direct []domainDependency
		for a, descriptor := range stage.base.state.descriptors {
			if _, removed := stage.removals[a]; removed {
				continue
			}
			switch d := descriptor.(type) {
			case *typeDescriptor:
				if d.typ.IsDomain() && target == (ObjectAddress{ClassID: TypeClassID, ObjectID: OID(d.typ.DomainBase().Type.ID())}) {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: "type " + typeDiagnosticNameAt(stage.base, d, stage.dependencyPath)})
				}
			case *relationDescriptor:
				if replacement, ok := stage.descriptors[a].(*relationDescriptor); ok {
					d = replacement
				}
				if d.view != nil {
					if viewDepends(d, target) {
						direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: "view " + dependencyRelationName(stage.base, d, stage.dependencyPath), relation: d})
					}
					continue
				}
				if d.relationKind != tableRelation {
					continue
				}
				name := dependencyRelationName(stage.base, d, stage.dependencyPath)
				for i, c := range d.columns {
					column := a
					column.SubID = d.attributes[i]
					if target == (ObjectAddress{ClassID: TypeClassID, ObjectID: OID(c.Usage.Type.ID())}) {
						direct = append(direct, domainDependency{address: column, target: target, root: root, oid: a.ObjectID, ordinal: column.SubID, label: "column " + dependencyIdentifier(c.Name) + " of table " + name, relation: d, column: c.Name})
					}
					if droppedColumns[column] {
						continue
					}
					if definition, ok := d.defaults[column.SubID]; ok {
						for _, ref := range definition.dependencies {
							if ref.Address == target {
								direct = append(direct, domainDependency{address: ObjectAddress{ClassID: 2604, ObjectID: definition.OID}, target: target, root: root, oid: definition.OID, label: "default value for column " + dependencyIdentifier(c.Name) + " of table " + name, relation: d})
								break
							}
						}
					}
				}
				for _, check := range d.checkConstraints {
					automatic := false
					depends := false
					for _, dep := range check.dependencies {
						automatic = automatic || droppedColumns[dep.ref.Address]
						depends = depends || dep.ref.Address == target
					}
					if depends && !automatic {
						direct = append(direct, domainDependency{address: ObjectAddress{ClassID: 2606, ObjectID: check.OID}, target: target, root: root, oid: check.OID, label: "constraint " + dependencyIdentifier(check.Name) + " on table " + name, relation: d})
					}
				}
			}
		}
		sort.Slice(direct, func(i, j int) bool {
			if direct[i].oid != direct[j].oid {
				return direct[i].oid < direct[j].oid
			}
			return direct[i].ordinal < direct[j].ordinal
		})
		for _, edge := range direct {
			if seen[edge.address] {
				continue
			}
			seen[edge.address] = true
			result = append(result, edge)
			if edge.address.ClassID == TypeClassID || edge.address.ClassID == RelationClassID {
				if err := visit(edge.address, root); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, root := range stage.domainTargets {
		if err := visit(root, root); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func domainRestrictError(stage *CatalogStage, edges []domainDependency) error {
	message := "cannot drop desired object(s) because other objects depend on them"
	if len(stage.domainTargets) == 1 {
		message = "cannot drop " + dependencyObject(stage.base, stage.domainTargets[0], stage.dependencyPath) + " because other objects depend on it"
	}
	details := make([]string, len(edges))
	for i, edge := range edges {
		details[i] = edge.label + " depends on " + dependencyObject(stage.base, edge.target, stage.dependencyPath)
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: message, Detail: strings.Join(details, "\n"), Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
}

func (scope *StatementScope) stageDomainDependents(stage *CatalogStage, mode parser.DropMode) error {
	if len(stage.domainTargets) == 0 {
		return nil
	}
	edges, err := domainDependencyClosure(scope.op.Context(), stage)
	if err != nil {
		return err
	}
	if mode == parser.DropRestrict && len(edges) != 0 {
		return domainRestrictError(stage, edges)
	}
	targets := append([]ObjectAddress(nil), stage.domainTargets...)
	var relations []*relationDescriptor
	seen := make(map[ObjectAddress]bool)
	for _, edge := range edges {
		if edge.address.ClassID == TypeClassID || edge.column != "" {
			targets = append(targets, edge.address)
		}
		if edge.column != "" && !seen[edge.relation.address] {
			relations = append(relations, edge.relation)
			seen[edge.relation.address] = true
		}
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].address.ObjectID < relations[j].address.ObjectID })
	e := stage.owner
	for _, r := range relations {
		ref := ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: r.address}
		if err := scope.acquireSequence(ref, accessExclusive, true); err != nil {
			return err
		}
	}
	if err = stageViewDependents(stage, targets, parser.DropCascade); err != nil {
		return err
	}
	for _, old := range relations {
		ref := ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: old.address}
		next := old
		if replacement, ok := stage.descriptors[old.address].(*relationDescriptor); ok {
			next = replacement
		}
		for _, edge := range edges {
			if edge.column == "" || edge.relation.address != old.address {
				continue
			}
			next, err = scope.dropColumnCandidate(stage, next, ref, parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: edge.column}, Mode: parser.DropCascade})
			if err != nil {
				return err
			}
		}
		if old.revision == DefinitionRevision(^uint64(0)) {
			return fmt.Errorf("stuntgres: definition revision exhausted")
		}
		next.revision = old.revision + 1
		e.mu.Lock()
		storage := e.storage[old.address]
		if e.state.descriptors[old.address] != old || storage == nil {
			e.mu.Unlock()
			return CatalogConflict{}
		}
		rows, ends, generation := storage.versions, storage.ends, storage.generation
		e.mu.Unlock()
		if generation == ^uint64(0) {
			return fmt.Errorf("stuntgres: storage generation exhausted")
		}
		projected, er := projectVersions(scope.op.Context(), rows, old, next, nil)
		if er != nil {
			return er
		}
		replacement := &relationStorage{versions: projected, ends: ends, generation: generation + 1}
		if err = stage.alterRelation(scope.op.Context(), ref, next, storage, replacement, len(rows), generation, ends); err != nil {
			return err
		}
	}
	e.mu.Lock()
	for _, target := range targets {
		if target.ClassID == TypeClassID {
			if err = stage.removeObjectLocked(ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: target}, false); err != nil {
				break
			}
		}
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	stage.viewCascadeNames = nil
	for _, edge := range edges {
		stage.schemaCascadeNames = append(stage.schemaCascadeNames, "drop cascades to "+edge.label)
	}
	return nil
}

func (e *Engine) stageSchemaDomainsLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, schemas []*namespaceDescriptor, relations []*relationDescriptor, domains []*typeDescriptor, cascade bool) error {
	var contained []Descriptor
	for _, r := range relations {
		contained = append(contained, r)
	}
	for _, d := range domains {
		contained = append(contained, d)
		stage.domainTargets = append(stage.domainTargets, d.Address())
	}
	sort.Slice(contained, func(i, j int) bool { return contained[i].Address().ObjectID < contained[j].Address().ObjectID })
	sort.Slice(stage.domainTargets, func(i, j int) bool { return stage.domainTargets[i].ObjectID < stage.domainTargets[j].ObjectID })
	stage.dependencyPath, _ = e.referencePath(auth)
	for _, d := range contained {
		if err := stage.removeObjectLocked(ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: d.Address()}, false); err != nil {
			return err
		}
	}
	edges, err := domainDependencyClosure(op.Context(), stage)
	if err != nil {
		return err
	}
	var details []string
	for _, d := range contained {
		label := dependencyObject(stage.base, d.Address(), stage.dependencyPath)
		namespace := stage.base.state.descriptors[d.Namespace()].Name()
		details = append(details, label+" depends on schema "+dependencyIdentifier(namespace))
		stage.schemaCascadeNames = append(stage.schemaCascadeNames, "drop cascades to "+label)
		for _, edge := range edges {
			if edge.root == d.Address() {
				details = append(details, edge.label+" depends on "+dependencyObject(stage.base, edge.target, stage.dependencyPath))
			}
		}
	}
	if !cascade {
		message := "cannot drop desired object(s) because other objects depend on them"
		if len(schemas) == 1 {
			message = "cannot drop schema " + dependencyIdentifier(schemas[0].name) + " because other objects depend on it"
		}
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: message, Detail: strings.Join(details, "\n"), Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
	}
	for _, d := range schemas {
		if err = stage.removeObjectLocked(ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: d.address}, true); err != nil {
			return err
		}
	}
	return nil
}
