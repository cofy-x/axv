package hostdir

// ErrorKind classifies directory failures independently of a transport protocol.
type ErrorKind uint8

const (
	InvalidPath ErrorKind = iota + 1
	Unavailable
	NotDirectory
	Unreadable
	HomeUnavailable
)

// Error carries the stable directory code and user-facing reason for a failure.
type Error struct {
	Kind    ErrorKind
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func failure(kind ErrorKind, code, message string) error {
	return &Error{Kind: kind, Code: code, Message: message}
}
