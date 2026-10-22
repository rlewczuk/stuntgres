package stored

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// ValidateDomainDefault checks an independent creation-copied default snapshot,
// its presentation, assignment destination and authentic provider manifest.
func ValidateDomainDefault(view catalog.Read, d *catalog.Type) error {
	if !d.SQLType.IsDomain() {
		if d.DomainDefault != nil || d.DomainDefaultOwn || d.DomainDefaultText != "" {
			return fmt.Errorf("stuntgres: non-domain default recipe")
		}
		return nil
	}
	if (d.DomainDefault == nil) != (d.DomainDefaultText == "") {
		return fmt.Errorf("stuntgres: inconsistent domain default presentation")
	}
	if d.DomainDefault != nil && (d.DomainDefault.Expression == nil || d.DomainDefault.OID != 0) {
		return fmt.Errorf("stuntgres: invalid domain default destination")
	}
	if d.DomainDefaultOwn && d.DomainDefault == nil {
		return fmt.Errorf("stuntgres: missing own domain default")
	}
	if d.DomainDefault == nil {
		return nil
	}
	definition := d.DomainDefault
	if d.DomainDefaultOwn && definition.Expression.ResultUsage() != d.SQLType.DomainBase() {
		return fmt.Errorf("stuntgres: invalid own domain default destination")
	}
	if _, err := sqltype.PlanCoercion(definition.Expression.ResultUsage(), d.SQLType.DomainBase(), sqltype.Assignment); err != nil {
		return fmt.Errorf("stuntgres: invalid domain default destination: %w", err)
	}

	var refs []ids.ObjectRef
	snapshot, aliases := false, false
	_, err := (ScalarRenderer{Validation: true, CapturedManifest: definition.Dependencies, Dependencies: &refs, CommentSnapshot: &snapshot, CatalogAliasReferences: &aliases}).SQL(context.Background(), view, definition.Expression)
	if err != nil {
		return err
	}
	if !slices.Equal(refs, definition.Dependencies) || snapshot != definition.CatalogSnapshot || aliases != definition.CatalogAliasReferences {
		return fmt.Errorf("stuntgres: inconsistent domain default recipe")
	}
	for _, ref := range refs {
		address := ref.Address
		address.SubID = 0
		if _, ok := view.Descriptor(address); !ok || ref.Domain() != view.Owner().Domain || ref.Epoch != view.Epoch() {
			return fmt.Errorf("stuntgres: foreign domain default provider")
		}
	}
	return nil
}
