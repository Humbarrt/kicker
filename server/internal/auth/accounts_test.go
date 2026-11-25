package auth

import (
	"strings"
	"testing"
)

func TestWordsAndCodes(t *testing.T) {
	if len(Words) != 2048 || Words[0] != "abandon" || Words[2047] != "zoo" {
		t.Fatalf("word list: %d words", len(Words))
	}
	k, err := RandomWords(12)
	if err != nil || len(strings.Fields(k)) != 12 {
		t.Fatalf("words: %q %v", k, err)
	}
	if got := normalizeWords("1. Abandon  2) ABILITY,\n3. able"); got != "abandon ability able" {
		t.Errorf("normalize: %q", got)
	}
	seen := map[string]bool{}
	for range 200 {
		c, err := newCode()
		if err != nil || len(c) != 9 || c[4] != '-' || strings.ContainsAny(c, "01OIL") {
			t.Fatalf("code %q: %v", c, err)
		}
		seen[c] = true
	}
	if len(seen) < 199 {
		t.Error("codes repeat")
	}
	if codeHash("abcd efgh") != codeHash("ABCD-EFGH") {
		t.Error("codes ignore case, spaces and dashes")
	}
}
