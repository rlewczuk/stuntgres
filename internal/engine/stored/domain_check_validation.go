package stored

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// ValidateDomainCheckDefinitions checks a domain's CHECK and NOT NULL
// ancestry, its CHECK records and their providers.
func ValidateDomainCheckDefinitions(view catalog.Read, domain *catalog.Type) error {
	base := domain.SQLType.DomainBase()
	if domain.SQLType.HasDomainChecks() != (len(domain.DomainChecks) != 0 || base.Type.HasDomainChecks()) {
		return fmt.Errorf("stuntgres: inconsistent domain CHECK ancestry")
	}
	if domain.SQLType.DomainNotNull() != (domain.SQLType.OwnDomainNotNull() || base.Type.DomainNotNull()) {
		return fmt.Errorf("stuntgres: inconsistent domain NOT NULL ancestry")
	}
	previous := ""
	for _, check := range domain.DomainChecks {
		if check.OID == 0 || check.Name == "" || len(check.Name) > 63 || !utf8.ValidString(check.Name) || strings.IndexByte(check.Name, 0) >= 0 || previous != "" && previous >= check.Name || check.Ordinal < 0 || check.Expression == nil || check.Expression.ResultUsage().Type == nil || check.Expression.ResultUsage().Type.ID() != sqltype.Bool {
			return fmt.Errorf("stuntgres: invalid domain CHECK definition")
		}
		previous = check.Name
		if _, err := (ScalarRenderer{DomainValue: &base}).SQL(context.Background(), view, check.Expression); err != nil {
			return err
		}
		for _, dependency := range check.Dependencies {
			address := dependency.Ref.Address
			address.SubID = 0
			provider, exists := view.Descriptor(address)
			if !exists || dependency.Ref.Domain() != view.Owner().Domain || dependency.Ref.Epoch != view.Epoch() || provider.Kind() != ids.TypeObject && provider.Kind() != ids.CollationObject && provider.Kind() != ids.ProcedureObject && provider.Kind() != ids.RelationObject {
				return fmt.Errorf("stuntgres: invalid domain CHECK provider")
			}
		}
	}
	return nil
}
