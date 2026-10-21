package sqltype

// SQLJSONPathMutable inspects the already parsed constant path and declared
// binding types, including typed NULLs. Bare temporal variables are immutable.
func SQLJSONPathMutable(path Value, names []string, usages []Usage) bool {
	if path.null {
		return false
	}
	if path.jsonpath == nil {
		return true
	}
	strict := path.jsonpath.strict
	var walk func(*jsonpathNode, byte, byte) (byte, bool)
	walk = func(n *jsonpathNode, incoming, current byte) (byte, bool) {
		if n == nil {
			return incoming, false
		}
		category := byte(0)
		mutable := false
		switch n.kind {
		case "variable":
			for i, name := range names {
				if name == n.text {
					switch usages[i].Type.BaseType().ID() {
					case Date, Time, Timestamp:
						category = 1
					case Timetz, Timestamptz:
						category = 2
					}
					break
				}
			}
		case "@":
			category = current
		case "filter":
			// The predicate sees this candidate as @; the result remains
			// the candidate, and nested predicates cannot overwrite outer @.
			_, mutable = walk(n.left, 0, incoming)
			category = incoming
		case "index", "anyarray":
			if !strict {
				category = incoming
			}
		case "recursive":
			// LAST is encoded as -1, not as depth zero.
			if n.first == 0 {
				category = incoming
			}
		}
		if n.kind != "filter" {
			left, lm := walk(n.left, 0, current)
			right, rm := walk(n.right, 0, current)
			mutable = lm || rm
			switch n.kind {
			case "==", "!=", "<", ">", "<=", ">=":
				mutable = mutable || left != 0 && right != 0 && left != right
			}
		}
		for _, arg := range n.args {
			_, a := walk(arg.from, 0, current)
			_, b := walk(arg.to, 0, current)
			mutable = mutable || a || b
		}
		if n.next != nil {
			next, nm := walk(n.next, category, current)
			category = next
			mutable = mutable || nm
		}
		return category, mutable
	}
	_, mutable := walk(path.jsonpath.root, 0, 0)
	return mutable
}
