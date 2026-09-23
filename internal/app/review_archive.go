package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"paperless/internal/db/sqlc"
)

// Keep the review source and a backup of the old archive until the database
// points to the new file. A failed approval can then restore the previous state.
type reviewArchive struct {
	path, previous, backup string
	committed              bool
}

func (a *reviewArchive) rollback() error {
	if a.committed {
		return nil
	}
	var err error
	if a.path != "" {
		err = removeJobArtifact(a.path, false)
	}
	if a.backup != "" {
		// Restore without overwriting any file that appeared during approval.
		restoreErr := copyArchiveExclusive(a.backup, a.previous)
		if restoreErr == nil {
			restoreErr = os.Remove(a.backup)
		}
		err = errors.Join(err, restoreErr)
	}
	return err
}

func (a *reviewArchive) cleanup(source string) error {
	return errors.Join(removeJobArtifact(a.backup, false), removeJobArtifact(source, false))
}

func (p *Processor) prepareReviewArchive(ctx context.Context, job sqlc.Job, destination string, replace bool) (_ *reviewArchive, err error) {
	// Resolve directory symlinks before checking containment, including on macOS
	// where temporary directories themselves can be reached through symlinks.
	if err := p.validateArchiveFile(destination); err != nil {
		return nil, err
	}
	a := &reviewArchive{}
	if replace && job.FinalPath != "" {
		if err := p.validateArchiveFile(job.FinalPath); err != nil {
			return nil, err
		}
		info, statErr := os.Lstat(job.FinalPath)
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return nil, statErr
		}
		if statErr == nil {
			if !info.Mode().IsRegular() {
				return nil, errors.New("previous archived file must be a regular file; choose Keep both files")
			}
			var references int
			if err := p.store.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE id != ? AND (final_path = ? OR current_path = ?)`, job.ID, job.FinalPath, job.FinalPath).Scan(&references); err != nil {
				return nil, err
			}
			if references != 0 {
				return nil, errors.New("previous archived file is used by another document; choose Keep both files")
			}
			a.previous = job.FinalPath
		}
	}
	// Fully copy the reviewed PDF on the destination filesystem before touching
	// the previous archive. Exclusive creation never overwrites a name collision.
	staged, err := os.CreateTemp(filepath.Dir(destination), ".paperless-review-*")
	if err != nil {
		return nil, err
	}
	stagedPath := staged.Name()
	if err := staged.Close(); err != nil {
		_ = os.Remove(stagedPath)
		return nil, err
	}
	defer os.Remove(stagedPath)
	if err := copyFile(job.CurrentPath, stagedPath); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, a.rollback())
		}
	}()
	if a.previous != "" {
		backup, createErr := os.CreateTemp(filepath.Dir(a.previous), ".paperless-previous-*")
		if createErr != nil {
			return nil, createErr
		}
		backupPath := backup.Name()
		if closeErr := backup.Close(); closeErr != nil {
			_ = os.Remove(backupPath)
			return nil, closeErr
		}
		if renameErr := os.Rename(a.previous, backupPath); renameErr != nil {
			_ = os.Remove(backupPath)
			return nil, renameErr
		}
		a.backup = backupPath
	}
	path := uniquePath(destination)
	if err := copyArchiveExclusive(stagedPath, path); err != nil {
		return nil, fmt.Errorf("save reviewed file: %w", err)
	}
	a.path = path
	return a, nil
}

func (p *Processor) validateArchiveFile(path string) error {
	root, err := filepath.EvalSymlinks(p.cfg.Paths.ArchiveRoot)
	if err != nil {
		return err
	}
	// A missing previous directory means the old file is already gone. Resolve
	// the nearest existing ancestor to still detect escapes through symlinks.
	parent := filepath.Dir(path)
	var suffix []string
	for {
		resolved, resolveErr := filepath.EvalSymlinks(parent)
		if resolveErr == nil {
			parent = resolved
			for i := len(suffix) - 1; i >= 0; i-- {
				parent = filepath.Join(parent, suffix[i])
			}
			break
		}
		if !errors.Is(resolveErr, os.ErrNotExist) || filepath.Dir(parent) == parent {
			return resolveErr
		}
		suffix = append(suffix, filepath.Base(parent))
		parent = filepath.Dir(parent)
	}
	rel, err := filepath.Rel(root, filepath.Join(parent, filepath.Base(path)))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("file must be inside the archive root; choose Keep both files if the previous file is elsewhere")
	}
	return nil
}

// Use exclusive creation instead of rename (which can overwrite another file)
// or hard links (which are not supported by every archive filesystem).
func copyArchiveExclusive(source, destination string) (err error) {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, out.Close())
		if err != nil {
			err = errors.Join(err, os.Remove(destination))
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
