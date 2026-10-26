package sqltype

import "fmt"

// GiST retains exact logical keys without a B-tree comparator or disk tree.
func LookupGiSTIndexClass(name string) (IndexClass, bool) {
	c := IndexClass{Name: name, Family: name, gist: true}
	switch name {
	case "inet_ops":
		c.input, c.oid, c.Family = Inet, 10017, "network_ops"
	case "box_ops":
		c.input, c.oid = Box, 10060
	case "point_ops":
		c.input, c.oid = Point, 10061
	case "poly_ops":
		c.input, c.oid = Polygon, 10062
	case "circle_ops":
		c.input, c.oid = Circle, 10063
	case "tsvector_ops":
		c.input, c.oid = TSVector, 10072
	case "tsquery_ops":
		c.input, c.oid = TSQuery, 10075
	case "range_ops":
		c.input, c.oid = 3831, 10078
	case "multirange_ops":
		c.input, c.oid = 4537, 10082
	default:
		return IndexClass{}, false
	}
	return c.withSeedIdentity(783), true
}

func DefaultGiSTIndexClass(t *Descriptor) (IndexClass, bool) {
	if t == nil || !t.Concrete() {
		return IndexClass{}, false
	}
	t = t.BaseType()
	if t.IsRange() {
		return LookupGiSTIndexClass("range_ops")
	}
	if t.IsMultirange() {
		return LookupGiSTIndexClass("multirange_ops")
	}
	return DefaultGiSTIndexClassID(t.ID())
}
func DefaultGiSTIndexClassID(id ID) (IndexClass, bool) {
	var name string
	switch id {
	case Box:
		name = "box_ops"
	case Point:
		name = "point_ops"
	case Polygon:
		name = "poly_ops"
	case Circle:
		name = "circle_ops"
	case TSVector:
		name = "tsvector_ops"
	case TSQuery:
		name = "tsquery_ops"
	default:
		if isRange(id) {
			name = "range_ops"
		} else if isMultirange(id) {
			name = "multirange_ops"
		} else {
			return IndexClass{}, false
		}
	}
	return LookupGiSTIndexClass(name)
}

func isMultirange(id ID) bool {
	return id == Int4Multirange || id == Int8Multirange || id == NumMultirange || id == 4533 || id == 4534 || id == 4535
}

func bindGiSTExclusion(u Usage, class IndexClass, name string) (ExclusionOperator, error) {
	if !class.Accepts(u.Type) {
		return ExclusionOperator{}, fmt.Errorf("invalid GiST exclusion class input")
	}
	t := u.Type.BaseType()
	p := ExclusionOperator{Name: name, Left: t.id, Right: t.id, usage: u, operatorInputs: [2]ID{class.input, class.input}}
	base := t.Usage()
	if class.IsSPGiST() && class.input == Text && (t.id == Text || t.id == VarChar) && name == "=" {
		p.comparison = Comparison{left: u.Type, right: u.Type, inputCollation: u.Collation}
		return p, nil
	}
	if class.input == Inet && (t.id == Inet || t.id == Cidr) && (name == "=" || name == "<>" || name == "&&") {
		if name != "&&" {
			p.comparison = Comparison{left: u.Type, right: u.Type, inputCollation: u.Collation}
		}
		return p, nil
	}
	if (t.IsRange() || t.IsMultirange()) && (name == "=" || name == "&&" || name == "-|-") {
		if name == "=" {
			p.comparison = Comparison{left: u.Type, right: u.Type, inputCollation: u.Collation}
			return p, nil
		}
		op, _ := RangeOperatorKind(name)
		var err error
		p.rangePlan, err = BindRangeOperator(op, t.RangeType().rangeBoolean.Usage(), base, base)
		p.gistPredicate = true
		return p, err
	}
	var err error
	p.gistPredicate = true
	switch t.id {
	case Point:
		if name == "~=" {
			p.pointPlan, err = BindPoint(PointSame, t.pointBoolean.Usage(), base, base)
			return p, err
		}
	case Polygon:
		if name == "~=" {
			p.polygonPlan, err = BindPolygon(PolygonSame, t.polygonBoolean.Usage(), base, base)
			return p, err
		}
	case Circle:
		if name == "~=" || name == "&&" {
			op, _ := CircleKind(name)
			p.circlePlan, err = BindCircle(op, t.circleBoolean.Usage(), base, base)
			return p, err
		}
	case Box:
		if name == "~=" {
			return p, nil
		}
	}
	if (t.id == Box || t.id == Polygon) && name == "&&" {
		op, _ := GeometryRelationKind(name, t.id, t.id)
		p.geometryPlan, err = BindGeometryRelation(op, relationBooleanOwner(t).Usage(), [2]Usage{base, base})
		return p, err
	}
	return ExclusionOperator{}, failure("42809", fmt.Sprintf(`operator %s(%s,%s) is not a member of operator family "%s"`, name, t.DiagnosticName(), t.DiagnosticName(), class.Family))
}
