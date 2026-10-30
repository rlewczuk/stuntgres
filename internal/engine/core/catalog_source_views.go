package core

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
)

func (r EngineCatalogRuntime) ViewCapabilities(ctx context.Context, read catalog.Read, relation *catalog.Relation) (pgcatalog.ViewCapability, error) {
	events, err := view.Capabilities(ctx, read, relation, nil, r.catalogViewLeafEvents)
	if err != nil {
		return pgcatalog.ViewCapability{}, err
	}
	var triggers uint32
	for _, trigger := range stored.TriggersOf(relation) {
		p := trigger.Parts()
		if p.Timing != stored.TriggerInstead || p.Level != stored.TriggerRow {
			continue
		}
		if p.Events&stored.TriggerUpdate != 0 {
			triggers |= 4
		}
		if p.Events&stored.TriggerInsert != 0 {
			triggers |= 8
		}
		if p.Events&stored.TriggerDelete != 0 {
			triggers |= 16
		}
	}
	return pgcatalog.ViewCapability{Events: events, Triggers: triggers}, nil
}

// No provider method is called: interface support is immutable handler metadata.
// The caller owns the captured catalog; no foreign transaction or scan is begun.
func (r EngineCatalogRuntime) catalogViewLeafEvents(ctx context.Context, read catalog.Read, relation *catalog.Relation, subset []int32) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if relation.RelationKind == catalog.CatalogRelation {
		switch pgcatalog.ClassKind(read, relation) {
		case "r", "p":
			return 28, nil
		case "v":
			return pgcatalog.BuiltinViewEvents(relation.Address().ObjectID, subset), nil
		default:
			return 0, nil
		}
	}
	if relation.RelationKind != catalog.ForeignRelation {
		return 0, nil
	}
	handler, err := fdw.RelationHandler(read, relation, r.Auth.RestrictedRelations())
	if err != nil {
		return 0, err
	}
	var events uint32
	if _, ok := handler.Wrapper.(catalog.ForeignInserter); ok {
		events |= 8
	}
	if _, ok := handler.Wrapper.(catalog.ForeignUpdater); ok {
		events |= 4
	}
	if _, ok := handler.Wrapper.(catalog.ForeignDeleter); ok {
		events |= 16
	}
	return events, nil
}

func (r EngineCatalogRuntime) builtinViewDefinition(ctx context.Context, read catalog.Read, relation *catalog.Relation) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if read.Get(relation.Address()) != relation {
		return "", ids.ErrCatalogReplaced
	}
	var spellingErr error
	definition := pgcatalog.BuiltinViewDefinition(relation.Address().ObjectID, r.presentation.Pretty, func(name pgcatalog.BuiltinViewName) string {
		if spellingErr != nil {
			return ""
		}
		spelling, err := r.builtinViewName(read, name)
		spellingErr = err
		return spelling
	})
	if spellingErr != nil {
		return "", spellingErr
	}
	if definition == "" && !r.presentation.Pretty {
		return "", fmt.Errorf("stuntgres: missing builtin view definition")
	}
	return strings.TrimSuffix(definition, ";"), nil
}

// This bounded display lookup applies only to explicitly named references in
// the builtin-owned fragments. It does not install upstream-only descriptors.
func (r EngineCatalogRuntime) builtinViewName(read catalog.Read, name pgcatalog.BuiltinViewName) (string, error) {
	path := r.presentation.Path
	identifier := (stored.Deparser{Output: r.presentation.Output}).Identifier
	if name.IdentifierOnly {
		return identifier(name.Name), nil
	}
	if name.Class == ids.ProcedureClassID && name.RoutineOID != 0 {
		if current, ok := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: name.RoutineOID}); ok {
			namespace, ok := read.Namespace(current.Namespace())
			if !ok {
				return "", ids.ErrCatalogReplaced
			}
			bare := identifier(current.Name())
			selected, count := regobj.SelectExactRoutine(read, analyze.NamespaceSelection{Path: path}, current.Name(), current.Signature)
			if count == 1 && selected.ID == name.RoutineOID {
				return bare, nil
			}
			return identifier(namespace.Name()) + "." + bare, nil
		}
	}
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: name.Namespace}
	bare := identifier(name.Name)
	schema := "pg_catalog"
	if name.Namespace == 13229 {
		schema = "information_schema"
	}
	qualified := identifier(schema) + "." + bare
	spelling := qualified
	key := ids.NameKey{ClassID: name.Class, Namespace: namespace, Name: name.Name, Signature: string(name.Signature)}
	if name.Class == ids.ProcedureClassID {
		selected, count := regobj.SelectExactRoutine(read, analyze.NamespaceSelection{Path: path}, name.Name, name.Signature)
		if count == 1 && selected.Namespace == name.Namespace {
			spelling = bare
		} else if count == 0 {
			// The released target can be absent locally (information_schema helpers).
			// Earlier exact signatures still mask it; other overloads do not.
			for _, candidate := range path {
				if candidate == namespace {
					spelling = bare
					break
				}
				if _, count := regobj.SelectExactRoutine(read, analyze.NamespaceSelection{Qualified: candidate}, name.Name, name.Signature); count != 0 {
					break
				}
			}
		}
	} else if target, ok := read.LookupName(key); ok && !r.presentation.Output.QuoteAllIdentifiers {
		if name.Class == ids.RelationClassID {
			spelling = regobj.ClassSpelling(read, path, target.ObjectID)
		} else {
			spelling = regobj.TypeSpelling(read, path, target.ObjectID)
		}
	} else {
		for _, candidate := range path {
			if candidate == namespace {
				spelling = bare
				break
			}
			key.Namespace = candidate
			if _, exists := read.LookupName(key); exists {
				break
			}
		}
	}
	if name.RegclassLiteral {
		return "'" + strings.ReplaceAll(spelling, "'", "''") + "'", nil
	}
	return spelling, nil
}
