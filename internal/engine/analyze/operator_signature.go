package analyze

import "stuntgres/internal/engine/ids"

// OperatorSignature is an operator's operand type pair. Operator candidate
// lists keep the first visible operator of each signature.
type OperatorSignature struct{ Left, Right ids.OID }
