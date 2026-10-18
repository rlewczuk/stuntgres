package catalog

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

type PolicyRole struct {
	Public bool
	Ref    authorization.PrincipalRef
}

type PolicyCommand string

const (
	PolicyAll    PolicyCommand = "all"
	PolicySelect PolicyCommand = "select"
	PolicyInsert PolicyCommand = "insert"
	PolicyUpdate PolicyCommand = "update"
	PolicyDelete PolicyCommand = "delete"
)

type PolicyDefinition struct {
	OID          ids.OID
	Name         string
	Permissive   bool
	Command      PolicyCommand
	Roles        []PolicyRole
	Using, Check PolicyPayload
}
