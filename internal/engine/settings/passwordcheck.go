package settings

import "math"

const PasswordCheckMinimum = "passwordcheck.min_password_length"

// PasswordCheckDefinition returns source-owned startup metadata. Its presence in
// the immutable registry is the authority to run the role password hook.
func PasswordCheckDefinition() Definition {
	return Definition{Name: PasswordCheckMinimum, Description: "Minimum allowed password length.", Category: "Customized Options", Type: Integer, Access: Privileged, Unit: Bytes, BootDefault: "8", IntegerMin: 0, IntegerMax: math.MaxInt32, bootValue: Value{Number: 8}}
}
