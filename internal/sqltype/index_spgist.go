package sqltype

// LookupSPGiSTIndexClass supplies released seed identity only. Runtime class
// selection remains catalog-owned, including SQL aliases and custom ranges.
func LookupSPGiSTIndexClass(name string) (IndexClass, bool) {
	c := IndexClass{Name: name, Family: name, spgist: true}
	switch name {
	case "inet_ops":
		c.input, c.oid, c.Family = Inet, 10018, "network_ops"
	case "range_ops":
		c.input, c.oid = 3831, 10079
	case "box_ops":
		c.input, c.oid = Box, 10083
	case "quad_point_ops":
		c.input, c.oid = Point, 10084
	case "kd_point_ops":
		c.input, c.oid = Point, 10085
	case "text_ops":
		c.input, c.oid = Text, 10086
	case "poly_ops":
		c.input, c.oid = Polygon, 10087
	default:
		return IndexClass{}, false
	}
	return c.withSeedIdentity(4000), true
}
