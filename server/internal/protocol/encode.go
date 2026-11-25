package protocol

import "encoding/json"

// Encode marshals a message into one line including the trailing newline.
// It fails with ErrTooLong if the line would exceed MaxLine; the caller must split
// the payload (e.g. send config as several partial `cfg` messages, spec 01 §4).
func Encode(m Message) ([]byte, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, errf(ErrSchema, "encode %s: %v", m.Type(), err)
	}
	if len(b)+1 > MaxLine {
		return nil, errf(ErrTooLong, "%s message is %d bytes, max %d", m.Type(), len(b)+1, MaxLine)
	}
	return append(b, '\n'), nil
}

// MustPayload marshals a payload for the `d` field. It panics on error, so only use it
// with payload types from this package.
func MustPayload(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
