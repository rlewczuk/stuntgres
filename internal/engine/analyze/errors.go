package analyze

import (
	"errors"
	"strconv"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// InputErrorAt positions a literal input error at pos for the SQLSTATEs
// whose PostgreSQL report carries the literal's position.
func InputErrorAt(err error, pos parser.Position) error {
	var d *sqlstate.Error
	if !errors.As(err, &d) {
		return err
	}
	if d.Code == "54000" || d.Code == "XX000" {
		copy := *d
		if pos.Character > 0 {
			copy.Position = strconv.Itoa(pos.Character)
		}
		return sqlstate.InputDiagnostic(err, &copy)
	}
	if d.Code == "42704" || d.Code == "42622" || d.Code == "55P04" || d.Code == "0A000" || d.Code == "22P02" || d.Code == "22P05" || d.Code == "22000" || d.Code == "22001" || d.Code == "2201B" || d.Code == "2202E" || d.Code == "22003" || d.Code == "22007" || d.Code == "22008" || d.Code == "22009" || d.Code == "22015" || d.Code == "22023" || d.Code == "42601" {
		copy := *d
		if pos.Character > 0 {
			copy.Position = strconv.Itoa(pos.Character)
		}
		return sqlstate.InputDiagnostic(err, &copy)
	}
	return err
}

// RegtypeInputError positions any SQL error from catalog identifier input
// at pos.
func RegtypeInputError(err error, pos parser.Position) error {
	var d *sqlstate.Error
	if errors.As(err, &d) && pos.Character > 0 {
		copied := *d
		copied.Position = strconv.Itoa(pos.Character)
		return sqlstate.InputDiagnostic(err, &copied)
	}
	return err
}
