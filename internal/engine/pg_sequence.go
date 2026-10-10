package engine

import (
	"context"
	"stuntgres/internal/sqltype"
)

// capturePGSequence reads only immutable definitions. The retained committed
// snapshot and current owner's overlay are distinct authorities: an override
// replaces (or deletes) its base row, and private additions contribute new rows.
// Caller holds Engine.mu and statement admission; no generator is accessed.
func capturePGSequence(ctx context.Context, base *catalogState, own *catalogChanges, ref ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	appendDefinition := func(descriptor Descriptor) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		relation, ok := descriptor.(*relationDescriptor)
		if !ok || relation.relationKind != sequenceRelation {
			return nil
		}
		options := relation.sequence
		relid, err := sqltype.OIDValue(source.columns[0].Usage.Type, uint32(relation.address.ObjectID))
		if err != nil {
			return err
		}
		typid, err := sqltype.OIDValue(source.columns[1].Usage.Type, uint32(options.typ))
		if err != nil {
			return err
		}
		row := make([]sqltype.Value, 8)
		row[0], row[1] = relid, typid
		for i, n := range [5]int64{options.start, options.increment, options.max, options.min, options.cache} {
			row[i+2], err = sqltype.Integer(source.columns[i+2].Usage.Type, n)
			if err != nil {
				return err
			}
		}
		row[7] = sqltype.Boolean(source.columns[7].Usage.Type, options.cycle)
		rows = append(rows, row)
		return nil
	}
	for address, descriptor := range base.descriptors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if own != nil {
			if _, overridden := own.descriptors[address]; overridden {
				continue
			}
		}
		if err := appendDefinition(descriptor); err != nil {
			return nil, err
		}
	}
	if own != nil {
		for _, descriptor := range own.descriptors {
			if err := appendDefinition(descriptor); err != nil {
				return nil, err
			}
		}
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
