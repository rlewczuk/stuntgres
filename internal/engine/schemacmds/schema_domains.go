package schemacmds

import (
	"context"
	"slices"
	"sort"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func domainRestrictError(stage *core.CatalogStage, targets []ids.ObjectAddress, edges []table.DomainDependency, originalRootCount int, subject ids.ObjectAddress) error {
	return view.RestrictError(core.StageDropProvider(context.Background(), stage, nil, nil, nil), targets, edges, originalRootCount, subject)
}

func stageDomainDependents(scope *core.StatementScope, stage *core.CatalogStage, mode parser.DropMode) error {
	start := len(stage.DropCascadeNames)
	if err := scope.StageExpressionDependents(stage, stage.DomainTargets, mode, nil); err != nil {
		return err
	}
	stage.SchemaCascadeNames = append(stage.SchemaCascadeNames, stage.DropCascadeNames[start:]...)
	stage.DropCascadeNames = stage.DropCascadeNames[:start]
	return nil
}

func stageSchemaDomainsLocked(e *core.Engine, op *session.Operation, auth core.AuthorizationView, stage *core.CatalogStage, schemas []*catalog.Namespace, relations []*catalog.Relation, domains []catalog.Descriptor, cascade bool) error {
	var contained []catalog.Descriptor
	for _, r := range relations {
		contained = append(contained, r)
	}
	for _, d := range domains {
		contained = append(contained, d)
		stage.DomainTargets = append(stage.DomainTargets, d.Address())
		if extension, ok := d.(*catalog.Extension); ok {
			for _, member := range extension.Members {
				stage.DomainTargets = append(stage.DomainTargets, member)
				if err := stage.RemoveObjectLocked(e.Catalog.Domain.Ref(stage.Base.Epoch(), member), false); err != nil {
					return err
				}
			}
		}
	}
	sort.Slice(contained, func(i, j int) bool { return contained[i].Address().ObjectID < contained[j].Address().ObjectID })
	sort.Slice(stage.DomainTargets, func(i, j int) bool { return stage.DomainTargets[i].ObjectID < stage.DomainTargets[j].ObjectID })
	stage.DependencyPath, _ = core.ReferencePath(auth)
	for _, d := range contained {
		stage.MarkDrop(d.Address(), catalog.DropProvenance{Normal: true})
		if err := stage.RemoveObjectLocked(e.Catalog.Domain.Ref(stage.Base.Epoch(), d.Address()), false); err != nil {
			return err
		}
	}
	edges, err := core.DomainDependencyClosure(op.Context(), stage, stage.DomainTargets)
	if err != nil {
		return err
	}
	var details []string
	for _, d := range contained {
		label := stored.DependencyObject(stage.Base, d.Address(), stage.DependencyPath)
		namespaceAddress := d.Namespace()
		if typ, ok := d.(*catalog.Type); ok && typ.SQLType.IsRange() {
			for _, schema := range schemas {
				if schema.Address() == namespaceAddress {
					break
				}
				companion := stage.Base.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.SQLType.MultirangeType().ID())})
				if companion != nil && companion.Namespace() == schema.Address() {
					namespaceAddress = schema.Address()
					break
				}
			}
		}
		if extension, ok := d.(*catalog.Extension); ok {
			namespaceAddress = extension.TargetNamespace
		}
		namespace := stage.Base.Get(namespaceAddress).Name()
		details = append(details, label+" depends on schema "+catalog.DependencyIdentifier(namespace))
		stage.SchemaCascadeNames = append(stage.SchemaCascadeNames, "drop cascades to "+label)
		for _, edge := range edges {
			root := edge.Root == d.Address()
			if extension, ok := d.(*catalog.Extension); ok {
				root = root || slices.Contains(extension.Members[:], edge.Root)
			}
			if root && !edge.Automatic {
				details = append(details, edge.Label+" depends on "+stored.DependencyObject(stage.Base, edge.Target, stage.DependencyPath))
			}
		}
	}
	if !cascade {
		message := "cannot drop desired object(s) because other objects depend on them"
		if len(schemas) == 1 {
			message = "cannot drop schema " + catalog.DependencyIdentifier(schemas[0].Name()) + " because other objects depend on it"
		}
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: message, Detail: strings.Join(details, "\n"), Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
	}
	for _, d := range schemas {
		stage.MarkDrop(d.Address(), catalog.DropProvenance{Original: true})
		if err = stage.RemoveObjectLocked(e.Catalog.Domain.Ref(stage.Base.Epoch(), d.Address()), true); err != nil {
			return err
		}
	}
	return nil
}
