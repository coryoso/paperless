package classify

import (
	"sort"
	"strings"

	"paperless/internal/config"
)

type RoutingExample struct {
	Sender         string `json:"sender"`
	Recipient      string `json:"recipient"`
	RecipientScope string `json:"recipient_scope"`
	DocumentType   string `json:"document_type"`
	Folder         string `json:"folder"`
	Filename       string `json:"filename"`
	Approvals      int64  `json:"approvals"`
}

func relevantExamples(c Classification, history []RoutingExample, folders []string, cfg config.Config) []RoutingExample {
	out := []RoutingExample{}
	if c.Recipient == "" || c.RecipientScope == "unknown" || c.RecipientScope == "" {
		return out
	}
	for _, example := range history {
		if !sameRecipient(c.Recipient, example.Recipient, c.RecipientScope, cfg.RecipientProfiles) || c.RecipientScope != example.RecipientScope {
			continue
		}
		if !folderAllowed(example.Folder, folders) || !FolderFitsRecipient(example.Folder, c, cfg.RecipientProfiles) {
			continue
		}
		if len(c.DocumentDate) >= 4 && folderHasDifferentYear(example.Folder, c.DocumentDate[:4]) {
			continue
		}
		out = append(out, example)
	}
	sort.SliceStable(out, func(i, j int) bool {
		score := func(e RoutingExample) int {
			s := 0
			if compactName(e.Sender) == compactName(c.Sender) {
				s += 4
			}
			if e.DocumentType == c.DocumentType {
				s += 2
			}
			return s
		}
		return score(out[i]) > score(out[j])
	})
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

func sameRecipient(a, b, scope string, profiles []config.RecipientProfile) bool {
	if compactName(a) == compactName(b) {
		return true
	}
	for _, p := range profiles {
		if p.Scope != scope {
			continue
		}
		matchA, matchB := false, false
		for _, alias := range append([]string{p.Name}, p.Aliases...) {
			matchA = matchA || compactName(a) == compactName(alias)
			matchB = matchB || compactName(b) == compactName(alias)
		}
		if matchA && matchB {
			return true
		}
	}
	return false
}

func applyLearnedRouting(c *Classification, examples []RoutingExample) {
	votes := map[string]int64{}
	for _, example := range examples {
		if compactName(example.Sender) == compactName(c.Sender) && example.DocumentType == c.DocumentType {
			votes[example.Folder] += example.Approvals
		}
	}
	best, count, tied := "", int64(0), false
	for folder, votes := range votes {
		if votes > count {
			best, count, tied = folder, votes, false
		} else if votes == count {
			tied = true
		}
	}
	if tied {
		c.SuggestedFolder = ""
		c.FolderRankings = nil
		c.Reasons = append(c.Reasons, "conflicting approved destinations for this recipient and document type")
		return
	}
	if best != "" {
		c.SuggestedFolder = best
		c.Reasons = append(c.Reasons, "learned from approvals for the same sender, recipient, capacity and document type")
		c.FolderRankings = rankSingle(best, c.Confidence, "previously approved destination")
	}
}

// Include relevant learned destinations before the application caps the folder
// prompt, so a folder with an unrelated name can still be suggested.
func LearnedFolders(cfg config.Config, text string, history []RoutingExample, folders []string) []string {
	recipient, typ := inferRecipient(text)
	c := Classification{Recipient: recipient, RecipientType: typ}
	assessRecipient(&c, cfg, text)
	out := []string{}
	for _, e := range relevantExamples(c, history, folders, cfg) {
		if !slicesContains(out, e.Folder) {
			out = append(out, e.Folder)
		}
	}
	for _, p := range cfg.RecipientProfiles {
		if p.Scope == c.RecipientScope && sameRecipient(c.Recipient, p.Name, c.RecipientScope, cfg.RecipientProfiles) {
			for _, folder := range folders {
				if p.FolderPrefix != "" && (folder == p.FolderPrefix || strings.HasPrefix(folder, p.FolderPrefix+"/")) && len(out) < 24 && !slicesContains(out, folder) {
					out = append(out, folder)
				}
			}
		}
	}
	return out
}

func slicesContains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
