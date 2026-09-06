package classify

import (
	"slices"
	"strings"

	"paperless/internal/config"
)

var RecipientScopes = []string{"personal", "sole_proprietor", "gbr", "organization", "unknown"}

func ValidRecipientScope(scope string) bool { return slices.Contains(RecipientScopes, scope) }

// PreferSavedRecipient resolves a recognized name to one saved identity, keeping
// the observed spelling for review and alias learning. Capacity remains a guard:
// a personal addressee must not become a business just because its name matches.
func PreferSavedRecipient(c Classification, cfg config.Config) Classification {
	var matches []config.RecipientProfile
	for _, profile := range cfg.RecipientProfiles {
		if profile.Scope != c.RecipientScope {
			continue
		}
		for _, alias := range append([]string{profile.Name}, profile.Aliases...) {
			if Slug(alias) != "" && Slug(alias) == Slug(c.Recipient) {
				matches = append(matches, profile)
				break
			}
		}
	}
	if len(matches) == 1 {
		if c.DetectedRecipient == "" {
			c.DetectedRecipient = c.Recipient
		}
		c.Recipient = Slug(matches[0].Name)
		c.RecipientProfileID = matches[0].ID
	} else if len(matches) > 1 {
		c.RecipientNeedsReview = true
		c.RecipientProfileID = 0
	}
	return c
}

func grounded(text, value string) bool {
	value = Slug(value)
	return value != "" && strings.Contains("-"+Slug(text)+"-", "-"+value+"-")
}

func recipientEvidence(text, recipient string) string {
	if recipient == "" {
		return ""
	}
	lines := cleanLines(text)
	for n := 1; n <= 4; n++ {
		for i := 0; i+n <= len(lines) && i < 80; i++ {
			block := strings.Join(lines[i:i+n], " ")
			if grounded(block, recipient) {
				// Include a following business name in the address, but never the street
				// or body of the letter. It can distinguish a person from their business.
				if i+n < len(lines) && looksLikeRecipientName(lines[i+n]) && businessScope(lines[i+n]) != "" {
					block += " " + lines[i+n]
				}
				return block
			}
		}
	}
	return ""
}

func businessScope(evidence string) string {
	value := "-" + Slug(evidence) + "-"
	if containsAny(value, "-gbr-", "-egbr-", "-gesellschaft-buergerlichen-rechts-", "-gesellschaft-burgerlichen-rechts-") {
		return "gbr"
	}
	if containsAny(value, "-einzelunternehmer", "-einzelunternehmen", "-sole-proprietor-", "-inhaber-", "-inhaberin-", "-freiberufler", "-als-unternehmer-", "-als-selbstaendiger-", "-als-selbststaendiger-") {
		return "sole_proprietor"
	}
	if containsAny(value, "-gmbh-", "-ohg-", "-kg-", "-ug-", "-ag-", "-e-v-", "-firma-") {
		return "organization"
	}
	return ""
}

func assessRecipient(c *Classification, cfg config.Config, text string) {
	// Saved spellings can contain OCR errors (for example a digit in a name)
	// which generic name detection deliberately rejects. Only recover these
	// inside an address introduced by a recipient marker, never from the sender.
	if c.Recipient == "" {
		lines := cleanLines(text)
		for i, line := range lines {
			if i >= 80 {
				break
			}
			marker, rest, ok := recipientMarker(line)
			if !ok {
				continue
			}
			candidates := []string{rest}
			if i+1 < len(lines) {
				candidates = append(candidates, lines[i+1])
			}
			for _, candidate := range candidates {
				for _, profile := range cfg.RecipientProfiles {
					for _, alias := range append([]string{profile.Name}, profile.Aliases...) {
						if Slug(alias) != "" && Slug(candidate) == Slug(alias) {
							c.Recipient, c.RecipientType = Slug(candidate), recipientType(marker, candidate)
						}
					}
				}
				if c.Recipient != "" {
					break
				}
			}
			if c.Recipient != "" {
				break
			}
		}
	}
	c.RecipientEvidence = recipientEvidence(text, c.Recipient)
	c.RecipientScope = "unknown"
	c.RecipientNeedsReview = true
	if c.RecipientEvidence == "" {
		c.Recipient = ""
		c.RecipientType = "unknown"
		return
	}
	scope := businessScope(c.RecipientEvidence)
	// Profiles match the addressee, never an organization mentioned elsewhere
	// (especially the sender). The same name can have several capacities.
	profileScopes := map[string]bool{}
	for _, profile := range cfg.RecipientProfiles {
		for _, alias := range append([]string{profile.Name}, profile.Aliases...) {
			if grounded(c.Recipient, alias) && ValidRecipientScope(profile.Scope) && profile.Scope != "unknown" {
				profileScopes[profile.Scope] = true
			}
		}
	}
	businessContext := containsAny(Slug(text), "umsatzsteuer", "gewerbesteuer", "gewerbebetrieb", "selbstaendige-taetigkeit", "selbststaendige-taetigkeit")
	if scope == "" && len(profileScopes) == 1 {
		for match := range profileScopes {
			if match == "gbr" || match == "organization" {
				scope = match
			}
		}
	}
	if scope == "" && (c.RecipientType == "person" || c.RecipientType == "household") {
		// A named individual is normally personal. Explicit business obligations
		// make their capacity ambiguous until the model or reviewer grounds it.
		if !businessContext {
			scope = "personal"
		}
	}
	if scope == "" && c.RecipientType == "company" {
		scope = "organization"
	}
	if scope != "" {
		c.RecipientScope = scope
		c.RecipientNeedsReview = false
	}
	if len(profileScopes) > 1 && businessScope(c.RecipientEvidence) == "" && c.RecipientScope != "personal" {
		c.RecipientScope = "unknown"
		c.RecipientNeedsReview = true
	}
	if profileScopes["sole_proprietor"] && businessScope(c.RecipientEvidence) == "" {
		c.RecipientNeedsReview = true
	}
}

