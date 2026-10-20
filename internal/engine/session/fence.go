package session

// FenceKind fixes where publication checks a fence. The root engine declares
// the kinds and the order in which publication checks them.
type FenceKind uint8

// Fence is one feature's commit precondition in the catalog journal. A fence
// is immutable once recorded; journal clones and savepoint marks share it.
// The root engine records concrete fences and recovers them with one checked
// assertion when it validates them.
type Fence interface {
	Kind() FenceKind
}
