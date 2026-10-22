package table

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// RelationValidator checks a relation for a package above table.
type RelationValidator func(catalog.Read, *catalog.Relation) error

// RelationValidators are the relation checks of the packages above table.
// Validate calls them at fixed points, in this order: Policies after the
// column checks, Partition after the CHECK-kind check, and Definition as the
// view and materialized-view tail. The root fills all three.
type RelationValidators struct {
	Policies   RelationValidator
	Partition  RelationValidator
	Definition RelationValidator
}

// RelationProvider is the catalog provider of relation descriptors.
type RelationProvider struct{ Validators RelationValidators }

func (RelationProvider) ClassID() ids.OID { return ids.RelationClassID }

// Validate checks a relation descriptor against the catalog read v.
func (p RelationProvider) Validate(v catalog.Read, d catalog.Descriptor) error {
	r, ok := d.(*catalog.Relation)
	if !ok || d.Kind() != ids.RelationObject || d.Address().ClassID != ids.RelationClassID || d.Address().ObjectID == 0 || d.Address().SubID != 0 || d.Revision() == 0 {
		return fmt.Errorf("stuntgres: invalid relation descriptor")
	}
	ns, ok := v.Descriptor(r.Namespace())
	if !ok || ns.Kind() != ids.NamespaceObject {
		return fmt.Errorf("stuntgres: relation namespace is missing")
	}
	if r.OnCommit > parser.OnCommitDrop || r.OnCommit != parser.OnCommitUnspecified && (r.RelationKind != catalog.TableRelation || ns.(*catalog.Namespace).Temporary == (ids.SessionToken{})) {
		return fmt.Errorf("stuntgres: invalid temporary table commit action")
	}
	found := v.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: r.Namespace(), Name: r.Name()})
	if len(found) != 1 || found[0] != r.Address() || len(r.Columns) != len(r.Attributes) || len(r.Index) != len(r.Columns) {
		return fmt.Errorf("stuntgres: invalid relation identity")
	}
	if r.VirtualView && r.RelationKind != catalog.CatalogRelation {
		return fmt.Errorf("stuntgres: virtual view requires a catalog source")
	}
	if r.Populated && r.RelationKind != catalog.MaterializedViewRelation || r.View != nil && r.RelationKind != catalog.ViewRelation && r.RelationKind != catalog.MaterializedViewRelation {
		return fmt.Errorf("stuntgres: invalid stored definition kind")
	}
	if len(r.AutoExtensions) != 0 && r.RelationKind != catalog.MaterializedViewRelation {
		return fmt.Errorf("stuntgres: extension dependency requires a materialized view")
	}
	for i, address := range r.AutoExtensions {
		if address.ClassID != ids.ExtensionClassID || address.ObjectID == 0 || address.SubID != 0 || i > 0 && r.AutoExtensions[i-1].ObjectID >= address.ObjectID {
			return fmt.Errorf("stuntgres: invalid materialized extension dependency")
		}
		ext, ok := v.Extension(address)
		if !ok {
			return fmt.Errorf("stuntgres: missing materialized extension provider")
		}
		if err := (extension.CatalogProvider{Class: ids.ExtensionClassID}).Validate(v, ext); err != nil {
			return err
		}
	}
	if r.RelationKind.Stored() || r.RelationKind == catalog.PartitionedRelation || r.RelationKind == catalog.ViewRelation || r.RelationKind == catalog.CompositeRelation || r.RelationKind == catalog.ForeignRelation {
		types := v.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: r.Namespace(), Name: r.Name()})
		if len(types) != 0 {
			row, ok := v.Type(types[0])
			if len(types) != 1 || !ok || row.OwningRelation != r.Address() || row.SQLType.ID() != r.RowType {
				return catalog.Conflict{}
			}
		}
	}
	if r.RelationKind == catalog.ForeignRelation {
		if r.Foreign == nil {
			return fmt.Errorf("stuntgres: missing foreign relation definition")
		}
		server, ok := v.Foreign(r.Foreign.Server)
		if !ok || server.Address().ClassID != ids.ForeignServerClassID {
			return fmt.Errorf("stuntgres: missing foreign server identity")
		}
	} else if r.Foreign != nil {
		return fmt.Errorf("stuntgres: foreign definition on a local relation")
	}
	if r.ColumnMeta.HighWater < 0 {
		return fmt.Errorf("stuntgres: invalid relation attribute high-water")
	}
	if r.RelationKind == catalog.CatalogRelation && len(r.ColumnMeta.CatalogAttributes) != len(r.Columns) {
		return fmt.Errorf("stuntgres: invalid catalog attribute metadata")
	}
	for number, history := range r.ColumnMeta.Dropped {
		if number <= 0 || number > r.ColumnMeta.HighWater || history.Number != number || !history.Dropped || history.TypeID != 0 || history.NotNull || history.HasDefault || history.Generated != "" || history.Dimensions < 0 || r.IndexForAttribute(number) >= 0 {
			return fmt.Errorf("stuntgres: invalid dropped attribute metadata")
		}
	}
	for i, c := range r.Columns {
		offset, exists := r.Index[c.Name]
		ordered := i == 0 || r.Attributes[i] > r.Attributes[i-1]
		if r.RelationKind == catalog.CatalogRelation {
			ordered = r.Attributes[i] == pgcatalog.AttributeNumber(r.Address().ObjectID, c.Name, int32(i+1))
		}
		if r.Attributes[i] <= 0 || r.Attributes[i] > r.ColumnMeta.HighWater || !ordered || !exists || offset != i {
			return fmt.Errorf("stuntgres: invalid relation column identity")
		}
		if c.Usage.Type == nil || c.DeclaredDimensions < 0 {
			return fmt.Errorf("stuntgres: invalid relation column type")
		}
		typ, exists := v.LookupType(c.Usage.Type.ID())
		catalogNull := r.RelationKind == catalog.CatalogRelation && r.Address().ObjectID == pgcatalog.AttributeOID && r.Attributes[i] == 25 && c.Name == "attmissingval" && c.Usage.CatalogNullUsage()
		if !exists || typ != c.Usage.Type || !catalogNull && (!typ.Concrete() || !c.Usage.Valid()) {
			return fmt.Errorf("stuntgres: invalid relation column type")
		}
		if err := stored.ValidateUsageCollation(v, c.Usage); err != nil {
			return err
		}
	}
	if (r.Security.RLSEnabled || r.Security.RLSForced) && r.RelationKind != catalog.TableRelation && r.RelationKind != catalog.PartitionedRelation {
		return fmt.Errorf("stuntgres: row-security flags require a table")
	}
	if err := p.Validators.Policies(v, r); err != nil {
		return err
	}
	if err := stored.ValidateRules(v, r); err != nil {
		return err
	}
	if err := typedef.ValidateTypedAttachment(v, r); err != nil {
		return err
	}
	if err := ValidateInheritance(v, r); err != nil {
		return err
	}
	if r.RelationKind == catalog.CompositeRelation {
		if r.RowType == 0 || r.Sequence != nil || r.View != nil || r.SQLIndex != nil || len(r.ColumnMeta.Defaults) != 0 || len(r.ColumnMeta.Identities) != 0 || len(r.Constraints.NotNull) != 0 || len(r.Constraints.Check) != 0 || len(r.ColumnMeta.Generations) != 0 || len(r.Constraints.Unique) != 0 || len(r.Constraints.Exclusion) != 0 || len(r.Constraints.ForeignKeys) != 0 {
			return fmt.Errorf("stuntgres: composite backing relation has storage behavior")
		}
		for _, c := range r.Columns {
			if !c.Nullable {
				return fmt.Errorf("stuntgres: composite member has table constraints")
			}
		}
		return nil
	}
	if r.SQLIndex != nil && r.RelationKind != catalog.IndexRelation {
		return fmt.Errorf("stuntgres: index definition requires an index relation")
	}
	if r.RelationKind == catalog.IndexRelation {
		return index.ValidateIndexDefinition(v, r)
	}
	if r.RelationKind != catalog.TableRelation && r.RelationKind != catalog.PartitionedRelation && r.RelationKind != catalog.ForeignRelation && len(r.Constraints.Check) != 0 {
		return fmt.Errorf("stuntgres: CHECK requires a table descriptor")
	}
	if err := p.Validators.Partition(v, r); err != nil {
		return err
	}
	if r.RelationKind == catalog.SequenceRelation {
		if r.SequencePlanStamp == 0 || r.Sequence == nil || len(r.Columns) != 3 || r.Columns[0].Name != "last_value" || r.Columns[1].Name != "log_cnt" || r.Columns[2].Name != "is_called" || r.Columns[0].Usage.Type.ID() != sqltype.Int8 || r.Columns[1].Usage.Type.ID() != sqltype.Int8 || r.Columns[2].Usage.Type.ID() != sqltype.Bool {
			return fmt.Errorf("stuntgres: invalid sequence descriptor")
		}
		if _, _, err := sequence.BindOptions(r.Sequence, storage.SequenceGenerator{Last: r.Sequence.Start}, nil); err != nil {
			return err
		}
		if r.Sequence.Identity && r.Sequence.Owned == (ids.ObjectAddress{}) {
			return fmt.Errorf("stuntgres: identity sequence lacks ownership")
		}
		if owned := r.Sequence.Owned; owned != (ids.ObjectAddress{}) {
			tableAddress := owned
			tableAddress.SubID = 0
			table, ok := v.Relation(tableAddress)
			if !ok || table.RelationKind != catalog.TableRelation && table.RelationKind != catalog.PartitionedRelation || !table.HasAttribute(owned.SubID) || table.Owner != r.Owner || table.Namespace() != r.Namespace() {
				return fmt.Errorf("stuntgres: invalid sequence ownership dependency")
			}
			if r.Sequence.Identity {
				identity, exists := table.ColumnMeta.Identities[owned.SubID]
				if !exists || identity.Sequence != r.Address() {
					return fmt.Errorf("stuntgres: inconsistent identity sequence ownership")
				}
			}
		}
		return nil
	}
	if r.RelationKind == catalog.ViewRelation || r.RelationKind == catalog.MaterializedViewRelation {
		return p.Validators.Definition(v, r)
	}
	names := make(map[string]bool, len(r.Constraints.NotNull))
	attrs := make(map[int32]bool, len(r.Constraints.NotNull))
	for _, c := range r.Constraints.NotNull {
		if c.OID == 0 || c.Name == "" || len(c.Name) > 63 || !utf8.ValidString(c.Name) || strings.IndexByte(c.Name, 0) >= 0 || names[c.Name] || attrs[c.Attribute] || !r.HasAttribute(c.Attribute) {
			return fmt.Errorf("stuntgres: invalid not-null constraint identity")
		}
		names[c.Name], attrs[c.Attribute] = true, true
	}
	if err := ValidatePhysicalNotNull(v, r, attrs); err != nil {
		return err
	}
	for i, c := range r.Columns {
		// Virtual sources declare result nullability without physical table
		// constraint identities. Existing constraint records still validate above.
		_, physical := r.Constraints.PhysicalNotNull[r.Attributes[i]]
		if r.RelationKind != catalog.CatalogRelation && (attrs[r.Attributes[i]] || physical) != (c.NullabilityKnown && !c.Nullable) {
			return fmt.Errorf("stuntgres: inconsistent not-null projection")
		}
	}
	if err := ValidateChecks(v, r, names); err != nil {
		return err
	}
	if err := index.ValidateUniques(v, r, names); err != nil {
		return err
	}
	if err := index.ValidateExclusions(v, r, names); err != nil {
		return err
	}
	if err := ValidateForeignKeys(v, r, names); err != nil {
		return err
	}
	for attribute, identity := range r.ColumnMeta.Identities {
		slot := -1
		for i, live := range r.Attributes {
			if live == attribute {
				slot = i
				break
			}
		}
		if slot < 0 || identity.Mode != parser.IdentityAlways && identity.Mode != parser.IdentityByDefault {
			return fmt.Errorf("stuntgres: invalid column identity")
		}
		column := r.Columns[slot]
		id := column.Usage.Type.ID()
		if column.Nullable || !column.NullabilityKnown || id != sqltype.Int2 && id != sqltype.Int4 && id != sqltype.Int8 {
			return fmt.Errorf("stuntgres: invalid identity column type or nullability")
		}
		if _, exists := r.ColumnMeta.Defaults[attribute]; exists {
			return fmt.Errorf("stuntgres: identity column has default")
		}
		sequence, exists := v.Relation(identity.Sequence)
		owned := r.Address()
		owned.SubID = attribute
		if !exists || sequence.Sequence == nil || !sequence.Sequence.Identity || sequence.Sequence.Owned != owned || sequence.Owner != r.Owner || sequence.Namespace() != r.Namespace() {
			return fmt.Errorf("stuntgres: invalid column identity sequence")
		}
	}
	for attribute, definition := range r.ColumnMeta.Defaults {
		slot := -1
		for i, live := range r.Attributes {
			if live == attribute {
				slot = i
				break
			}
		}
		if definition.OID == 0 || slot < 0 || definition.Expression == nil || definition.Expression.ResultUsage() != r.Columns[slot].Usage {
			return fmt.Errorf("stuntgres: invalid column default destination")
		}
		var dependencies []ids.ObjectRef
		catalogSnapshot := false
		_, err := (stored.ScalarRenderer{Validation: true, Provenance: r, CapturedManifest: definition.Dependencies, Dependencies: &dependencies, CommentSnapshot: &catalogSnapshot}).SQL(context.Background(), v, definition.Expression)
		if err != nil || !slices.Equal(dependencies, definition.Dependencies) || catalogSnapshot != definition.CatalogSnapshot || stored.OmitColumnDefault(v, definition.Expression, r.Columns[slot].Usage) {
			return fmt.Errorf("stuntgres: invalid column default expression")
		}
	}
	for attribute, definition := range r.ColumnMeta.Generations {
		slot := -1
		for i, live := range r.Attributes {
			if live == attribute {
				slot = i
				break
			}
		}
		_, hasDefault := r.ColumnMeta.Defaults[attribute]
		_, hasIdentity := r.ColumnMeta.Identities[attribute]
		if definition.OID == 0 || slot < 0 || definition.Expression == nil || definition.Expression.ResultUsage() != r.Columns[slot].Usage || definition.Planned == nil || definition.Planned.ResultUsage() != r.Columns[slot].Usage || hasDefault || hasIdentity || !plan.GenerationImmutable(definition.Planned) {
			return fmt.Errorf("stuntgres: invalid column generation destination")
		}
		if definition.Kind != catalog.GenerationStored && definition.Kind != catalog.GenerationVirtual {
			return fmt.Errorf("stuntgres: invalid column generation kind")
		}
		if definition.Kind == catalog.GenerationVirtual {
			if err := VirtualDestination(r.Columns[slot]); err != nil {
				return err
			}
			if err := VirtualGenerationSecurity(definition.Expression); err != nil {
				return err
			}
			if err := VirtualGenerationSecurity(definition.Planned); err != nil {
				return err
			}
		}
		if err := ValidateGenerationPlan(v, r, definition.Planned); err != nil {
			return err
		}
		var dependencies []ids.ObjectRef
		catalogSnapshot := false
		_, err := (stored.ScalarRenderer{Validation: true, Owner: r, CapturedManifest: definition.Dependencies, Dependencies: &dependencies, CommentSnapshot: &catalogSnapshot}).SQL(context.Background(), v, definition.Expression)
		if err != nil || !slices.Equal(dependencies, definition.Dependencies) || catalogSnapshot != definition.CatalogSnapshot {
			return fmt.Errorf("stuntgres: invalid column generation expression")
		}
	}
	if err := ValidateGenerationProviders(v, r); err != nil {
		return err
	}
	for sub, text := range r.ColumnMeta.Comments {
		if !r.HasAttribute(sub) || text == "" {
			return fmt.Errorf("stuntgres: invalid relation column comment")
		}
	}
	return nil
}
