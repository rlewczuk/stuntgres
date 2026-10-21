package engine

import (
	"context"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

// Invalid domain CHECKs are post-data events. Their providers do not constrain
// CREATE DOMAIN, which would invent cycles through a later table/derived type.
func domainInvalidDumpEvents(ctx context.Context, view catalog.Read, graph dump.Graph, domains map[ids.OID]*catalog.Type) error {
	var data []dump.Event
	for event := range graph {
		if event.Phase == dump.TableCompletion {
			data = append(data, event)
		}
	}
	for oid, domain := range domains {
		event := dump.Event{OID: oid, Phase: dump.DomainChecks}
		for _, check := range domain.DomainChecks {
			if !check.NotValid {
				continue
			}
			if _, exists := graph[event]; !exists {
				graph[event] = append([]dump.Event{{OID: oid}}, data...)
			}
			add := func(address ids.ObjectAddress) error {
				predecessor, err := dumpPredecessor(view, address, ids.ObjectAddress{})
				if err != nil {
					return err
				}
				if predecessor != 0 {
					graph.Add(event, dump.Event{OID: predecessor})
				}
				return nil
			}
			if err := ClassReplayReferences(view, check.Expression, add); err != nil {
				return err
			}
			for _, dependency := range check.Dependencies {
				if err := add(dependency.Ref.Address); err != nil {
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

func domainInvalidDumpStatement(ctx context.Context, view catalog.Read, domain *catalog.Type) (string, error) {
	qualified, err := TypeDeclaration(view, domain.SQLType.Usage())
	if err != nil {
		return "", err
	}
	base := domain.SQLType.DomainBase()
	var text strings.Builder
	for _, check := range domain.DomainChecks {
		if !check.NotValid {
			continue
		}
		expression, err := (scalarRenderer{domainValue: &base}).sql(ctx, view, check.Expression)
		if err != nil {
			return "", err
		}
		text.WriteString("ALTER DOMAIN " + qualified + " ADD CONSTRAINT " + catalog.Identifier(check.Name) + " CHECK (" + expression + ") NOT VALID;\n")
	}
	return text.String(), nil
}

func restoreDomainAlter(raw *parser.AlterDomain) error {
	if raw == nil || raw.Action != parser.DomainAddCheck || raw.Check == nil || !raw.Check.NotValid || raw.Check.NoInherit || raw.Check.Name.Text == "" || raw.Constraint != raw.Check.Name || raw.IfExists || raw.Mode != 0 || len(raw.Name.Parts) != 2 || raw.Name.Parts[0].Text == "pg_temp" || strings.HasPrefix(raw.Name.Parts[0].Text, "pg_temp_") {
		return sqlError("0A000", "restore accepts only qualified named NOT VALID domain CHECK additions")
	}
	return dump.RestoreScalar(raw.Check.Expression, true)
}

// Only the constructor's actual fresh ref, immediately after the captured owner
// and bootstrap directives, can produce this pair. The Restore loop expires it
// on every subsequent statement; it is not general ALTER OWNER eligibility.
func (e *Engine) captureRestoredDomainOwner(op *session.Operation, created ids.ObjectRef, owner authorization.PrincipalRef) (typedef.RestoredDomainOwner, error) {
	reject := func() (typedef.RestoredDomainOwner, error) {
		return typedef.RestoredDomainOwner{}, sqlError("0A000", typedef.RestoreDomainOwnerBoundary)
	}
	if op == nil {
		return reject()
	}
	if err := op.Context().Err(); err != nil {
		return typedef.RestoredDomainOwner{}, err
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return typedef.RestoredDomainOwner{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !partitionConstraintReplayImport(e, op, auth) || created.Domain() != e.catalog.Domain || created.Epoch != auth.Catalog.Epoch() || created.Address.ClassID != ids.TypeClassID || created.Address.SubID != 0 {
		return reject()
	}
	if _, captured := e.importTargets[owner]; !captured {
		return reject()
	}
	domain, ok := auth.Catalog.Type(created.Address)
	if !ok || !domain.SQLType.IsDomain() {
		return reject()
	}
	namespace, ok := auth.Catalog.Namespace(domain.ObjectNamespace)
	if !ok || namespace.Temporary != (ids.SessionToken{}) {
		return reject()
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return typedef.RestoredDomainOwner{}, err
	}
	if !actor.Superuser || domain.Owner != actor.Ref || owner == actor.Ref {
		return reject()
	}
	if _, err := auth.Roles.Descriptor(owner); err != nil {
		return typedef.RestoredDomainOwner{}, err
	}
	return typedef.RestoredDomainOwner{Ref: created, Domain: domain, Namespace: namespace, Owner: owner, Creator: actor.Ref}, nil
}

func (e *Engine) restoreDomainOwner(op *session.Operation, raw *parser.AlterOwner, pair typedef.RestoredDomainOwner) error {
	reject := func() error { return sqlError("0A000", typedef.RestoreDomainOwnerBoundary) }
	if raw == nil || raw.Kind != "domain" || raw.IfExists || len(raw.Name.Parts) != 2 || raw.Target.Identity != "" || pair.Ref == (ids.ObjectRef{}) {
		return reject()
	}
	current, err := e.captureRestoredDomainOwner(op, pair.Ref, pair.Owner)
	if err != nil {
		return err
	}
	if current != pair || raw.Name.Parts[0].Text != pair.Namespace.Name() || raw.Name.Parts[1].Text != pair.Domain.Name() {
		return reject()
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return err
	}
	owner, err := auth.Roles.Descriptor(pair.Owner)
	if err != nil {
		return err
	}
	if raw.Target.Name != owner.Name {
		return reject()
	}
	return nil
}
