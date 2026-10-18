package control

import (
	"encoding/json"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

// operationRecord retains private execution inputs in the existing flat JSON
// journal. Only its public Operation is returned by service query methods.
type operationRecord struct {
	compute.Operation
	Payload json.RawMessage `json:"payload,omitempty"`
}
