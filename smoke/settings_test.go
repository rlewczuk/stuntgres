package smoke_test

import (
	"errors"
	"fmt"
	"stuntgres"
)

func settingsFullDiagnostic(err error, want stuntgres.Error) error {
	var d *stuntgres.Error
	if !errors.As(err, &d) || *d != want {
		return fmt.Errorf("diagnostic got %#v (%v) want %#v", d, err, want)
	}
	return nil
}
