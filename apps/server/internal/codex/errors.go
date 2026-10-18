package codex

// ErrorKind classifies service failures independently of a transport protocol.
type ErrorKind uint8

const (
	Invalid ErrorKind = iota + 1
	Conflict
	Unavailable
)

// Error carries the stable feature code and user-facing reason for a failure.
type Error struct {
	Kind    ErrorKind
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func failure(kind ErrorKind, code, message string) error {
	return &Error{Kind: kind, Code: code, Message: message}
}
