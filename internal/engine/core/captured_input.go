package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

// inputAuthority is the read authority a captured resolver checks names with:
// auth's catalog and roles, auth itself as the environment, and its cooking
// definition overlay.
func inputAuthority(auth AuthorizationView) regobj.Authority {
	return regobj.Authority{View: auth.View, Environment: auth, Definitions: auth.Definition}
}

// inputCapture reads the session's database, token and TEMP namespace from
// auth, then drops the session and settings so the captured authority never
// reads live session state.
func inputCapture(auth AuthorizationView, path []ids.ObjectAddress) regobj.InputCapture {
	c := regobj.InputCapture{Path: path, Temporary: temporaryNamespace(auth)}
	if auth.Session != nil {
		c.Database, c.Token = auth.Session.DatabaseName, auth.Session.SessionToken
	}
	auth.Session, auth.settings = nil, nil
	c.Authority = inputAuthority(auth)
	return c
}

func captureRegclassInput(auth AuthorizationView) (sqltype.RegclassResolver, error) {
	path, err := ReferencePath(auth)
	if err != nil {
		return nil, err
	}
	return regobj.NewClassInput(inputCapture(auth, path)), nil
}

func CaptureRegtypeInput(auth AuthorizationView, op *session.Operation) (regobj.TypeInput, error) {
	path, err := LookupPath(auth, analyze.TypeLookup)
	if err != nil {
		return regobj.TypeInput{}, err
	}
	return regobj.NewTypeInput(inputCapture(auth, path), op), nil
}

func captureOperatorInput(auth AuthorizationView, op *session.Operation) (regobj.OperatorInput, error) {
	types, err := CaptureRegtypeInput(auth, op)
	if err != nil {
		return regobj.OperatorInput{}, err
	}
	path, err := LookupPath(auth, analyze.OperatorLookup)
	if err != nil {
		return regobj.OperatorInput{}, err
	}
	return regobj.NewOperatorInput(types, path), nil
}

// CaptureRoutineInput uses the type input's authority; its function path
// excludes temporary namespaces.
func CaptureRoutineInput(auth AuthorizationView, types regobj.TypeInput) (regobj.RoutineInput, error) {
	path, err := LookupPath(auth, analyze.FunctionLookup)
	if err != nil {
		return regobj.RoutineInput{}, err
	}
	return regobj.NewRoutineInput(types, path), nil
}

func CaptureRegTSInput(auth AuthorizationView) (regobj.TextSearchInput, error) {
	path, err := LookupPath(auth, analyze.OperatorLookup) // Text search, like operators, excludes TEMP.
	if err != nil {
		return regobj.TextSearchInput{}, err
	}
	return regobj.NewTextSearchInput(inputCapture(auth, path)), nil
}

func captureRegObjectInput(auth AuthorizationView) (regobj.ObjectInput, error) {
	path, err := LookupPath(auth, analyze.CollationLookup)
	if err != nil {
		return regobj.ObjectInput{}, err
	}
	return captureRegObjectsOnPath(auth, path), nil
}

func captureRegObjectsOnPath(auth AuthorizationView, path []ids.ObjectAddress) regobj.ObjectInput {
	return regobj.NewObjectInput(inputCapture(auth, path))
}
