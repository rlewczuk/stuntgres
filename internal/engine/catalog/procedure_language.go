package catalog

import (
	"fmt"
	"stuntgres/internal/engine/ids"
)

// ProcedureLanguageAddress projects declaration identity, never execution from a
// display label. Host scalar declarations own an authentic language association;
// their Go handler/converter utilities retain the native zero-language sentinel.
func ProcedureLanguageAddress(view Read, d *Procedure) (ids.ObjectAddress, error) {
	if d == nil || !d.Display.Valid() {
		return ids.ObjectAddress{}, fmt.Errorf("stuntgres: invalid routine declaration display")
	}
	if d.HostHandler != nil {
		if err := ValidateHostProcedure(view, d); err != nil {
			return ids.ObjectAddress{}, err
		}
	}
	if d.HostConversion != nil {
		if err := ValidateHostConversion(view, d); err != nil {
			return ids.ObjectAddress{}, err
		}
	}
	address := d.Language
	if d.Host != nil {
		if err := ValidateHostProcedure(view, d); err != nil {
			return ids.ObjectAddress{}, err
		}
		return address, nil
	} else if d.Display.Language == GoRoutineLanguage {
		if address != (ids.ObjectAddress{}) {
			return ids.ObjectAddress{}, fmt.Errorf("stuntgres: native routine has SQL language")
		}
		return address, nil
	} else if address == (ids.ObjectAddress{}) {
		// These are released bootstrap identities, not current SQL labels.
		switch d.Display.Language {
		case InternalRoutineLanguage:
			address = ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: 12}
		case CRoutineLanguage:
			address = ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: 13}
		case SQLRoutineLanguage:
			address = ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: 14}
		}
	}
	language, ok := view.Get(address).(*Language)
	if !ok {
		return ids.ObjectAddress{}, fmt.Errorf("stuntgres: missing routine declaration language")
	}
	if err := (LanguageProvider{}).Validate(view, language); err != nil {
		return ids.ObjectAddress{}, err
	}
	return address, nil
}
