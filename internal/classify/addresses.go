package classify

import (
	"regexp"
	"strings"

	"paperless/internal/config"
)

var (
	postalLine    = regexp.MustCompile(`(?i)^(?:[a-z]{1,3}-)?[0-9]{4,5}\s+[\p{L}][\p{L}\s.()/-]*$`)
	postalAddress = regexp.MustCompile(`(?is)^\s*(.+?\s+[0-9]+\s*[a-z]?(?:\s*[-/]\s*[0-9]+[a-z]?)?)\s*[,\n ]+\s*(?:[a-z]{1,3}-)?([0-9]{4,5})\s+([\p{L}][\p{L}\s.()/-]*)\s*$`)
)

type addressRecipient struct {
	Name, Type, Address, Evidence string
	Score                         int
	Ambiguous                     bool
	Profiles                      []config.RecipientProfile
}

func addressKey(address string) string {
	match := postalAddress.FindStringSubmatch(strings.TrimSpace(address))
	if match == nil || !hasLetter(match[1]) {
		return ""
	}
	street := strings.ReplaceAll(Slug(match[1]), "strasse", "str")
	return strings.ReplaceAll(street, "-", "") + ":" + match[2]
}

func ValidRecipientAddress(address string) bool { return addressKey(address) != "" }

func profileMatchesName(profile config.RecipientProfile, name string) bool {
	for _, alias := range append([]string{profile.Name}, profile.Aliases...) {
		if compactName(alias) != "" && compactName(alias) == compactName(name) {
			return true
		}
	}
	return false
}

func matchesAddress(addresses []string, key string) bool {
	for _, address := range addresses {
		if key != "" && addressKey(address) == key {
			return true
		}
	}
	return false
}

func addressName(line string, cfg config.Config) bool {
	if containsAny(Slug(line), "telefon", "telefax", "internet", "bankverbindung", "kundennummer", "rechnungsnummer", "betreff", "ansprechpartner", "sachbearbeiter") || strings.ContainsAny(line, "@:") {
		return false
	}
	if looksLikeRecipientName(line) {
		return true
	}
	for _, profile := range cfg.RecipientProfiles {
		if profileMatchesName(profile, line) {
			return true // A saved OCR spelling can contain a digit.
		}
	}
	return false
}

func careOfLine(line string) bool {
	value := "-" + Slug(line) + "-"
	return strings.HasPrefix(value, "-c-o-") || strings.HasPrefix(value, "-z-hd-") || strings.HasPrefix(value, "-zu-haenden-") || strings.HasPrefix(value, "-care-of-")
}

