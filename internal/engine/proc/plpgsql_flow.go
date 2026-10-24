package proc

type PLFlowKind uint8

const (
	PLNormal PLFlowKind = iota
	PLReturn
	PLExit
	PLContinue
)

// PLFlow is not a SQL error: exception checkpoints commit normal transfers and
// lexical/cursor owners unwind before the addressed control token consumes it.
type PLFlow struct {
	Kind   PLFlowKind
	Target *PLControl
}
