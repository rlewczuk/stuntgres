package sqltype

import "fmt"

// GiST admission is deliberately limited to the logical exclusion providers.
// It supplies no B-tree comparator and makes no physical-tree claim.
func LookupGiSTIndexClass(name string) (IndexClass, bool) {
	switch name {
	case "inet_ops":
		return IndexClass{Name: name, Family: "network_ops", input: Inet, gist: true, oid: 10017}, true
	case "range_ops":
		return IndexClass{Name: name, Family: "range_ops", input: 3831, gist: true, oid: 10078}, true
	}
	return IndexClass{}, false
}

func DefaultGiSTIndexClass(t *Descriptor) (IndexClass, bool) {
	if t != nil && t.Concrete() && isRange(t.BaseType().id) {
		return LookupGiSTIndexClass("range_ops")
	}
	return IndexClass{}, false
}
func DefaultGiSTIndexClassID(id ID) (IndexClass, bool) {
	if isRange(id) {
		return LookupGiSTIndexClass("range_ops")
	}
	return IndexClass{}, false
}

func bindGiSTExclusion(u Usage, class IndexClass, name string) (ExclusionOperator, error) {
	if !class.Accepts(u.Type) {
		return ExclusionOperator{}, fmt.Errorf("invalid GiST exclusion class input")
	}
	t := u.Type.BaseType()
	p := ExclusionOperator{Name: name, Left: t.id, Right: t.id, usage: u, operatorInputs: [2]ID{class.input, class.input}}
	if class.input == Inet && t.id == Inet && (name == "=" || name == "&&") {
		if name == "=" {
			var err error
			p.comparison, err = BindOrdering(u)
			return p, err
		}
		return p, nil
	}
	if class.input == 3831 && isRange(t.id) && name == "&&" && u == t.Usage() {
		var err error
		p.rangePlan, err = BindRangeOperator(RangeOverlap, t.rangeBoolean.Usage(), u, u)
		return p, err
	}
	return ExclusionOperator{}, failure("42809", fmt.Sprintf(`operator %s(%s,%s) is not a member of operator family "%s"`, name, t.DiagnosticName(), t.DiagnosticName(), class.Family))
}
