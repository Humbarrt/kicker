package protocol

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func readAll(t *testing.T, input string) []Line {
	t.Helper()
	r := NewReader(strings.NewReader(input))
	var lines []Line
	for {
		l, err := r.Next()
		if errors.Is(err, io.EOF) {
			return lines
		}
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, l)
	}
}

func TestReaderKinds(t *testing.T) {
	lines := readAll(t, "{\"t\":\"pong\"}\nhello debug\r\n\n   \n  {\"t\":\"log\"}\r\n")
	want := []struct {
		kind LineKind
		data string
	}{
		{LineJSON, `{"t":"pong"}`},
		{LineRaw, "hello debug"},
		{LineJSON, `{"t":"log"}`},
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d: %+v", len(lines), len(want), lines)
	}
	for i, w := range want {
		if lines[i].Kind != w.kind || string(lines[i].Data) != w.data {
			t.Errorf("line %d: got %v %q, want %v %q", i, lines[i].Kind, lines[i].Data, w.kind, w.data)
		}
	}
}

func TestReaderTooLongResyncs(t *testing.T) {
	exact := "{" + strings.Repeat("a", MaxLine-3) + "}" // MaxLine-1 bytes + newline = MaxLine
	long := strings.Repeat("x", 5000)
	lines := readAll(t, exact+"\n"+long+"\n"+`{"t":"pong"}`+"\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if lines[0].Kind != LineJSON || len(lines[0].Data) != MaxLine-1 {
		t.Errorf("line of exactly MaxLine must be accepted, got %v len %d", lines[0].Kind, len(lines[0].Data))
	}
	if lines[1].Kind != LineTooLong || len(lines[1].Data) > tooLongPreview {
		t.Errorf("over-long line: got %v len %d", lines[1].Kind, len(lines[1].Data))
	}
	if lines[2].Kind != LineJSON || string(lines[2].Data) != `{"t":"pong"}` {
		t.Errorf("stream must resync after an over-long line, got %v %q", lines[2].Kind, lines[2].Data)
	}
}

func TestReaderOneByteOverIsTooLong(t *testing.T) {
	over := "{" + strings.Repeat("a", MaxLine-2) + "}"
	lines := readAll(t, over+"\n")
	if len(lines) != 1 || lines[0].Kind != LineTooLong {
		t.Fatalf("got %+v, want one too_long line", lines)
	}
}

func TestReaderPartialLineAtEOF(t *testing.T) {
	lines := readAll(t, "{\"t\":\"pong\"}\n{\"t\":\"po")
	if len(lines) != 1 {
		t.Fatalf("unterminated tail must be dropped, got %d lines", len(lines))
	}
}

func FuzzReader(f *testing.F) {
	f.Add("{\"t\":\"pong\"}\nraw\r\n")
	f.Add(strings.Repeat("y", 3000) + "\n{}\n")
	f.Fuzz(func(t *testing.T, input string) {
		r := NewReader(strings.NewReader(input))
		for {
			l, err := r.Next()
			if err != nil {
				return
			}
			if len(l.Data) > MaxLine-1 {
				t.Fatalf("line longer than MaxLine-1: %d", len(l.Data))
			}
		}
	})
}
