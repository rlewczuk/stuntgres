package engine

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
	return regobj.Authority{View: auth.View, Environment: auth, Definitions: auth.definition}
}

// inputCapture reads the session's database, token and TEMP namespace from
// auth, then drops the session and settings so the captured authority never
// reads live session state.
func inputCapture(auth AuthorizationView, path []ids.ObjectAddress) regobj.InputCapture {
	c := regobj.InputCapture{Path: path, Temporary: temporaryNamespace(auth)}
	if auth.session != nil {
		c.Database, c.Token = auth.session.DatabaseName, auth.session.SessionToken
	}
	auth.session, auth.settings = nil, nil
	c.Authority = inputAuthority(auth)
	return c
}

func (e *Engine) captureRegclassInput(auth AuthorizationView) (sqltype.RegclassResolver, error) {
	path, err := e.referencePath(auth)
	if err != nil {
		return nil, err
	}
	return regobj.NewClassInput(inputCapture(auth, path)), nil
}

func (e *Engine) captureRegtypeInput(auth AuthorizationView, op *session.Operation) (regobj.TypeInput, error) {
	path, err := e.lookupPath(auth, analyze.TypeLookup)
	if err != nil {
		return regobj.TypeInput{}, err
	}
	return regobj.NewTypeInput(inputCapture(auth, path), op), nil
}

func (e *Engine) captureOperatorInput(auth AuthorizationView, op *session.Operation) (regobj.OperatorInput, error) {
	types, err := e.captureRegtypeInput(auth, op)
	if err != nil {
		return regobj.OperatorInput{}, err
	}
	path, err := e.lookupPath(auth, analyze.OperatorLookup)
	if err != nil {
		return regobj.OperatorInput{}, err
	}
	return regobj.NewOperatorInput(types, path), nil
}

// captureRoutineInput uses the type input's authority; its function path
// excludes temporary namespaces.
func (e *Engine) captureRoutineInput(auth AuthorizationView, types regobj.TypeInput) (regobj.RoutineInput, error) {
	path, err := e.lookupPath(auth, analyze.FunctionLookup)
	if err != nil {
		return regobj.RoutineInput{}, err
	}
	return regobj.NewRoutineInput(types, path), nil
}

func (e *Engine) captureRegTSInput(auth AuthorizationView) (regobj.TextSearchInput, error) {
	path, err := e.lookupPath(auth, analyze.OperatorLookup) // Text search, like operators, excludes TEMP.
	if err != nil {
		return regobj.TextSearchInput{}, err
	}
	return regobj.NewTextSearchInput(inputCapture(auth, path)), nil
}

func (e *Engine) captureRegObjectInput(auth AuthorizationView) (regobj.ObjectInput, error) {
	path, err := e.lookupPath(auth, analyze.CollationLookup)
	if err != nil {
		return regobj.ObjectInput{}, err
	}
	return captureRegObjectsOnPath(auth, path), nil
}

func captureRegObjectsOnPath(auth AuthorizationView, path []ids.ObjectAddress) regobj.ObjectInput {
	return regobj.NewObjectInput(inputCapture(auth, path))
}
