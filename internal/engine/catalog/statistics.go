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
	keys  [8]int32
	count uint8
	kinds uint8
}

func NewStatisticsSelection(keys []int32, kinds string) (StatisticsSelection, error) {
	var s StatisticsSelection
	if len(keys) < 2 || len(keys) > len(s.keys) {
		return s, fmt.Errorf("stuntgres: invalid statistics width")
	}
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
		default:
			return StatisticsSelection{}, fmt.Errorf("stuntgres: invalid statistics kind")
		}
	}
	if s.kinds == 0 {
		return StatisticsSelection{}, fmt.Errorf("stuntgres: empty statistics kinds")
	}
	return s, nil
}
func (s StatisticsSelection) Len() int { return int(s.count) }
func (s StatisticsSelection) Key(i int) int32 {
	if i < 0 || i >= int(s.count) {
		panic("statistics key index out of range")
	}
	return s.keys[i]
}
func (s StatisticsSelection) Kinds() string {
	switch s.kinds {
	case 1:
		return "d"
	case 2:
		return "f"
	case 3:
		return "df"
	case 4:
		return "m"
	case 5:
		return "dm"
	case 6:
		return "fm"
	case 7:
		return "dfm"
	}
	return ""
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
	if s.Len() < 2 || s.Len() > 8 || s.Kinds() == "" {
		return invalid()
	}
	for i := range s.Len() {
		if s.Key(i) <= 0 || i > 0 && s.Key(i-1) >= s.Key(i) || !rel.HasAttribute(s.Key(i)) {
			return invalid()
		}
	}
	return nil
}
func StatisticsRelationKind(k RelationKind) bool {
	return k == TableRelation || k == PartitionedRelation || k == MaterializedViewRelation || k == ForeignRelation
}
