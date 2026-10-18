package engine

import (
	"context"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Invalid domain CHECKs are post-data events. Their providers do not constrain
// CREATE DOMAIN, which would invent cycles through a later table/derived type.
func domainInvalidDumpEvents(ctx context.Context, view CatalogRead, graph map[dumpEventKey][]dumpEventKey, domains map[ids.OID]*typeDescriptor) error {
	var data []dumpEventKey
	for event := range graph {
		if event.phase == dumpTableCompletion {
			data = append(data, event)
		}
	}
	for oid, domain := range domains {
		event := dumpEventKey{oid: oid, phase: dumpDomainChecks}
		for _, check := range domain.domainChecks {
			if !check.NotValid {
				continue
			}
			if _, exists := graph[event]; !exists {
				graph[event] = append([]dumpEventKey{{oid: oid}}, data...)
			}
			add := func(address ids.ObjectAddress) error {
				predecessor, err := dumpPredecessor(view, address, ids.ObjectAddress{})
				if err != nil {
					return err
				}
				if predecessor != 0 {
					addDumpDependency(graph, event, dumpEventKey{oid: predecessor})
				}
				return nil
			}
			if err := regclassReplayReferences(view, check.expression, add); err != nil {
				return err
			}
			for _, dependency := range check.dependencies {
				if err := add(dependency.ref.Address); err != nil {
					return err
				}
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

func domainInvalidDumpStatement(ctx context.Context, view CatalogRead, domain *typeDescriptor) (string, error) {
	qualified, err := catalogTypeDeclaration(view, domain.typ.Usage())
	if err != nil {
		return "", err
	}
	base := domain.typ.DomainBase()
	var text strings.Builder
	for _, check := range domain.domainChecks {
		if !check.NotValid {
			continue
		}
		expression, err := (scalarRenderer{domainValue: &base}).sql(ctx, view, check.expression)
		if err != nil {
			return "", err
		}
		text.WriteString("ALTER DOMAIN " + qualified + " ADD CONSTRAINT " + pgIdentifier(check.Name) + " CHECK (" + expression + ") NOT VALID;\n")
	}
	return text.String(), nil
}

func restoreDomainAlter(raw *parser.AlterDomain) error {
	if raw == nil || raw.Action != parser.DomainAddCheck || raw.Check == nil || !raw.Check.NotValid || raw.Check.NoInherit || raw.Check.Name.Text == "" || raw.Constraint != raw.Check.Name || raw.IfExists || raw.Mode != 0 || len(raw.Name.Parts) != 2 || raw.Name.Parts[0].Text == "pg_temp" || strings.HasPrefix(raw.Name.Parts[0].Text, "pg_temp_") {
		return sqlError("0A000", "restore accepts only qualified named NOT VALID domain CHECK additions")
	}
	return restoreScalar(raw.Check.Expression, true)
}

const restoreDomainOwnerBoundary = "restore accepts only immediately paired fresh domain ownership"

type restoredDomainOwner struct {
	ref            ids.ObjectRef
	domain         *typeDescriptor
	namespace      *namespaceDescriptor
	owner, creator authorization.PrincipalRef
}

// Only the constructor's actual fresh ref, immediately after the captured owner
// and bootstrap directives, can produce this pair. The Restore loop expires it
// on every subsequent statement; it is not general ALTER OWNER eligibility.
func (e *Engine) captureRestoredDomainOwner(op *Operation, created ids.ObjectRef, owner authorization.PrincipalRef) (restoredDomainOwner, error) {
	reject := func() (restoredDomainOwner, error) {
		return restoredDomainOwner{}, sqlError("0A000", restoreDomainOwnerBoundary)
	}
	if op == nil {
		return reject()
	}
	if err := op.Context().Err(); err != nil {
		return restoredDomainOwner{}, err
	}
	auth, err := op.Authorization()
	if err != nil {
		return restoredDomainOwner{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !partitionConstraintReplayImport(e, op, auth) || created.Domain() != e.domain || created.Epoch != auth.catalog.Epoch() || created.Address.ClassID != TypeClassID || created.Address.SubID != 0 {
		return reject()
	}
	if _, captured := e.importTargets[owner]; !captured {
		return reject()
	}
	domain, ok := auth.catalog.state.descriptors[created.Address].(*typeDescriptor)
	if !ok || !domain.typ.IsDomain() {
		return reject()
	}
	namespace, ok := auth.catalog.state.descriptors[domain.namespace].(*namespaceDescriptor)
	if !ok || namespace.temporary != (SessionToken{}) {
		return reject()
	}
	actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
	if err != nil {
		return restoredDomainOwner{}, err
	}
	if !actor.Superuser || domain.owner != actor.Ref || owner == actor.Ref {
		return reject()
	}
	if _, err := auth.roles.Descriptor(owner); err != nil {
		return restoredDomainOwner{}, err
	}
	return restoredDomainOwner{ref: created, domain: domain, namespace: namespace, owner: owner, creator: actor.Ref}, nil
}

func (e *Engine) restoreDomainOwner(op *Operation, raw *parser.AlterOwner, pair restoredDomainOwner) error {
	reject := func() error { return sqlError("0A000", restoreDomainOwnerBoundary) }
	if raw == nil || raw.Kind != "domain" || raw.IfExists || len(raw.Name.Parts) != 2 || raw.Target.Identity != "" || pair.ref == (ids.ObjectRef{}) {
		return reject()
	}
	current, err := e.captureRestoredDomainOwner(op, pair.ref, pair.owner)
	if err != nil {
		return err
	}
	if current != pair || raw.Name.Parts[0].Text != pair.namespace.Name() || raw.Name.Parts[1].Text != pair.domain.Name() {
		return reject()
	}
	auth, err := op.Authorization()
	if err != nil {
		return err
	}
	owner, err := auth.roles.Descriptor(pair.owner)
	if err != nil {
		return err
	}
	if raw.Target.Name != owner.Name {
		return reject()
	}
	return nil
}
