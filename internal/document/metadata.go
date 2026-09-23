package document

import (
	"regexp"
	"strings"
	"unicode"
)

const MetadataVersion = 2

// Metadata is a stable presentation/identity projection. OCR blocks and extracted
// parties remain untouched, so normalization never erases source evidence.
type Metadata struct {
	Fields    []Group  `json:"fields"`
	Version   int      `json:"version"`
	Date      string   `json:"date"`
	Subject   string   `json:"subject"`
	Sender    Identity `json:"sender"`
	Recipient Identity `json:"recipient"`
}
type Identity struct {
	PrimaryAddress   int             `json:"primary_address"`
	AddressSelection string          `json:"address_selection"`
	Names            []string        `json:"names"`
	Addresses        []PostalAddress `json:"addresses"`
	Phones           []string        `json:"phones"`
	Faxes            []string        `json:"faxes"`
	Emails           []string        `json:"emails"`
	Websites         []string        `json:"websites"`
	SourceIDs        []int           `json:"source_block_ids"`
	ProfileID        int64           `json:"profile_id,omitempty"`
	Origin           string          `json:"origin"`
}
type PostalAddress struct {
	Lines       []string `json:"lines"`
	StreetName  string   `json:"street_name"`
	HouseNumber string   `json:"house_number"`
	PostalCode  string   `json:"postal_code"`
	City        string   `json:"city"`
}

func NormalizeMetadata(u *Unified) *Metadata {
	fields := []Group{}
	for _, g := range u.Groups {
		if g.Type != "text" {
			fields = append(fields, g)
		}
	}
	return &Metadata{Fields: fields, Version: MetadataVersion, Date: u.Date, Subject: strings.Join(strings.Fields(u.Subject), " "), Sender: NormalizeIdentity(u.Sender, false), Recipient: NormalizeIdentity(u.Recipient, true)}
}
func NormalizeIdentity(p Party, person bool) Identity {
	out := Identity{Names: []string{}, Addresses: []PostalAddress{}, Phones: cleanValues(p.Phones), Faxes: cleanValues(p.Faxes), Emails: cleanValues(p.Emails), Websites: cleanValues(p.Websites), SourceIDs: append([]int{}, p.SourceIDs...), Origin: "extracted"}
	for _, name := range p.Names {
		name = NormalizeName(name, person)
		if name != "" {
			out.Names = append(out.Names, name)
		}
	}
	// Preserve source lines alongside explicitly parsed postal components.
	// Adjacent street/postcode fragments belong to the same postal address.
	for _, address := range p.Addresses {
		lines := []string{}
		address = inlinePostal.ReplaceAllString(address, "$1\n$2")
		for _, line := range strings.FieldsFunc(address, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' }) {
			line = normalizeCase(strings.Join(strings.Fields(line), " "), false)
			if line != "" {
				lines = append(lines, line)
			}
		}
		if len(lines) == 0 {
			continue
		}
		if len(out.Addresses) > 0 && startsPostalLine(lines[0]) {
			last := &out.Addresses[len(out.Addresses)-1]
			if len(last.Lines) == 1 && !startsPostalLine(last.Lines[0]) {
				last.Lines = append(last.Lines, lines...)
				continue
			}
		}
		out.Addresses = append(out.Addresses, PostalAddress{Lines: lines})
	}
	best := -1
	for i := range out.Addresses {
		out.Addresses[i] = ParsePostalAddress(out.Addresses[i].Lines)
		a := out.Addresses[i]
		score := 0
		for _, value := range []string{a.StreetName, a.HouseNumber, a.PostalCode, a.City} {
			if value != "" {
				score++
			}
		}
		if score > best {
			best = score
			out.PrimaryAddress = i
		}
	}
	out.AddressSelection = "Most complete address; first occurrence breaks ties. Alternatives retained."
	return out
}

var inlinePostal = regexp.MustCompile(`(\S)\s+([0-9]{4,6}\s+[\p{L}].*)$`)
var postalLine = regexp.MustCompile(`^([0-9]{4,6})\s+(.+)$`)
var streetLine = regexp.MustCompile(`^(.+?[\p{L}.])\s*([0-9]+\s*[\p{L}]?(?:\s*[-/]\s*[0-9]+\s*[\p{L}]?)?)$`)

// Missing or ambiguous components stay empty. Unparsed lines remain evidence.
func ParsePostalAddress(lines []string) PostalAddress {
	a := PostalAddress{Lines: append([]string{}, lines...)}
	for _, line := range lines {
		if m := postalLine.FindStringSubmatch(line); m != nil && a.PostalCode == "" {
			a.PostalCode, a.City = m[1], m[2]
		} else if m := streetLine.FindStringSubmatch(line); m != nil && a.StreetName == "" {
			a.StreetName, a.HouseNumber = m[1], strings.TrimSpace(m[2])
		}
	}
	return a
}
func startsPostalLine(s string) bool {
	fields := strings.Fields(s)
	if len(fields) < 2 || len(fields[0]) < 4 || len(fields[0]) > 6 {
		return false
	}
	for _, r := range fields[0] {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
func cleanValues(values []string) []string {
	out := []string{}
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
func NormalizeName(name string, person bool) string {
	fields := strings.Fields(name)
	for len(fields) > 0 {
		first := strings.ToLower(strings.Trim(fields[0], ".,"))
		if first != "herr" && first != "herrn" && first != "frau" && first != "mr" && first != "mrs" && first != "ms" {
			break
		}
		fields = fields[1:]
	}
	return normalizeCase(strings.Join(fields, " "), person)
}
func normalizeCase(value string, person bool) string {
	// Mixed-case spelling is authoritative (e.g. McDonald, iRobot). Transform
	// shouting-case words only, preserving short organization acronyms/initials.
	words := strings.Fields(value)
	for i, word := range words {
		if word != strings.ToUpper(word) || word == strings.ToLower(word) {
			continue
		}
		letters := 0
		for _, r := range word {
			if unicode.IsLetter(r) {
				letters++
			}
		}
		if (!person && letters <= 3) || (letters == 1) {
			continue
		}
		boundary := true
		words[i] = strings.Map(func(r rune) rune {
			if !unicode.IsLetter(r) {
				boundary = true
				return r
			}
			if boundary {
				boundary = false
				return unicode.ToUpper(r)
			}
			return unicode.ToLower(r)
		}, word)
	}
	return strings.Join(words, " ")
}
