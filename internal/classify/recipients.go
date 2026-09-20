package classify

import (
	"regexp"
	"slices"
	"strings"

	"paperless/internal/config"
)

var RecipientScopes = []string{"personal", "sole_proprietor", "gbr", "organization", "unknown"}

var businessTaxPeriod = regexp.MustCompile(`(?i)umsatzsteuer(?:\s+f[üu]r)?\s+20[0-9]{2}\b`)

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
		if c.RecipientProfileID != 0 && profile.ID != c.RecipientProfileID {
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

// Look beyond the postal addressee only when a role explicitly identifies who
// the document concerns. A business in the sender, footer or income breakdown
// does not turn correspondence to a partner into correspondence to their GbR.
func businessRecipientContext(text, recipient string) string {
	lines := cleanLines(text)
	for i, line := range lines {
		value := Slug(line)
		label := false
		for _, prefix := range []string{"steuerschuldner", "leistungsempfaenger", "leistungsempfanger", "rechnungsempfaenger", "rechnungsempfanger"} {
			if strings.HasPrefix(value, prefix+"-") || value == prefix {
				label = true
			}
		}
		// Handle a label and company name split across OCR lines.
		if label && businessScope(line) == "" && i+1 < len(lines) {
			line += " " + lines[i+1]
		}
		representative := grounded(line, recipient) && containsAny(value,
			"als-gesellschafter", "als-empfangsbevollmaechtigter", "als-empfangsbevollmachtigter",
			"als-vertreter", "handelnd-fuer", "handelnd-fur", "im-namen-der")
		if (label || representative) && businessScope(line) != "" {
			return line
		}
	}
	return ""
}

func businessObligation(text string) bool {
	value := Slug(text)
	// Personal income-tax correspondence can discuss profits from a business
	// or partnership. Those sources of income do not change its addressee.
	if hasDocumentHeading(text, "einkommensteuerbescheid", "einkommensteuer") || containsAny(value, "ueber-einkommensteuer", "ueber-die-einkommensteuer") {
		return false
	}
	if containsAny(value, "gewerbesteuer", "gewerbebetrieb", "umsatzsteuerbescheid", "umsatzsteuererklaerung", "umsatzsteuervoranmeldung",
		"umsatzsteuer-voranmeldung", "mahnung-umsatzsteuer",
		"geschaeftskunden", "geschaeftskonto", "betriebshaftpflicht",
		"selbstaendige-taetigkeit", "selbststaendige-taetigkeit",
		"gesonderte-und-einheitliche-feststellung", "einheitliche-und-gesonderte-feststellung") || businessTaxPeriod.MatchString(text) {
		return true
	}
	for _, line := range cleanLines(text) {
		if Slug(line) == "umsatzsteuer" {
			return true
		}
	}
	// A retail invoice's VAT rate/amount says nothing about the customer's
	// capacity. Only obligations or an explicit business account do.
	return false
}

func resolveBusinessProfile(c *Classification, cfg config.Config) {
	var matches []config.RecipientProfile
	for _, profile := range cfg.RecipientProfiles {
		if profile.Scope == c.RecipientScope && grounded(c.RecipientEvidence, profile.Name) {
			matches = append(matches, profile)
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
	}
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
	identityEvidence := c.RecipientEvidence
	address, hasAddress := addressForRecipient(text, cfg, c.Recipient)
	c.RecipientAddress = ""
	if hasAddress {
		c.RecipientAddress = address.Address
		c.RecipientEvidence = address.Evidence
		identityEvidence = address.Name
		defer func() { applyAddressAssociation(c, address, text) }()
	}
	c.RecipientScope = "unknown"
	c.RecipientNeedsReview = true
	if c.RecipientEvidence == "" {
		c.Recipient = ""
		c.RecipientType = "unknown"
		return
	}
	scope := businessScope(identityEvidence)
	contextEvidence := businessRecipientContext(text, c.Recipient)
	if contextEvidence != "" {
		contextScope := businessScope(contextEvidence)
		if scope != "" && scope != contextScope {
			// Conflicting explicit identities need a reviewer, not a guess.
			return
		}
		scope = contextScope
		c.RecipientEvidence = contextEvidence
		identityEvidence = contextEvidence
	}
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
	businessContext := businessObligation(text)
	if scope == "" {
		for _, profile := range cfg.RecipientProfiles {
			// A canonical business name can identify the entity. A partner's
			// name saved as an alias cannot establish their business capacity.
			if compactName(c.Recipient) == compactName(profile.Name) && (profile.Scope == "gbr" || profile.Scope == "organization") && len(profileScopes) == 1 {
				scope = profile.Scope
			}
		}
	}
	observedRecipient, observedType := inferRecipient(text, cfg)
	jointRecipient := observedType == "household" && compactName(observedRecipient) == compactName(c.Recipient)
	jointBusiness := scope == "" && jointRecipient && businessContext
	if jointBusiness {
		scope = "gbr"
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
	if businessScope(identityEvidence) == "" && (profileScopes["sole_proprietor"] || profileScopes["gbr"] || profileScopes["organization"] || jointBusiness) {
		// Shared identities and a partnership inferred without its legal form
		// require confirmation before automatic filing.
		c.RecipientNeedsReview = true
	}
	if scope == "gbr" || scope == "organization" {
		c.RecipientType = "company"
		resolveBusinessProfile(c, cfg)
	}
}

func mergeRecipient(out *Classification, base, llm Classification, cfg config.Config, text string) {
	// Ground the merge in the observed addressee, not a canonical profile name
	// which may never appear in the document. Reassess its capacity and resolve
	// the saved identity again after accepting any grounded model refinement.
	if base.DetectedRecipient != "" && grounded(text, base.DetectedRecipient) {
		base.Recipient = base.DetectedRecipient
		if observed, ok := addressForRecipient(text, cfg, base.Recipient); ok {
			base.RecipientType = observed.Type
		}
		out.Recipient, out.RecipientType = base.Recipient, base.RecipientType
	}
	out.DetectedRecipient = ""
	out.RecipientProfileID = 0
	address, addressMatch := addressForRecipient(text, cfg, llm.Recipient)
	addressRefinement := addressMatch && address.Ambiguous
	if grounded(text, llm.Recipient) && !genericRecipient(llm.Recipient) {
		if base.Recipient == "" || compactName(base.Recipient) == compactName(llm.Recipient) || grounded(base.Recipient, llm.Recipient) || addressRefinement {
			out.Recipient = Slug(llm.Recipient)
			if normalized := normalizeRecipientType(llm.RecipientType); normalized != "" {
				if base.RecipientType == "" || base.RecipientType == "unknown" || base.Recipient == "" || normalized == base.RecipientType || businessScope(recipientEvidence(text, llm.Recipient)) != "" {
					out.RecipientType = normalized
				}
			}
		}
	}
	assessRecipient(out, cfg, text)
	if base.Recipient != "" && llm.Recipient != "" && !genericRecipient(llm.Recipient) && compactName(base.Recipient) != compactName(llm.Recipient) && !grounded(base.Recipient, llm.Recipient) && !addressRefinement {
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
	if containsAny(value, "-business-", "-company-", "-geschaeftlich-", "-unternehmen-", "-firma-") {
		return "business"
	}
	return ""
}

func FolderFitsRecipient(folder string, c Classification, profiles []config.RecipientProfile) bool {
	scope := scopeForFolder(folder)
	if scope == "business" {
		if !slices.Contains([]string{"sole_proprietor", "gbr", "organization"}, c.RecipientScope) {
			return false
		}
	} else if scope != "" && scope != c.RecipientScope {
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
	recipient, typ := inferRecipient(text, cfg)
	c := Classification{Recipient: recipient, RecipientType: typ}
	assessRecipient(&c, cfg, text)
	if c.RecipientNeedsReview && !(c.RecipientScope == "gbr" && businessObligation(text)) {
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
	rejected := c.SuggestedFolder != "" && !FolderFitsRecipient(c.SuggestedFolder, *c, cfg.RecipientProfiles)
	if rejected {
		c.SuggestedFolder = ""
		c.Reasons = append(c.Reasons, "folder does not match the recipient's personal or business capacity")
	}
	c.FolderRankings = slices.DeleteFunc(c.FolderRankings, func(r FolderRanking) bool { return !FolderFitsRecipient(r.Folder, *c, cfg.RecipientProfiles) })
	if rejected && len(c.FolderRankings) == 1 && c.RecipientScope != "unknown" {
		c.SuggestedFolder = c.FolderRankings[0].Folder
		c.RecipientNeedsReview = true
		c.Reasons = append(c.Reasons, "suggested the only ranked folder compatible with the recipient; confirm during review")
	}
}
