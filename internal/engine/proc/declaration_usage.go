package proc

import "stuntgres/internal/sqltype"

// DeclarationUsage admits signature identities without turning them into datums.
func DeclarationUsage(u sqltype.Usage) bool {
	return u.Valid() || u.Type != nil && sqltype.IsPseudo(u.Type.ID()) && u.Typmod == -1 && u.Collation == (sqltype.CollationRef{})
}
