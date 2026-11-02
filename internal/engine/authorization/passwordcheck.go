package authorization

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"
)

// CheckRolePassword runs the stock C-locale hook before empty clearing or
// encryption. No catalog, session or settings lock may be held by its caller.
func CheckRolePassword(ctx context.Context, name, password string, minimum int32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if roleMD5(password) {
		sum := md5.Sum([]byte(name + name))
		if hmac.Equal([]byte(password[3:]), []byte(hex.EncodeToString(sum[:]))) {
			return roleDiagnostic("22023", "password must not equal user name", "")
		}
		return nil
	}
	if secret, ok := parseRoleSCRAM(password); ok {
		salted, err := roleSCRAMSaltedPassword(ctx, roleSASLprep(name), secret.salt, secret.iterations)
		if err != nil {
			return err
		}
		if hmac.Equal(roleHMAC(salted, "Server Key"), secret.server) {
			return roleDiagnostic("22023", "password must not equal user name", "")
		}
		return nil
	}
	if int64(len(password)) < int64(minimum) {
		return roleDiagnostic("22023", "password is too short", fmt.Sprintf(`password must be at least "passwordcheck.min_password_length" (%d) bytes long`, minimum))
	}
	if strings.Contains(password, name) {
		return roleDiagnostic("22023", "password must not contain user name", "")
	}
	letter, other := false, false
	for i := range len(password) {
		c := password[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			letter = true
		} else {
			other = true
		}
	}
	if !letter || !other {
		return roleDiagnostic("22023", "password must contain both letters and nonletters", "")
	}
	return nil
}
