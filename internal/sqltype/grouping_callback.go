package sqltype

import "fmt"

// A selected callback contains immutable source identity only. A boundary is
// borrowed by the real equality/hash operation, including recursive array leaves.
type GroupingCallbackReference interface{ GroupingCallbackType() *Descriptor }
type GroupingBoundary interface {
	EnterGroupingCallback(GroupingCallbackReference) (RoutineBodyToken, error)
}

func (g Grouping) WithCallbacks(equal, hash GroupingCallbackReference) (Grouping, error) {
	for _, ref := range []GroupingCallbackReference{equal, hash} {
		if ref != nil && ref.GroupingCallbackType() != g.usage.Type {
			return Grouping{}, fmt.Errorf("stuntgres: foreign grouping callback type")
		}
	}
	g.equalityCallback, g.hashCallback = equal, hash
	return g, nil
}

func (g Grouping) WithEqualityCallback(equal GroupingCallbackReference) (Grouping, error) {
	return g.WithCallbacks(equal, g.hashCallback)
}
func (g Grouping) ElementGrouping() (Grouping, bool) {
	if g.element == nil {
		return Grouping{}, false
	}
	return *g.element, true
}
func (g Grouping) WithElementGrouping(element Grouping) (Grouping, error) {
	if g.element == nil || g.element.usage != element.usage {
		return Grouping{}, fmt.Errorf("stuntgres: invalid grouping element callback")
	}
	g.element = &element
	return g, nil
}
func enterGroupingCallback(reference GroupingCallbackReference, boundary GroupingBoundary) (RoutineBodyToken, error) {
	if reference == nil {
		return RoutineBodyToken{}, nil
	}
	if boundary == nil {
		return RoutineBodyToken{}, fmt.Errorf("stuntgres: missing actual grouping callback owner")
	}
	return boundary.EnterGroupingCallback(reference)
}

// VisitCallbacks is initializer-only; repeated rows do not allocate a callback
// list or enumerate unrelated opclass members.
func (g Grouping) VisitCallbacks(yield func(GroupingCallbackReference) error) error {
	if g.equalityCallback != nil {
		if err := yield(g.equalityCallback); err != nil {
			return err
		}
	}
	if g.hashCallback != nil {
		if err := yield(g.hashCallback); err != nil {
			return err
		}
	}
	if g.element != nil {
		return g.element.VisitCallbacks(yield)
	}
	return nil
}

func (g Grouping) VisitIndexSupports(visit func(IndexSupport, uint16) error) error {
	if err := g.comparison.VisitIndexSupports(visit); err != nil {
		return err
	}
	if g.element != nil {
		return g.element.VisitIndexSupports(visit)
	}
	return nil
}

// RemapCallbacks follows an already selected recursive grouping graph; it never
// discovers a new default support function during replay or retag.
func (g Grouping) RemapCallbacks(old Grouping, mapReference func(GroupingCallbackReference) (GroupingCallbackReference, error)) (Grouping, error) {
	var err error
	for i, reference := range [2]GroupingCallbackReference{old.equalityCallback, old.hashCallback} {
		if reference == nil {
			continue
		}
		mapped, failure := mapReference(reference)
		if failure != nil {
			return Grouping{}, failure
		}
		if i == 0 {
			g.equalityCallback = mapped
		} else {
			g.hashCallback = mapped
		}
	}
	if old.element != nil {
		if g.element == nil {
			return Grouping{}, fmt.Errorf("stuntgres: grouping callback recursion changed")
		}
		element, failure := g.element.RemapCallbacks(*old.element, mapReference)
		if failure != nil {
			return Grouping{}, failure
		}
		g.element = &element
	}
	return g, err
}