func mergeRecipient(out *Classification, base, llm Classification, cfg config.Config, text string) {
	if grounded(text, llm.Recipient) && !genericRecipient(llm.Recipient) {
		if base.Recipient == "" || compactName(base.Recipient) == compactName(llm.Recipient) || grounded(base.Recipient, llm.Recipient) {
			out.Recipient = Slug(llm.Recipient)
			if normalized := normalizeRecipientType(llm.RecipientType); normalized != "" {
				if base.RecipientType == "" || base.RecipientType == "unknown" || base.Recipient == "" || normalized == base.RecipientType || businessScope(recipientEvidence(text, llm.Recipient)) != "" {
					out.RecipientType = normalized
				}
			}
		}
	}
	assessRecipient(out, cfg, text)
	if base.Recipient != "" && llm.Recipient != "" && !genericRecipient(llm.Recipient) && compactName(base.Recipient) != compactName(llm.Recipient) && !grounded(base.Recipient, llm.Recipient) {
		out.RecipientNeedsReview = true
	}
	// A scope change needs a quoted, grounded explanation tied to this recipient.
	// A tax authority or a business name elsewhere is not evidence of capacity.
	if llm.RecipientEvidence != "" && grounded(text, llm.RecipientEvidence) && grounded(llm.RecipientEvidence, out.Recipient) {
		evidenceScope := businessScope(llm.RecipientEvidence)
		if evidenceScope != "" && evidenceScope == llm.RecipientScope && len([]rune(llm.RecipientEvidence)) <= 240 {
			if out.RecipientScope == "unknown" || out.RecipientScope == evidenceScope || (out.RecipientScope == "personal" && evidenceScope == "sole_proprietor") {
				out.RecipientScope = evidenceScope
				out.RecipientEvidence = llm.RecipientEvidence
			} else {
				out.RecipientNeedsReview = true
			}
		}
	}
	if out.RecipientScope == "unknown" {
		out.RecipientNeedsReview = true
	}
}

func scopeForFolder(folder string) string {
	value := "-" + Slug(folder) + "-"
	if containsAny(value, "-gbr-", "-egbr-") {
		return "gbr"
	}
	if containsAny(value, "-einzelunternehmen-", "-einzelunternehmer-", "-sole-proprietor-", "-freiberuflich-", "-selbstaendigkeit-", "-selbsta-ndigkeit-", "-selbstandigkeit-") {
		return "sole_proprietor"
	}
	if containsAny(value, "-privat-", "-private-", "-personal-") {
		return "personal"
	}
	return ""
}

func FolderFitsRecipient(folder string, c Classification, profiles []config.RecipientProfile) bool {
	scope := scopeForFolder(folder)
	if scope != "" && scope != c.RecipientScope {
		return false
	}
	matched, compatible, specificity := false, false, 0
	for _, profile := range profiles {
		prefix := strings.Trim(profile.FolderPrefix, "/")
		if prefix == "" || (folder != prefix && !strings.HasPrefix(folder, prefix+"/")) {
			continue
		}
		if len(prefix) < specificity {
			continue
		}
		if len(prefix) > specificity {
			compatible = false
			specificity = len(prefix)
		}
		matched = true
		if profile.Scope != c.RecipientScope {
			continue
		}
		for _, alias := range append([]string{profile.Name}, profile.Aliases...) {
			if grounded(c.Recipient, alias) {
				compatible = true
			}
		}
	}
	return !matched || compatible
}

func RecipientFolders(cfg config.Config, text string, folders []string) []string {
	recipient, typ := inferRecipient(text)
	c := Classification{Recipient: recipient, RecipientType: typ}
	assessRecipient(&c, cfg, text)
	if c.RecipientNeedsReview {
		return folders
	}
	out := []string{}
	for _, folder := range folders {
		if FolderFitsRecipient(folder, c, cfg.RecipientProfiles) {
			out = append(out, folder)
		}
	}
	return out
}

func validateRecipientFolder(c *Classification, cfg config.Config) {
	if c.SuggestedFolder != "" && !FolderFitsRecipient(c.SuggestedFolder, *c, cfg.RecipientProfiles) {
		c.SuggestedFolder = ""
		c.Reasons = append(c.Reasons, "folder does not match the recipient's personal or business capacity")
	}
	c.FolderRankings = slices.DeleteFunc(c.FolderRankings, func(r FolderRanking) bool { return !FolderFitsRecipient(r.Folder, *c, cfg.RecipientProfiles) })
}
