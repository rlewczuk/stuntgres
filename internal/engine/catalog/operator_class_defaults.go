package catalog

import (
	"cmp"
	"errors"
	"slices"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type defaultClassPair struct{ Method, Input ids.OID }

// Buckets are immutable derived descriptor indexes, not unique name bindings.
func (s *State) updateDefaultClasses(old, next Descriptor) {
	a, was := old.(*OperatorClass)
	b, is := next.(*OperatorClass)
	was = was && a.Default
	is = is && b.Default
	if !was && !is {
		return
	}
	if was && is && a.Address() == b.Address() && a.Method == b.Method && a.InputType == b.InputType && a.Name() == b.Name() && a.Namespace() == b.Namespace() {
		return
	}
	if was && is && a.Address() == b.Address() && a.Method == b.Method && a.InputType == b.InputType {
		key := defaultClassPair{b.Method, b.InputType}
		bucket, _ := s.defaultClasses.Get(key)
		if len(bucket) < 2 {
			return
		}
		copy := slices.Clone(bucket)
		s.sortDefaultClasses(copy)
		s.defaultClasses = s.defaultClasses.Set(key, copy)
		return
	}
	if was {
		key := defaultClassPair{a.Method, a.InputType}
		bucket, _ := s.defaultClasses.Get(key)
		at := slices.Index(bucket, a.Address().ObjectID)
		if at >= 0 {
			if len(bucket) == 1 {
				s.defaultClasses = s.defaultClasses.Delete(key)
			} else {
				copy := make([]ids.OID, len(bucket)-1)
				copy = append(copy[:0], bucket[:at]...)
				copy = append(copy, bucket[at+1:]...)
				s.defaultClasses = s.defaultClasses.Set(key, copy)
			}
		}
	}
	if is {
		key := defaultClassPair{b.Method, b.InputType}
		bucket, _ := s.defaultClasses.Get(key)
		copy := make([]ids.OID, 0, len(bucket)+1)
		copy = append(copy, bucket...)
		copy = append(copy, b.Address().ObjectID)
		s.sortDefaultClasses(copy)
		s.defaultClasses = s.defaultClasses.Set(key, copy)
	}
}

func (s *State) sortDefaultClasses(bucket []ids.OID) {
	slices.SortFunc(bucket, func(x, y ids.OID) int {
		left, _ := s.findDescriptor(ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: x})
		right, _ := s.findDescriptor(ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: y})
		if n := cmp.Compare(left.Name(), right.Name()); n != 0 {
			return n
		}
		if n := cmp.Compare(left.Namespace().ObjectID, right.Namespace().ObjectID); n != 0 {
			return n
		}
		return cmp.Compare(x, y)
	})
}

// AnyDefaultOperatorClass serves sequential CREATE admission only. Concurrent
// publications retain every descriptor; absence is not a unique-name fence.
func (r Read) AnyDefaultOperatorClass(method, input ids.OID) (*OperatorClass, bool) {
	if r.state == nil {
		return nil, false
	}
	bucket, _ := r.state.defaultClasses.Get(defaultClassPair{method, input})
	if len(bucket) == 0 {
		return nil, false
	}
	return r.OperatorClass(bucket[0])
}

func (r Read) defaultClassCount(method, input ids.OID) int {
	if r.state == nil {
		return 0
	}
	bucket, _ := r.state.defaultClasses.Get(defaultClassPair{method, input})
	return len(bucket)
}

// HasMultiplePersistentDefaults observes only the persistent replay graph.
// Runtime resolution deliberately continues to include the full supplied view.
func (r Read) HasMultiplePersistentDefaults() bool {
	if r.state == nil {
		return false
	}
	for _, bucket := range r.state.defaultClasses.All() {
		if len(bucket) < 2 {
			continue
		}
		persistent := false
		for _, oid := range bucket {
			class, _ := r.OperatorClass(oid)
			if r.state.MustNamespace(class.Namespace()).Temporary != (ids.SessionToken{}) {
				continue
			}
			if persistent {
				return true
			}
			persistent = true
		}
	}
	return false
}

