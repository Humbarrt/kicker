package protocol

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// LineKind classifies a received line (spec 01 §2).
type LineKind int

const (
	// LineJSON starts with '{' and should be decoded as a message.
	LineJSON LineKind = iota
	// LineRaw doesn't start with '{': raw log output (e.g. a stray Serial.println). Never an error.
	LineRaw
	// LineTooLong exceeded MaxLine. Its content was dropped; Data holds the first bytes for the console.
	LineTooLong
)

func (k LineKind) String() string {
	switch k {
	case LineJSON:
		return "json"
	case LineRaw:
		return "raw"
	case LineTooLong:
		return "too_long"
	}
	return "unknown"
}

// Line is one line received from a module, without the trailing newline.
type Line struct {
	Kind LineKind
	Data []byte
}

// Reader splits a byte stream into lines of at most MaxLine bytes.
// Over-long lines are skipped up to the next newline and reported as LineTooLong,
// so one broken line never desynchronises the stream.
type Reader struct {
	br  *bufio.Reader
	max int
}

// NewReader returns a Reader with the protocol's MaxLine.
func NewReader(r io.Reader) *Reader {
	return &Reader{br: bufio.NewReaderSize(r, 4096), max: MaxLine}
}

// tooLongPreview is how many bytes of an over-long line are kept for the module console.
const tooLongPreview = 80

// Next returns the next non-empty line. At the end of the stream it returns io.EOF;
// an unterminated partial line at the end is discarded.
func (r *Reader) Next() (Line, error) {
	var buf []byte
	tooLong := false
	for {
		chunk, err := r.br.ReadSlice('\n')
		if !tooLong {
			buf = append(buf, chunk...)
			// MaxLine includes the newline.
			if len(bytes.TrimRight(buf, "\r\n")) > r.max-1 {
				tooLong = true
				buf = buf[:min(len(buf), tooLongPreview)]
			}
		}
		switch {
		case err == nil:
			// complete line
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return Line{}, err
		}

		if tooLong {
			return Line{Kind: LineTooLong, Data: bytes.TrimRight(buf, "\r\n")}, nil
		}
		data := bytes.TrimRight(buf, "\r\n")
		trimmed := bytes.TrimSpace(data)
		if len(trimmed) == 0 {
			buf = buf[:0]
			continue // ignore empty lines
		}
		kind := LineRaw
		if trimmed[0] == '{' {
			kind = LineJSON
			data = trimmed
		}
		return Line{Kind: kind, Data: data}, nil
	}
}
