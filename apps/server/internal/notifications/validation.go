package notifications

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
)

func identifier(value string, required bool, max int) bool {
	if value == "" {
		return !required
	}
	if len(value) > max {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == ':' || c == '-') {
			return false
		}
	}
	return true
}

func opaqueID(value string) bool {
	_, err := hex.DecodeString(value)
	return len(value) == 32 && err == nil
}

func validTarget(t notificationevent.Target) bool {
	if !identifier(t.StoreID, true, 128) {
		return false
	}
	for _, value := range []string{t.SessionID, t.RunID, t.ThreadID, t.TurnID, t.EventID, t.HostID, t.ProjectID, t.TaskID, t.AttemptID} {
		if !identifier(value, false, 128) {
			return false
		}
	}
	switch t.Kind {
	case "codex":
		return t.SessionID != "" && t.RunID != "" && t.ThreadID != "" && t.EventID != "" && t.TaskID == "" && t.AttemptID == "" && (t.HostID == "") == (t.ProjectID == "")
	case "task":
		return t.HostID != "" && t.StoreID == t.HostID && t.ProjectID != "" && t.TaskID != "" && t.AttemptID != "" && t.EventID != "" && t.SessionID == "" && t.RunID == "" && t.ThreadID == "" && t.TurnID == ""
	}
	return false
}

// encoding/json otherwise accepts duplicate object fields and may choose the
// last value. Neither a credential update nor an on-disk identity may be ambiguous.
func uniqueJSON(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 16 {
			return false
		}
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return false
				}
				text, ok := key.(string)
				if !ok {
					return false
				}
				for _, character := range text {
					if character > 127 {
						return false
					}
				}
				text = strings.ToLower(text)
				if seen[text] {
					return false
				}
				seen[text] = true
				if !value(depth + 1) {
					return false
				}
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for decoder.More() {
				if !value(depth + 1) {
					return false
				}
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim(']')
		}
		return false
	}
	if !value(0) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}
