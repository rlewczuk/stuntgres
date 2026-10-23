package catalog

// RelationOption retains explicit SQL spelling beside its validated logical value.
// Published entries and slices are immutable, like the owning descriptor.
type RelationOption struct {
	Name, Text string
	Kind       RelationOptionKind
	Integer    int64
	Real       float64
	Boolean    bool
	Enum       string
}

type RelationOptionKind uint8

const (
	RelationOptionInteger RelationOptionKind = iota + 1
	RelationOptionReal
	RelationOptionBoolean
	RelationOptionEnum
)

// RelationOptions deliberately has no physical TOAST relation counterpart.
// Toast holds validated intent only; only Base is projected in pg_class.
type RelationOptions struct {
	Base  []RelationOption
	Toast []RelationOption
}