// UniqueDefaultIndexClass is an optional metadata/proof predicate, not a SQL
// default binder. An ambiguous exact default never falls through adaptation.
func (r Read) UniqueDefaultIndexClass(method ids.OID, t *sqltype.Descriptor) (sqltype.IndexClass, bool) {
	if t == nil || !t.Concrete() {
		return sqltype.IndexClass{}, false
	}
	t = t.BaseType()
	input := t.ID()
	if t.IsEnum() && r.defaultClassCount(method, ids.OID(input)) == 0 {
		input = 3500
	}
	if m, ok := r.AccessMethod(method); ok && m.Native == GistAccessMethod && r.defaultClassCount(method, ids.OID(input)) == 0 {
		if t.IsRange() {
			input = 3831
		} else if t.IsMultirange() {
			input = 4537
		}
	}
	return r.uniqueDefaultIndexClassID(method, input)
}
func (r Read) uniqueDefaultIndexClassID(method ids.OID, input sqltype.ID) (sqltype.IndexClass, bool) {
	if n := r.defaultClassCount(method, ids.OID(input)); n != 0 {
		if n != 1 {
			return sqltype.IndexClass{}, false
		}
		d, _ := r.DefaultOperatorClass(method, ids.OID(input))
		return r.IndexClass(d.Address().ObjectID)
	}
	if adapted := defaultClassAdaptation(input); adapted != 0 {
		if n := r.defaultClassCount(method, ids.OID(adapted)); n != 0 {
			if n != 1 {
				return sqltype.IndexClass{}, false
			}
			d, _ := r.DefaultOperatorClass(method, ids.OID(adapted))
			return r.IndexClass(d.Address().ObjectID)
		}
	}
	if m, ok := r.AccessMethod(method); ok && m.Native == GistAccessMethod {
		for _, polymorphic := range [...]ids.OID{3831, 4537} {
			if d, ok := r.DefaultOperatorClass(method, polymorphic); ok {
				if class, ok := r.IndexClass(d.Address().ObjectID); ok && class.AcceptsID(input) {
					return class, true
				}
			}
		}
	}
	return sqltype.IndexClass{}, false
}
func defaultClassAdaptation(input sqltype.ID) sqltype.ID {
	switch input {
	case sqltype.VarChar, sqltype.PgNodeTree:
		return sqltype.Text
	case sqltype.Cidr:
		return sqltype.Inet
	case sqltype.Regclass, sqltype.Regtype, sqltype.Regproc, sqltype.Regprocedure, sqltype.Regrole, sqltype.Regoper, sqltype.Regoperator, sqltype.Regcollation, sqltype.Regnamespace, sqltype.Regconfig, sqltype.Regdictionary:
		return sqltype.Oid
	}
	return 0
}

// DefaultClassAmbiguity retains the exact (domain-flattened) input identity.
// Catalog lookup has no executing search path; its diagnostic is a fallback
// until an authority-bearing caller presents it with the canonical type namer.
type DefaultClassAmbiguity struct {
	Input      sqltype.ID
	diagnostic *sqlstate.Error
}

func (e *DefaultClassAmbiguity) Error() string    { return e.diagnostic.Error() }
func (e *DefaultClassAmbiguity) SQLState() string { return e.diagnostic.Code }
func (e *DefaultClassAmbiguity) Unwrap() error    { return e.diagnostic }

// RenderDefaultClassAmbiguity changes only the displayed primary, preserving
// the typed cause, any diagnostic decorations and the retained original error.
func RenderDefaultClassAmbiguity(err error, name func(sqltype.ID) string) error {
	if err == nil {
		return nil
	}
	var ambiguity *DefaultClassAmbiguity
	if !errors.As(err, &ambiguity) {
		return err
	}
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) {
		return err
	}
	copy := *diagnostic
	copy.Message = "there are multiple default operator classes for data type " + name(ambiguity.Input)
	return sqlstate.WithDiagnostic(err, &copy)
}

func (r Read) defaultClassAmbiguity(input sqltype.ID) error {
	name := "unknown"
	if typ, ok := r.LookupType(input); ok {
		name = typ.DiagnosticName()
	}
	return &DefaultClassAmbiguity{Input: input, diagnostic: sqlError("42710", "there are multiple default operator classes for data type "+name).(*sqlstate.Error)}
}
