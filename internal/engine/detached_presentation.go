package engine

import (
	"fmt"

	"stuntgres/internal/sqltype"
)

// detachedPresentation formats output when no statement snapshot exists: a
// runtime record without a host, or a scope without a comment snapshot. It is
// the single source of the missing-snapshot diagnostics.
type detachedPresentation struct{}

var _ presentationFormatters = detachedPresentation{}

func (detachedPresentation) FormatRegclass(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing regclass output snapshot")
}
func (detachedPresentation) FormatRegtype(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing regtype output snapshot")
}
func (detachedPresentation) FormatRegproc(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing regproc output snapshot")
}
func (detachedPresentation) FormatRegprocedure(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing regprocedure output snapshot")
}
func (detachedPresentation) FormatRegrole(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing regrole output snapshot")
}
func (detachedPresentation) FormatRegoper(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing operator output snapshot")
}
func (detachedPresentation) FormatRegoperator(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing operator output snapshot")
}
func (detachedPresentation) FormatRegcollation(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing regcollation output snapshot")
}
func (detachedPresentation) FormatRegnamespace(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing regnamespace output snapshot")
}
func (detachedPresentation) FormatRegconfig(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing text search output snapshot")
}
func (detachedPresentation) FormatRegdictionary(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing text search output snapshot")
}
func (detachedPresentation) FormatACLRole(*sqltype.ACLAuthority, uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing ACL output snapshot")
}
