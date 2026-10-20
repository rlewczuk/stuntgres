package exec

import (
	"fmt"

	"stuntgres/internal/sqltype"
)

// DetachedPresentation formats output when no statement snapshot exists: a
// runtime record without a host, or a scope without a comment snapshot. It is
// the single source of the missing-snapshot diagnostics.
type DetachedPresentation struct{}

var _ PresentationFormatters = DetachedPresentation{}

func (DetachedPresentation) FormatRegclass(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing regclass output snapshot")
}
func (DetachedPresentation) FormatRegtype(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing regtype output snapshot")
}
func (DetachedPresentation) FormatRegproc(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing regproc output snapshot")
}
func (DetachedPresentation) FormatRegprocedure(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing regprocedure output snapshot")
}
func (DetachedPresentation) FormatRegrole(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing regrole output snapshot")
}
func (DetachedPresentation) FormatRegoper(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing operator output snapshot")
}
func (DetachedPresentation) FormatRegoperator(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing operator output snapshot")
}
func (DetachedPresentation) FormatRegcollation(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing regcollation output snapshot")
}
func (DetachedPresentation) FormatRegnamespace(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing regnamespace output snapshot")
}
func (DetachedPresentation) FormatRegconfig(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing text search output snapshot")
}
func (DetachedPresentation) FormatRegdictionary(uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing text search output snapshot")
}
func (DetachedPresentation) FormatACLRole(*sqltype.ACLAuthority, uint32) (string, error) {
	return "", fmt.Errorf("stuntgres: missing ACL output snapshot")
}
