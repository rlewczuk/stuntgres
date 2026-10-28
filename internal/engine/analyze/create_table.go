package analyze

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Column-local structure is validated as CREATE reaches each typed column,
// before later elements or any default-expression cooking.
type ColumnDeclaration struct {
	ColumnNotNull    bool
	ColumnName       string
	NotNull          bool
	Name             string
	DefaultExpr      parser.Expr
	CopiedDefault    bool
	Identity         *parser.IdentityDefinition
	Generation       *parser.GenerationDefinition
	CopiedGeneration bool
	Serial           bool
	// Empty means unspecified; selected LIKE storage includes type defaults,
	// whereas selected default compression remains unspecified.
	Storage          string
	Compression      string
}

type InheritanceInput struct {
	Ref    ids.ObjectRef
	Source *catalog.Relation
}

func (in LikeInput) Attribute(destination *catalog.Relation, old int32) (int32, error) {
	slot := slices.Index(in.Source.Attributes, old)
	if slot < 0 {
		return 0, fmt.Errorf("stuntgres: missing LIKE index attribute")
	}
	return destination.Attributes[in.TargetSlot(slot)], nil
}

// Source provenance is CREATE-local, never part of the persistent constraint.
// Ordinary LIKE index copies retain their existing independent copy semantics.
type UniqueConstraint struct {
	catalog.UniqueConstraint
	Source       *catalog.Relation
	Like         *LikeInput
	CopyComments bool
}

type LikeMask uint16

const (
	LikeComments LikeMask = 1 << iota
	LikeCompression
	LikeConstraints
	LikeDefaults
	LikeGenerated
	LikeIdentity
	LikeIndexes
	LikeStatistics
	LikeStorage
	LikeAll = (1 << 9) - 1
)

type LikeInput struct {
	Ref         ids.ObjectRef
	Source      *catalog.Relation
	SourceType  *catalog.Type
	Offset      int
	TargetSlots []int
	Options     LikeMask
	RowRef      func(ids.ObjectRef) ids.ObjectRef
	Slots       map[int32]int
}

func (in LikeInput) TargetSlot(slot int) int {
	if in.TargetSlots != nil {
		return in.TargetSlots[slot]
	}
	return in.Offset + slot
}

// Captures are transient CREATE inputs. Source rows, authority and object-literal
// references are never imported; row owners and stable attributes are mapped.
func (in LikeInput) RowMapper(destination *catalog.Relation, authority catalog.Read) (func(ids.ObjectRef) ids.ObjectRef, map[int32]int) {
	attributes := make(map[int32]int32, len(in.Source.Attributes))
	slots := make(map[int32]int, len(in.Source.Attributes))
	for slot, attribute := range in.Source.Attributes {
		target := destination.Attributes[in.TargetSlot(slot)]
		attributes[attribute] = target
		slots[target] = in.TargetSlot(slot)
	}
	// Retained TEMP source rows can carry an older epoch. Only mapped local
	// row origins receive the fresh CREATE candidate's reference authority.
	target := authority.Owner().Domain.Ref(authority.Epoch(), destination.Address())
	return func(ref ids.ObjectRef) ids.ObjectRef {
		address := ref.Address
		address.SubID = 0
		if address == in.Source.Address() {
			attribute := ref.Address.SubID
			ref = target
			ref.Address.SubID = attributes[attribute]
		}
		return ref
	}, slots
}
