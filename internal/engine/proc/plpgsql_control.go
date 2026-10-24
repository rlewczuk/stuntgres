package proc

import (
	"fmt"

	"stuntgres/internal/parser"
)

// Lexical scopes also carry their non-owning control tokens. Synthetic variable
// overlays have no token and cannot become transfer targets.
func resolvePLControl(raw *parser.PLTransfer, pos parser.Position, scopes []*PLBlock) (*PLControl, error) {
	for i := len(scopes) - 1; i >= 0; i-- {
		block := scopes[i]
		if block.Control == nil {
			continue
		}
		if raw.Label.Text == "" {
			if block.Control.Loop {
				return block.Control, nil
			}
			continue
		}
		if block.Label != raw.Label.Text {
			continue
		}
		if raw.Continue && !block.Control.Loop {
			return nil, parser.PLPositionError("42601", fmt.Sprintf(`block label "%s" cannot be used in CONTINUE`, raw.Label.Text), raw.Label.Pos)
		}
		return block.Control, nil
	}
	if raw.Label.Text != "" {
		return nil, parser.PLPositionError("42601", fmt.Sprintf(`there is no label "%s" attached to any block or loop enclosing this statement`, raw.Label.Text), raw.Label.Pos)
	}
	message := "EXIT cannot be used outside a loop, unless it has a label"
	if raw.Continue {
		message = "CONTINUE cannot be used outside a loop"
	}
	return nil, parser.PLPositionError("42601", message, pos)
}
