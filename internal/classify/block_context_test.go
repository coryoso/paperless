package classify

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestShortenedBlockContextRemainsJSON(t *testing.T) {
	input := `[{"id":1,"type":"subject","content":"Invitation"},{"id":2,"type":"text","content":"` + strings.Repeat("ü", 2000) + `"},{"id":3,"type":"sender","content":"Library"}]`
	got, ok := boundedBlockJSON(input, 150)
	if !ok || !json.Valid([]byte(got)) || !utf8.ValidString(got) || len([]rune(got)) > 150 || !strings.Contains(got, "Invitation") {
		t.Fatal(got)
	}
	single := `[{"id":9,"type":"text","content":"` + strings.Repeat("ü", 2000) + `"}]`
	got, ok = boundedBlockJSON(single, 150)
	if !ok || !json.Valid([]byte(got)) || len([]rune(got)) > 150 {
		t.Fatal(got)
	}
}
