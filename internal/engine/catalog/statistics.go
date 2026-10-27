package catalog

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

// StatisticsSelection owns canonical stable attribute identities and kind flags.
// Its fixed storage never aliases parser or caller buffers.
type StatisticsSelection struct {
	keys        [8]int32
	count       uint8
	kinds       uint8
	expressions uint8
}

func NewStatisticsSelection(keys []int32, expressionCount int, kinds string) (StatisticsSelection, error) {
	var s StatisticsSelection
	if expressionCount < 0 || len(keys)+expressionCount < 1 || len(keys)+expressionCount > len(s.keys) {
		return s, fmt.Errorf("stuntgres: invalid statistics width")
	}
	s.expressions = uint8(expressionCount)
	copy(s.keys[:], keys)
	slices.Sort(s.keys[:len(keys)])
	for i, key := range s.keys[:len(keys)] {
		if key <= 0 || i > 0 && key == s.keys[i-1] {
			return StatisticsSelection{}, fmt.Errorf("stuntgres: invalid statistics attribute")
		}
	}
	s.count = uint8(len(keys))
	for _, k := range kinds {
		switch k {
		case 'd':
			s.kinds |= 1
		case 'f':
			s.kinds |= 2
		case 'm':
			s.kinds |= 4
		case 'e':
			s.kinds |= 8
		default:
			return StatisticsSelection{}, fmt.Errorf("stuntgres: invalid statistics kind")
		}
	}
	if s.kinds == 0 || (s.kinds&8 != 0) != (expressionCount > 0) || len(keys)+expressionCount == 1 && (expressionCount != 1 || s.kinds != 8) {
		return StatisticsSelection{}, fmt.Errorf("stuntgres: invalid statistics kinds")
	}
	return s, nil
}
func (s StatisticsSelection) Len() int           { return int(s.count) }
func (s StatisticsSelection) ExpressionLen() int { return int(s.expressions) }
func (s StatisticsSelection) DimensionLen() int  { return s.Len() + s.ExpressionLen() }
func (s StatisticsSelection) Dimension(i int) int32 {
	if i < s.Len() {
		return s.Key(i)
	}
	if i >= s.DimensionLen() {
		panic("statistics dimension index out of range")
	}
	return -int32(i - s.Len() + 1)
}
func (s StatisticsSelection) Key(i int) int32 {
	if i < 0 || i >= int(s.count) {
		panic("statistics key index out of range")
	}
	return s.keys[i]
}
func (s StatisticsSelection) Kinds() string {
	var kinds [4]byte
	n := 0
	for i, k := range "dfme" {
		if s.kinds&(1<<i) != 0 {
			kinds[n] = byte(k)
			n++
		}
	}
	return string(kinds[:n])
}
func (s StatisticsSelection) Selects(attribute int32) bool {
	for i := range s.Len() {
		if s.keys[i] == attribute {
			return true
		}
	}
	return false
}

type Statistics struct {
	ObjectAddress, ObjectNamespace ids.ObjectAddress
	ObjectRevision                 ids.DefinitionRevision
	ObjectName                     string
	Owner                          authorization.PrincipalRef
	Relation                       ids.ObjectRef
	Selection                      StatisticsSelection
	Expressions                    StatisticsExpressions
	Target                         int16
	TargetSet                      bool
}

func (d *Statistics) Address() ids.ObjectAddress       { return d.ObjectAddress }
func (d *Statistics) Namespace() ids.ObjectAddress     { return d.ObjectNamespace }
func (d *Statistics) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (d *Statistics) Name() string                     { return d.ObjectName }
func (*Statistics) Kind() ids.ObjectKind               { return ids.StatisticsObject }
func (s *State) Statistics(a ids.ObjectAddress) (*Statistics, bool) {
	return descriptorAs[*Statistics](s, a)
}
func (r Read) Statistics(a ids.ObjectAddress) (*Statistics, bool) { return r.state.Statistics(a) }

