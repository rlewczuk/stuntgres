package catalog

import "stuntgres/internal/engine/ids"

// VisitDependencies shares AUTO input and NORMAL provider semantics between
// catalog projection, DROP closure and declaration ordering, without duplicate edges.
func (d *Statistics) VisitDependencies(visit func(ids.ObjectAddress, byte)) {
	visit(d.Namespace(), 'n')
	if d.Selection.Len() == 0 {
		visit(d.Relation.Address, 'a')
	}
	for i := range d.Selection.Len() {
		a := d.Relation.Address
		a.SubID = d.Selection.Key(i)
		visit(a, 'a')
	}
	for i := range d.Expressions.Len() {
		e := d.Expressions.At(i)
		for j := range e.InputLen() {
			a := e.Input(j).Address
			if d.Selection.Selects(a.SubID) {
				continue
			}
			duplicate := false
			for p := range i {
				prior := d.Expressions.At(p)
				for q := range prior.InputLen() {
					if prior.Input(q).Address == a {
						duplicate = true
					}
				}
			}
			for q := range j {
				if e.Input(q).Address == a {
					duplicate = true
				}
			}
			if !duplicate {
				visit(a, 'a')
			}
		}
		for j := range e.DependencyLen() {
			a := e.Dependency(j).Address
			if a.ClassID == ids.RelationClassID && a.ObjectID == d.Relation.Address.ObjectID || a == d.Namespace() {
				continue
			}
			duplicate := false
			for p := range i {
				prior := d.Expressions.At(p)
				for q := range prior.DependencyLen() {
					if prior.Dependency(q).Address == a {
						duplicate = true
					}
				}
			}
			for q := range j {
				if e.Dependency(q).Address == a {
					duplicate = true
				}
			}
			if !duplicate {
				visit(a, 'n')
			}
		}
	}
}
