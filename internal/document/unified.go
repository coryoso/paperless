package document

import (
	"strings"
	"unicode"
)

// Unified is persisted with the spatial blocks. Entries keep every contributing
// block ID, including redundant occurrences removed from their reading text.
type Unified struct {
	Normalized       *Metadata `json:"normalized,omitempty"`
	Date             string    `json:"date"`
	DateSourceIDs    []int     `json:"date_source_ids"`
	Version          int       `json:"version"`
	Groups           []Group   `json:"groups"`
	Sender           Party     `json:"sender"`
	Recipient        Party     `json:"recipient"`
	Subject          string    `json:"subject"`
	SubjectSourceIDs []int     `json:"subject_source_ids"`
	ExtractionStatus string    `json:"extraction_status"`
}
type Party struct {
	Names     []string `json:"names"`
	Addresses []string `json:"addresses"`
	Phones    []string `json:"phones"`
	Faxes     []string `json:"faxes"`
	Emails    []string `json:"emails"`
	Websites  []string `json:"websites"`
	SourceIDs []int    `json:"source_block_ids"`
}
type Group struct {
	Type    string  `json:"type"`
	Entries []Entry `json:"entries"`
}
type Entry struct {
	Content   string `json:"content"`
	SourceIDs []int  `json:"source_block_ids"`
}

func EmptyParty() Party {
	return Party{[]string{}, []string{}, []string{}, []string{}, []string{}, []string{}, []int{}}
}
func Normalize(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
}

// Keep contact and reference punctuation: a name fragment appearing inside a
// domain is not a repeated postal identity. Only whitespace and prose delimiters
// are ignored for containment; exact body/payment comparisons keep punctuation.
func mergeText(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r == ',' || r == ';' || r == '(' || r == ')' {
			return ' '
		}
		return unicode.ToLower(r)
	}, s)), " ")
}
func containsText(a, b string) bool {
	a, b = mergeText(a), mergeText(b)
	return b != "" && strings.Contains(" "+a+" ", " "+b+" ")
}

// Consolidate only removes exact/contained metadata repetitions. Body paragraphs
// and tables retain their boundaries and order; conflicting values remain visible.
func Consolidate(blocks []Block) *Unified {
	u := &Unified{Version: 1, DateSourceIDs: []int{}, Groups: []Group{}, Sender: EmptyParty(), Recipient: EmptyParty(), SubjectSourceIDs: []int{}, ExtractionStatus: "pending"}
	for _, b := range blocks {
		if strings.TrimSpace(b.Content) == "" {
			continue
		}
		typ := b.Type
		if typ == "" {
			typ = "text"
		}
		gi := -1
		for i, g := range u.Groups {
			if g.Type == typ {
				gi = i
				break
			}
		}
		if gi < 0 {
			u.Groups = append(u.Groups, Group{Type: typ, Entries: []Entry{}})
			gi = len(u.Groups) - 1
		}
		g := &u.Groups[gi]
		merged := false
		for i := range g.Entries {
			e := &g.Entries[i]
			same := strings.Join(strings.Fields(e.Content), " ") == strings.Join(strings.Fields(b.Content), " ")
			metadata := typ == "sender" || typ == "recipient" || typ == "reference" || typ == "date" || typ == "subject"
			if same || (metadata && containsText(e.Content, b.Content)) {
				e.SourceIDs = append(e.SourceIDs, b.ID)
				merged = true
				break
			}
			if metadata && containsText(b.Content, e.Content) {
				e.Content = b.Content
				e.SourceIDs = append(e.SourceIDs, b.ID)
				merged = true
				break
			}
		}
		if !merged {
			g.Entries = append(g.Entries, Entry{Content: b.Content, SourceIDs: []int{b.ID}})
		}
	}
	// A longer later occurrence can subsume several earlier fragments.
	for gi := range u.Groups {
		g := &u.Groups[gi]
		if g.Type == "text" || g.Type == "payment" {
			continue
		}
		for i := 0; i < len(g.Entries); i++ {
			for j := i + 1; j < len(g.Entries); {
				a, b := &g.Entries[i], g.Entries[j]
				if containsText(a.Content, b.Content) {
					a.SourceIDs = append(a.SourceIDs, b.SourceIDs...)
					g.Entries = append(g.Entries[:j], g.Entries[j+1:]...)
				} else if containsText(b.Content, a.Content) {
					a.Content = b.Content
					a.SourceIDs = append(a.SourceIDs, b.SourceIDs...)
					g.Entries = append(g.Entries[:j], g.Entries[j+1:]...)
				} else {
					j++
				}
			}
		}
	}
	return u
}
