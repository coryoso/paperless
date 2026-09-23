package classify

import (
	"encoding/json"
	"strings"
)

// Small-context providers can shorten a block projection without cutting JSON
// syntax or UTF-8. Callers must mark the document for review when shortened.
func boundedBlockJSON(text string, limit int) (string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(text), "[") {
		return "", false
	}
	var blocks []struct {
		ID      int    `json:"id"`
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	if json.Unmarshal([]byte(text), &blocks) != nil {
		return "", false
	}
	encode := func() string { data, _ := json.Marshal(blocks); return string(data) }
	for len([]rune(encode())) > limit && len(blocks) > 1 {
		// Preserve both the opening and closing sections while dropping the middle.
		i := len(blocks) * 3 / 4
		if i == len(blocks)-1 {
			i = len(blocks) / 2
		}
		blocks = append(blocks[:i], blocks[i+1:]...)
	}
	for len(blocks) > 0 && len([]rune(encode())) > limit {
		runes := []rune(blocks[0].Content)
		if len(runes) == 0 {
			blocks = nil
			break
		}
		keep := max(0, len(runes)-(len([]rune(encode()))-limit))
		if keep >= len(runes) {
			keep = len(runes) - 1
		}
		blocks[0].Content = string(runes[:keep])
	}
	return encode(), true
}
