package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Apply only to Restore's isolated catalog. Named target identities were
// captured before reading; final replacement fences their remapping again.
func (e *Engine) importExtensionOwners(ctx context.Context, op *Operation, records []OwnerRecord) error {
	if len(records) == 0 {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.importTargets == nil {
		return fmt.Errorf("stuntgres: invalid extension owner import authority")
	}
	seen := map[ids.ObjectAddress]bool{}
	candidate := catalog.CloneState(e.catalog.state)
	roles := e.roles.read(e.catalog.owner(), e.catalog.read(candidate), e.roles.published)
	ownersChanged := false
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		a := candidate.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: record.Name})
		ext, ok := candidate.Extension(a)
		if !ok || seen[a] || ext.Version != record.Version {
			return fmt.Errorf("stuntgres: invalid extension owner import target")
		}
		seen[a] = true
		ns, ok := candidate.Namespace(ext.TargetNamespace)
		if !ok || ns.Name() != record.Schema {
			return fmt.Errorf("stuntgres: mismatching extension owner namespace")
		}
		owner, ok := roles.Lookup(record.Owner)
		if !ok || owner.Ref != ext.Owner {
			return fmt.Errorf("stuntgres: mismatching extension installation owner")
		}
		for _, member := range record.Members {
			owner, ok := roles.Lookup(member.Owner)
			if !ok {
				return fmt.Errorf("stuntgres: extension member owner %q does not exist", member.Owner)
			}
			class := ids.ProcedureClassID
			if member.Kind == "dictionary" {
				class = ids.TSDictionaryClassID
			}
			if member.Kind == "type" {
				class = ids.TypeClassID
			}
			signature := member.Signature
			if member.Kind == "procedure" && (record.Name == "intarray" && member.Name == "bqarr_out" || record.Name == "cube" && member.Name == "cube_out") {
				base, ok := candidate.Type(ext.Members[0])
				if !ok || record.Name == "intarray" && !base.SQLType.IsQueryInt() || record.Name == "cube" && !base.SQLType.IsCube() {
					return fmt.Errorf("stuntgres: missing installed owner type")
				}
				signature = string(QueryIntSignature(base.SQLType.ID()))
			}
			a := candidate.NameAddress(ids.NameKey{ClassID: class, Namespace: ext.TargetNamespace, Name: member.Name, Signature: signature})
			d := candidate.Get(a)
			if d == nil || Membership(d) != ext.Address() {
				return fmt.Errorf("stuntgres: invalid extension ownership member")
			}
			switch d := d.(type) {
			case *catalog.Type:
				if d.Owner != owner.Ref {
					n := *d
					n.Owner = owner.Ref
					candidate.SetDescriptor(a, &n)
					arrayAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.Array)}
					array, ok := candidate.Type(arrayAddress)
					if !ok || !array.SQLType.IsArray() || array.SQLType.Element() != d.SQLType {
						return fmt.Errorf("stuntgres: invalid owned intarray array")
					}
					mapped := *array
					mapped.Owner = owner.Ref
					candidate.SetDescriptor(arrayAddress, &mapped)
					ownersChanged = true
				}
			case *catalog.Procedure:
				if d.Owner != owner.Ref {
					n := *d
					n.Owner = owner.Ref
					candidate.SetDescriptor(a, &n)
					ownersChanged = true
				}
			case *catalog.TSDictionary:
				if d.Owner != owner.Ref {
					n := *d
					n.Owner = owner.Ref
					candidate.SetDescriptor(a, &n)
					ownersChanged = true
				}
			default:
				return fmt.Errorf("stuntgres: ownerless extension metadata member")
			}
		}
	}
	if !ownersChanged {
		return nil
	}
	// Ownership changes replace provider generations even when all SQL types are
	// unchanged. Refresh stored CHECK/view/policy/default captures through the
	// ordinary catalog expression remapper, not its type-only rename fast path.
	if err := e.reownCatalogExpressions(candidate, nil, false); err != nil {
		return err
	}
	e.catalog.state = candidate
	return nil
}