func addressRecipientCandidates(text string, cfg config.Config) []addressRecipient {
	// Preserve blank lines: a letterhead separated from the address must not
	// become another recipient name simply because it is nearby in the OCR.
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.Join(strings.Fields(lines[i]), " ")
	}
	var candidates []addressRecipient
	for i := 2; i < len(lines) && i < 100; i++ {
		if !postalLine.MatchString(lines[i]) {
			continue
		}
		address := lines[i-1] + "\n" + lines[i]
		key := addressKey(address)
		if key == "" {
			continue
		}
		start, nameEnd := i-2, i-1
		if careOfLine(lines[start]) {
			nameEnd = start
			start--
		} else if start > 0 && careOfLine(lines[start-1]) {
			nameEnd = start - 1
			start -= 2
		}
		if start < 0 {
			continue
		}
		if !addressName(lines[start], cfg) {
			continue
		}
		// Include a title and wrapped names immediately above the street.
		// Without a title, use the closest name rather than swallowing a sender.
		for j := start; j >= 0 && j >= i-5; j-- {
			if lines[j] == "" || !addressName(lines[j], cfg) {
				break
			}
			if _, _, ok := recipientMarker(lines[j]); ok {
				start = j
				break
			}
		}
		nameLines := lines[start:nameEnd]
		marker, rest, marked := recipientMarker(nameLines[0])
		parts := append([]string{}, nameLines...)
		if marked {
			parts[0] = rest
		}
		name := strings.TrimSpace(strings.Join(parts, " "))
		if name == "" || genericRecipient(name) {
			continue
		}
		typ := recipientType(marker, name)
		if typ == "unknown" {
			typ = "person"
		}
		if typ != "company" && multipleRecipientLines(parts) {
			typ = "household"
		}
		candidate := addressRecipient{Name: Slug(name), Type: typ, Address: address, Evidence: strings.Join(lines[start:i+1], "\n")}
		if marked {
			candidate.Score += 30
		}
		if start > 0 && containsAny(Slug(lines[start-1]), "absender", "sender", "ruecksendeadresse") {
			candidate.Score -= 200
		}
		if matchesAddress(cfg.RecipientAddresses, key) {
			candidate.Score += 100
		}
		profileAddress := false
		for _, profile := range cfg.RecipientProfiles {
			nameMatch := profileMatchesName(profile, name)
			if nameMatch {
				candidate.Score += 10
			}
			if matchesAddress(profile.Addresses, key) {
				profileAddress = true
				if nameMatch {
					candidate.Profiles = append(candidate.Profiles, profile)
				}
			}
		}
		if profileAddress {
			candidate.Score += 100
			if len(candidate.Profiles) > 0 {
				candidate.Score += 30
			} else {
				candidate.Ambiguous = true // Known place, different name.
			}
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func detectAddressRecipient(text string, cfg config.Config) (addressRecipient, bool) {
	candidates := addressRecipientCandidates(text, cfg)
	if len(candidates) == 0 {
		return addressRecipient{}, false
	}
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.Score > best.Score {
			best = candidate
		}
	}
	for _, candidate := range candidates {
		if candidate.Score == best.Score && (candidate.Name != best.Name || candidate.Address != best.Address) {
			best.Ambiguous = true
		}
	}
	if best.Score < 0 {
		return addressRecipient{}, false
	}
	return best, true
}

func addressForRecipient(text string, cfg config.Config, name string) (addressRecipient, bool) {
	best, ok := detectAddressRecipient(text, cfg)
	if !ok {
		return addressRecipient{}, false
	}
	for _, candidate := range addressRecipientCandidates(text, cfg) {
		if candidate.Score >= best.Score && grounded(candidate.Name, name) {
			candidate.Ambiguous = candidate.Ambiguous || best.Ambiguous
			return candidate, true
		}
	}
	return addressRecipient{}, false
}

func inferRecipient(text string, configs ...config.Config) (string, string) {
	var cfg config.Config
	if len(configs) > 0 {
		cfg = configs[0]
	}
	if address, ok := detectAddressRecipient(text, cfg); ok {
		return address.Name, address.Type
	}
	return inferRecipientWithoutAddress(text)
}

func applyAddressAssociation(c *Classification, address addressRecipient, text string) {
	if address.Ambiguous {
		c.RecipientNeedsReview = true
	}
	if len(address.Profiles) != 1 {
		if len(address.Profiles) > 1 {
			c.RecipientNeedsReview = true
		}
		return
	}
	profile := address.Profiles[0]
	if !profileMatchesName(profile, c.Recipient) || profile.Scope == "unknown" {
		return
	}
	if c.RecipientScope != profile.Scope {
		// The address helps identify the block and identity, but cannot silently
		// override contradictory personal/business evidence in the document.
		c.RecipientNeedsReview = true
		explicitScope := businessScope(address.Name)
		personalTax := hasDocumentHeading(text, "einkommensteuerbescheid", "einkommensteuer") || containsAny(Slug(text), "ueber-einkommensteuer", "ueber-die-einkommensteuer")
		if explicitScope != "" || businessRecipientContext(text, address.Name) != "" || personalTax || (profile.Scope == "personal" && businessObligation(text)) {
			return
		}
		// A unique saved name/address association can suggest a persona where
		// the document has no contrary evidence. The change still needs review.
		c.RecipientScope = profile.Scope
		if profile.Scope == "gbr" || profile.Scope == "organization" {
			c.RecipientType = "company"
		}
	}
	if c.DetectedRecipient == "" {
		c.DetectedRecipient = c.Recipient
	}
	c.Recipient = Slug(profile.Name)
	c.RecipientProfileID = profile.ID
}
