package access

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// ACLItemImporter reowns typed aclitem values of a replacement from the
// source store's ACL authority to this store's. catalogs is the destination
// catalog store this role store backs.
func (s *RoleStore) ACLItemImporter(sourceStore *RoleStore, catalogs *catalog.Store, source, target authorization.Read, expected map[authorization.PrincipalRef]authorization.PrincipalRef) func(sqltype.Value) (sqltype.Value, error) {
	var principal func(authorization.PrincipalRef) (authorization.PrincipalRef, error)
	// Structural traversal belongs to typedef.ReownDomainDatum, combining descriptor and
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
		if authority == &s.ACLAuthority {
			return value, nil
		}
		if sourceStore == nil || authority != &sourceStore.ACLAuthority || !source.BelongsTo(&sourceStore.Domain) {
			return sqltype.Value{}, fmt.Errorf("stuntgres: foreign ACL import identity")
		}
		if principal == nil {
			principal = s.PrincipalMapper(catalogs, source, target, expected)
		}
		remap := func(id uint32) (uint32, error) {
			if id == 0 {
				return 0, nil
			}
			ref, err := principal(sourceStore.Domain.Principal(uint64(id)))
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
		physical, err = sqltype.ACLItemValue(physical.Type(), &s.ACLAuthority, grantee, grantor, privileges, options)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.Type().IsDomain() {
			return value.Type().Wrap(physical)
		}
		return physical, nil
	}
}
