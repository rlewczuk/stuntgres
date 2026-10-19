package engine

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqltype"
)

func (e *Engine) aclItemImporter(sourceEngine *Engine, source, target authorization.Read, expected map[authorization.PrincipalRef]authorization.PrincipalRef) func(sqltype.Value) (sqltype.Value, error) {
	var principal func(authorization.PrincipalRef) (authorization.PrincipalRef, error)
	// Structural traversal belongs to reownDomainDatum, combining descriptor and
	// principal changes in one copy-on-change array/composite pass.
	return func(value sqltype.Value) (sqltype.Value, error) {
		if value.IsNull() || value.Type().BaseType().ID() != sqltype.ACLItem {
			return value, nil
		}
		authority, grantee, grantor, privileges, options, err := value.ACLItem()
		if err != nil {
			return sqltype.Value{}, err
		}
		// Retained destination-owned temporary objects must not be reimported.
		if authority == &e.roles.aclAuthority {
			return value, nil
		}
		if sourceEngine == nil || authority != &sourceEngine.roles.aclAuthority || !source.BelongsTo(&sourceEngine.roles.domain) {
			return sqltype.Value{}, fmt.Errorf("stuntgres: foreign ACL import identity")
		}
		if principal == nil {
			principal = e.replacementPrincipalMapper(source, target, expected)
		}
		remap := func(id uint32) (uint32, error) {
			if id == 0 {
				return 0, nil
			}
			ref, err := principal(sourceEngine.roles.domain.Principal(uint64(id)))
			if err != nil {
				return 0, err
			}
			if ref.ID() > math.MaxUint32 {
				return 0, fmt.Errorf("stuntgres: ACL import principal out of range")
			}
			return uint32(ref.ID()), nil
		}
		grantee, err = remap(grantee)
		if err != nil {
			return sqltype.Value{}, err
		}
		grantor, err = remap(grantor)
		if err != nil {
			return sqltype.Value{}, err
		}
		physical := value
		if value.Type().IsDomain() {
			physical, err = value.BaseValue()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		physical, err = sqltype.ACLItemValue(physical.Type(), &e.roles.aclAuthority, grantee, grantor, privileges, options)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.Type().IsDomain() {
			return value.Type().Wrap(physical)
		}
		return physical, nil
	}
}
