package core

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
)

// TablespaceRepairs is one native Restore's bounded explicit administrative
// delta, not portable snapshot state or another committed authority store.
// Values remain in the isolated stage; only exact touched fields are imported.
type TablespaceRepairs [2]struct {
	Original   *catalog.Tablespace
	Owner, ACL bool
}

// RestoreTablespaceRepairs returns this isolated stage's pre-reader predecessors.
// The enclosing RestoreStage capture owns their role and descriptor provenance.
func (e *Engine) RestoreTablespaceRepairs() TablespaceRepairs {
	return e.importTablespaceRepairs
}

func (e *Engine) captureTablespaceRepairsLocked() TablespaceRepairs {
	var repairs TablespaceRepairs
	for i, builtin := range catalog.BuiltinTablespaces {
		repairs[i].Original = e.Catalog.State.Get(ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: builtin.OID}).(*catalog.Tablespace)
	}
	return repairs
}

func (r *TablespaceRepairs) Touch(address ids.ObjectAddress, owner bool) error {
	if r != nil && address.ClassID == ids.TablespaceClassID && address.SubID == 0 {
		for i, builtin := range catalog.BuiltinTablespaces {
			if address.ObjectID == builtin.OID && r[i].Original != nil && r[i].Original.Address() == address {
				if owner {
					r[i].Owner = true
				} else {
					r[i].ACL = true
				}
				return nil
			}
		}
	}
	return fmt.Errorf("stuntgres: invalid explicit tablespace repair authority")
}

type tablespaceRepairInput struct {
	repairs TablespaceRepairs
	values  [2]*catalog.Tablespace
}

func captureTablespaceRepairs(source *Engine, view catalog.Read, op *session.Operation) (input tablespaceRepairInput, err error) {
	if op == nil {
		return input, nil
	}
	replay := OperationReplayOrZero(op)
	if replay.TablespaceRepairs == nil {
		return input, nil
	}
	input.repairs = *replay.TablespaceRepairs
	if !input.repairs[0].Owner && !input.repairs[0].ACL && !input.repairs[1].Owner && !input.repairs[1].ACL {
		return input, nil
	}
	if !replay.StagingEffects || op.Session == nil || session.PrivateSessionOf(op.Session.SessionToken) != op.Session || EngineOf(op.Session) != source {
		return tablespaceRepairInput{}, fmt.Errorf("stuntgres: invalid explicit tablespace repair source")
	}
	if err := session.ValidateOperation(source.Catalog.Domain, op); err != nil {
		return tablespaceRepairInput{}, err
	}
	for i, repair := range input.repairs {
		if !repair.Owner && !repair.ACL {
			continue
		}
		if repair.Original == nil {
			return tablespaceRepairInput{}, fmt.Errorf("stuntgres: missing explicit tablespace repair predecessor")
		}
		address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: catalog.BuiltinTablespaces[i].OID}
		if repair.Original.Address() != address {
			return tablespaceRepairInput{}, fmt.Errorf("stuntgres: foreign tablespace repair predecessor")
		}
		descriptor, ok := view.Get(address).(*catalog.Tablespace)
		if !ok {
			return tablespaceRepairInput{}, fmt.Errorf("stuntgres: missing explicit tablespace repair value")
		}
		input.values[i] = descriptor
	}
	return input, nil
}

func (e *Engine) applyTablespaceRepairsLocked(next *catalog.State, input tablespaceRepairInput, source, target authorization.Read, expected map[authorization.PrincipalRef]authorization.PrincipalRef) error {
	if input.values == [2]*catalog.Tablespace{} {
		return nil
	}
	policy := e.RoleStore.PrincipalRemap(&e.Catalog, source, target, expected, routine.OperatorSeeded)
	revision := catalog.RevisionFrontier(e.Catalog.NextRevision(), next.Descriptors())
	for i, value := range input.values {
		if value == nil {
			continue
		}
		repair := input.repairs[i]
		current := e.Catalog.State.Get(value.Address())
		if current != repair.Original {
			return ids.ErrCatalogReplaced
		}
		if revision == ids.DefinitionRevision(^uint64(0)) {
			return fmt.Errorf("stuntgres: catalog definition revision exhausted")
		}
		replacement := *next.Get(value.Address()).(*catalog.Tablespace)
		var err error
		if repair.Owner {
			replacement.Owner, err = policy.Principal(value.Owner)
			if err != nil {
				return err
			}
		}
		if repair.ACL {
			replacement.ACL, err = catalog.RemapACL(value.ACL, policy.Principal)
			if err != nil {
				return err
			}
		}
		replacement.ObjectRevision = revision
		revision++
		next.SetDescriptor(replacement.Address(), &replacement)
	}
	return nil
}

// takeReplacementTablespaces excludes ignored source OWNER/ACL principals from
// ordinary remapping, while retaining the candidate's supported alias image.
func takeReplacementTablespaces(next *catalog.State) ([2]*catalog.Tablespace, error) {
	var image [2]*catalog.Tablespace
	for i, builtin := range catalog.BuiltinTablespaces {
		address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: builtin.OID}
		descriptor, ok := next.Get(address).(*catalog.Tablespace)
		if !ok {
			return image, fmt.Errorf("stuntgres: missing replacement builtin tablespace")
		}
		image[i] = descriptor
	}
	for _, descriptor := range image {
		next.UnbindName(catalog.DescriptorNameKey(descriptor))
		next.RemoveDescriptor(descriptor.Address())
	}
	return image, nil
}

// retainTablespaceAuthorityLocked composes candidate names with latest committed
// destination OWNER/ACL. It reserves no frontier and installs nothing publicly.
func (e *Engine) retainTablespaceAuthorityLocked(next *catalog.State, image [2]*catalog.Tablespace) error {
	revision := catalog.RevisionFrontier(e.Catalog.NextRevision(), next.Descriptors())
	for _, candidate := range image {
		current := e.Catalog.State.Get(candidate.Address()).(*catalog.Tablespace)
		descriptor := current
		if candidate.Name() != current.Name() {
			if revision == ids.DefinitionRevision(^uint64(0)) {
				return fmt.Errorf("stuntgres: catalog definition revision exhausted")
			}
			replacement := *current
			replacement.ObjectName, replacement.ObjectRevision = candidate.Name(), revision
			revision++
			descriptor = &replacement
		}
		next.SetDescriptor(descriptor.Address(), descriptor)
		next.BindName(catalog.DescriptorNameKey(descriptor), descriptor.Address())
	}
	return nil
}
