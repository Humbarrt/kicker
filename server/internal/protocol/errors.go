package protocol

import "fmt"

// ErrCode is an `err` code (either direction, spec 01 §8).
type ErrCode string

const (
	ErrBadJSON          ErrCode = "bad_json"
	ErrTooLong          ErrCode = "too_long"
	ErrUnknownType      ErrCode = "unknown_type"
	ErrUnknownInstance  ErrCode = "unknown_instance"
	ErrUnknownEvent     ErrCode = "unknown_event"
	ErrSchema           ErrCode = "schema"
	ErrProtoUnsupported ErrCode = "proto_unsupported"
	ErrDuplicateUID     ErrCode = "duplicate_uid"
)

// NackCode is a `nack` code sent by a module (spec 01 §8).
type NackCode string

const (
	NackUnknownCommand NackCode = "unknown_command"
	NackBadArgs        NackCode = "bad_args"
	NackBusy           NackCode = "busy"
	NackHWFault        NackCode = "hw_fault"
	NackNotReady       NackCode = "not_ready"
)

// Error is a protocol error. It maps 1:1 to an `err` message, so the server can
// answer bad input and the problem shows up in the module's serial monitor too.
type Error struct {
	Code ErrCode
	Msg  string
	Ref  *uint32 // id or n of the message the error refers to, if known
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Msg) }

// Message converts the error into an `err` message for the wire.
func (e *Error) Message() *Err {
	return &Err{T: TErr, Code: e.Code, Msg: e.Msg, Ref: e.Ref}
}

func errf(code ErrCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}
