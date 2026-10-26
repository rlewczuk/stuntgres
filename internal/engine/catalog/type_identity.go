package catalog

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqltype"
)

// SamePreparedTypeDefinition reports whether held analysis may survive the
// revision of old to current: a revised non-domain type keeps it only when its
// executable definition and authority are unchanged. Names, namespace and
// presentation revisions are not executable signatures. Child types have their
// own dependency entries in the same analysis cell; domains keep their existing
// domain stamps.
func SamePreparedTypeDefinition(old, current *Type) bool {
	if old == nil || current == nil || old.Address() != current.Address() || old.Owner != current.Owner || old.Extension != current.Extension || old.Array != current.Array || old.OwningRelation != current.OwningRelation || old.Dimensions != current.Dimensions || !authorization.EqualACL(old.ACL, current.ACL) {
		return false
	}
	a, b := old.SQLType, current.SQLType
	if a == b {
		return true
	}
	if a.Concrete() != b.Concrete() || a.IsDomain() != b.IsDomain() || a.IsArray() != b.IsArray() || a.IsEnum() != b.IsEnum() || a.IsComposite() != b.IsComposite() {
		return false
	}
	switch {
	case a.IsDomain():
		return false // Never replace domain executable-stamp policy.
	case a.IsArray():
		return a.Element().ID() == b.Element().ID()
	case a.IsEnum():
		if a.EnumMemberCount() != b.EnumMemberCount() {
			return false
		}
		for i := range a.EnumMemberCount() {
			left, _ := a.EnumMemberAt(i)
			right, _ := b.EnumMemberAt(i)
			if left != right {
				return false
			}
		}
		return true
	case a.IsComposite():
		if a.CompositeLen() != b.CompositeLen() {
			return false
		}
		for i := range a.CompositeLen() {
			left, right := a.CompositeMember(i), b.CompositeMember(i)
			if left.Attribute != right.Attribute || left.Name != right.Name || !sameLogicalUsage(left.Usage, right.Usage) {
				return false
			}
		}
		return true
	case a.IsCube():
		return b.IsCube()
	case a.IsHstore():
		return b.IsHstore()
	case a.IsSeg():
		return b.IsSeg()
	case a.IsQueryInt():
		return b.IsQueryInt()
	case a.IsLquery():
		return b.IsLquery()
	case a.IsLtxtquery():
		return b.IsLtxtquery()
	case a.IsLtree():
		return b.IsLtree()
	case a.IsCitext():
		return b.IsCitext()
	case a.IsEan13():
		return b.IsEan13()
	case a.IsIsbn():
		return b.IsIsbn()
	case a.IsIsbn13():
		return b.IsIsbn13()
	case a.IsIsmn():
		return b.IsIsmn()
	case a.IsIsmn13():
		return b.IsIsmn13()
	case a.IsIssn():
		return b.IsIssn()
	case a.IsIssn13():
		return b.IsIssn13()
	case a.IsUpc():
		return b.IsUpc()
	}
	return false
}

func sameLogicalUsage(a, b sqltype.Usage) bool {
	if a.Type == nil || b.Type == nil || a.Type.ID() != b.Type.ID() {
		return false
	}
	a.Type, b.Type = nil, nil
	return a == b
}
