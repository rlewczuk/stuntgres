package expr

import (
	"context"
	"fmt"

	"stuntgres/internal/sqltype"
)

// Source is explicit admission to the closed function-source lane. Admission and
// set cardinality are independent; Composite or Renderable is not admission.
type Source interface {
	Expr
	SourceAdmission() (admitted, returnsSet bool)
}

type ScalarSourceOut interface{ ScalarSourceOutName() string }
type SourceOccurrence interface{ SameSourceOccurrence(Expr) bool }
type SourceOpening interface {
	OpenSource(Context) (SourceCursor, error)
}

// SourceCursor is invocation-private and must not be copied after advancement.
// State, when needed, points to the owner's native cursor or immutable provider
// payload (catalog metadata), never a second adapter. Only that owner interprets
// it. Shared fields support scalar, owned-vector, borrowed-payload and arithmetic
// providers without allocating an interface implementation per open. No analyzed
// node or evaluation context is retained.
type SourceCursor struct {
	// Type is the result descriptor bound at open, retained for typed EOF.
	Type *sqltype.Descriptor
	// Value is a scalar-once value or a borrowed immutable provider payload.
	Value sqltype.Value
	// Values is a collection already completed privately by its provider.
	Values []sqltype.Value
	// Position is the next vector/payload index, or scalar presence index.
	Position int
	// Current, Stop and Step are shared checked arithmetic progression state.
	Current, Stop, Step int64
	// Exhausted prevents further emission; Close also releases all payloads.
	Exhausted bool
	// State is owner-native cursor state or borrowed immutable metadata.
	State   any
	Advance func(context.Context, *SourceCursor) (sqltype.Value, bool, error)
	// Release receives only State so closing an inline cursor does not force
	// the caller-owned handle to escape through an indirect callback.
	Release     func(any)
	initialized bool
}

func NewSourceCursor(s SourceCursor) SourceCursor {
	s.initialized = true
	return s
}

func (s *SourceCursor) Next(ctx context.Context) (sqltype.Value, bool, error) {
	if err := ctx.Err(); err != nil {
		s.Close()
		return sqltype.Value{}, false, err
	}
	if !s.initialized {
		return sqltype.Value{}, false, fmt.Errorf("stuntgres: unopened function set cursor")
	}
	if s.Exhausted {
		s.Close()
		return sqltype.Null(s.Type), false, nil
	}
	value, present, err := s.Advance(ctx, s)
	if err != nil {
		s.Close()
		return sqltype.Value{}, false, err
	}
	if !present {
		s.Close()
		return sqltype.Null(s.Type), false, nil
	}
	return value, true, nil
}

func (s *SourceCursor) Close() {
	if s.Release != nil {
		s.Release(s.State)
		s.Release = nil
	}
	s.State = nil
	s.Values = nil
	s.Value = sqltype.Value{}
	s.Exhausted = true
}

func ScalarSource(value sqltype.Value) SourceCursor {
	return NewSourceCursor(SourceCursor{Type: value.Type(), Value: value, Advance: nextScalarSource})
}

func nextScalarSource(_ context.Context, s *SourceCursor) (sqltype.Value, bool, error) {
	if s.Position != 0 {
		return sqltype.Value{}, false, nil
	}
	s.Position++
	return s.Value, true, nil
}

func OwnedSource(typ *sqltype.Descriptor, values []sqltype.Value) SourceCursor {
	return NewSourceCursor(SourceCursor{Type: typ, Values: values, Advance: nextOwnedSource})
}

func nextOwnedSource(_ context.Context, s *SourceCursor) (sqltype.Value, bool, error) {
	if s.Position == len(s.Values) {
		return sqltype.Value{}, false, nil
	}
	value := s.Values[s.Position]
	s.Position++
	return value, true, nil
}

// SourceTraits queries only explicit typed admission, never scalar evaluation.
func SourceTraits(e Expr) (bool, bool) {
	if source, ok := e.(Source); ok {
		return source.SourceAdmission()
	}
	return false, false
}

func IsSetSource(e Expr) bool {
	_, set := SourceTraits(e)
	return set
}

func AdmitsSource(e Expr) bool {
	admitted, _ := SourceTraits(e)
	return admitted
}

func SourceOutName(e Expr) string {
	if out, ok := e.(ScalarSourceOut); ok {
		return out.ScalarSourceOutName()
	}
	return ""
}