type StatisticsProvider struct{}

func (StatisticsProvider) ClassID() ids.OID { return ids.StatisticExtClassID }
func (StatisticsProvider) Validate(v Read, raw Descriptor) error {
	d, ok := raw.(*Statistics)
	invalid := func() error { return fmt.Errorf("stuntgres: invalid statistics descriptor authority") }
	if !ok || d == nil || v.Owner() == nil || v.State() == nil || v.Get(d.Address()) != d || d.Address().ClassID != ids.StatisticExtClassID || d.Address().ObjectID == 0 || d.Address().SubID != 0 || d.Revision() == 0 || d.Name() == "" || v.NameAddress(DescriptorNameKey(d)) != d.Address() {
		return invalid()
	}
	if d.Target < 0 || d.Target > 10000 || !d.TargetSet && d.Target != 0 {
		return invalid()
	}
	if v.Owner().Principals == nil || d.Owner.ID() == 0 || d.Owner != v.Owner().Principals.Principal(d.Owner.ID()) {
		return invalid()
	}
	if _, ok := v.Namespace(d.Namespace()); !ok {
		return invalid()
	}
	if d.Relation.Domain() != v.Domain() || d.Relation.Epoch != v.Epoch() || d.Relation.Address.ClassID != ids.RelationClassID || d.Relation.Address.SubID != 0 {
		return invalid()
	}
	rel, ok := v.Relation(d.Relation.Address)
	if !ok || !StatisticsRelationKind(rel.RelationKind) {
		return invalid()
	}
	s := d.Selection
	if s.DimensionLen() < 1 || s.DimensionLen() > 8 || s.Kinds() == "" || s.ExpressionLen() != d.Expressions.Len() {
		return invalid()
	}
	if checked, err := NewStatisticsSelection(s.keys[:s.Len()], s.ExpressionLen(), s.Kinds()); err != nil || checked != s {
		return invalid()
	}
	for i := range s.Len() {
		if s.Key(i) <= 0 || i > 0 && s.Key(i-1) >= s.Key(i) || !rel.HasAttribute(s.Key(i)) {
			return invalid()
		}
	}
	for i := range d.Expressions.Len() {
		e := d.Expressions.At(i)
		if e.Definition() == nil || e.Execution() == nil || e.Usage().Type == nil || e.Definition().ResultUsage() != e.Usage() || e.Execution().ResultUsage() != e.Usage() {
			return invalid()
		}
		if !e.Usage().Valid() {
			return invalid()
		}
		if typ, ok := v.LookupType(e.Usage().Type.ID()); !ok || typ != e.Usage().Type {
			return invalid()
		}
		for j := range e.AttributeLen() {
			if e.Attribute(j) <= 0 {
				return invalid()
			}
			for k := range j {
				if e.Attribute(k) == e.Attribute(j) {
					return invalid()
				}
			}
		}
		for j := range e.InputLen() {
			ref := e.Input(j)
			if ref.Domain() != v.Domain() || ref.Epoch != v.Epoch() || ref.Address.ClassID != ids.RelationClassID || ref.Address.ObjectID != rel.Address().ObjectID || !rel.HasAttribute(ref.Address.SubID) {
				return invalid()
			}
			found := false
			for k := range e.AttributeLen() {
				if e.Attribute(k) == ref.Address.SubID {
					found = true
				}
			}
			if !found {
				return invalid()
			}
			for k := range j {
				if e.Input(k) == ref {
					return invalid()
				}
			}
		}
		for j := range e.DependencyLen() {
			ref := e.Dependency(j)
			address := ref.Address
			address.SubID = 0
			if ref.Domain() != v.Domain() || ref.Epoch != v.Epoch() || v.Get(address) == nil {
				return invalid()
			}
		}
	}
	return nil
}
func StatisticsRelationKind(k RelationKind) bool {
	return k == TableRelation || k == PartitionedRelation || k == MaterializedViewRelation || k == ForeignRelation
}
