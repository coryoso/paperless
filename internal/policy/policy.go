package policy

import (
	"context"
	"database/sql"
	"strings"

	"paperless/internal/classify"
	"paperless/internal/config"
	"paperless/internal/db/sqlc"
)

type Decision struct {
	AutoFile bool
	Reasons  []string
}

type Counter interface {
	ApprovedExampleCount(ctx context.Context, arg sqlc.ApprovedExampleCountParams) (int64, error)
	GetFolderApprovedCount(ctx context.Context, path string) (int64, error)
}

func Evaluate(ctx context.Context, cfg config.Config, queries Counter, c classify.Classification) Decision {
	reasons := []string{}
	if c.PhysicalOriginalAction != "discard_candidate" {
		reasons = append(reasons, "paper original is not discard-candidate")
	}
	minConfidence := float64(cfg.Policy.AutoFileMinConfidence) / 100
	if c.Confidence < minConfidence {
		reasons = append(reasons, "classification confidence below auto-file threshold")
	}
	if strings.TrimSpace(c.SuggestedFolder) == "" {
		reasons = append(reasons, "no suggested folder")
	}
	if c.Sensitive {
		reasons = append(reasons, "sensitive document type")
	}
	folderKnown := folderConfigured(cfg, c.SuggestedFolder)
	if !folderKnown && queries != nil && c.SuggestedFolder != "" {
		if count, err := queries.GetFolderApprovedCount(ctx, c.SuggestedFolder); err == nil {
			folderKnown = count > 0
		}
	}
	if !folderKnown && !cfg.Policy.AllowNewTopLevelFolders {
		reasons = append(reasons, "folder is not configured or learned")
	}
	if cfg.Policy.MinApprovedExamples > 0 && queries != nil && c.SuggestedFolder != "" {
		count, err := queries.ApprovedExampleCount(ctx, sqlc.ApprovedExampleCountParams{
			Sender:       c.Sender,
			Recipient:    c.Recipient,
			DocumentType: c.DocumentType,
			Folder:       c.SuggestedFolder,
		})
		if err != nil && err != sql.ErrNoRows {
			reasons = append(reasons, "could not check learned examples")
		} else if count < int64(cfg.Policy.MinApprovedExamples) {
			reasons = append(reasons, "not enough learned approvals for sender/recipient/type/folder")
		}
	}
	return Decision{AutoFile: len(reasons) == 0, Reasons: reasons}
}

func folderConfigured(cfg config.Config, folder string) bool {
	folder = strings.Trim(folder, "/")
	for _, known := range cfg.Policy.KnownFolders {
		if strings.Trim(known, "/") == folder {
			return true
		}
	}
	return false
}
