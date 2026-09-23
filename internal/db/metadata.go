package db

import (
	"context"
	"encoding/json"
	"paperless/internal/classify"
	"paperless/internal/db/sqlc"
	"paperless/internal/document"
	"strings"
)

// BackfillMetadata adds the deterministic identity projection without rerunning
// OCR or the model. Existing reviewed profile selections remain authoritative.
func (s *Store) BackfillMetadata(ctx context.Context) error {
	rows, err := s.conn.QueryContext(ctx, `SELECT b.job_id,b.unified_json,j.classification_json FROM document_blocks b JOIN jobs j ON j.id=b.job_id WHERE b.unified_json<>'null'`)
	if err != nil {
		return err
	}
	type item struct{ id, unified, classification string }
	items := []item{}
	for rows.Next() {
		var v item
		if err = rows.Scan(&v.id, &v.unified, &v.classification); err != nil {
			rows.Close()
			return err
		}
		items = append(items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, v := range items {
		var u document.Unified
		if err = json.Unmarshal([]byte(v.unified), &u); err != nil {
			return err
		}
		if u.Normalized != nil && u.Normalized.Version == document.MetadataVersion {
			continue
		}
		previous := u.Normalized
		u.Normalized = document.NormalizeMetadata(&u)
		if previous != nil && (previous.Recipient.Origin == "review" || previous.Recipient.Origin == "profile") {
			u.Normalized.Recipient = previous.Recipient
			for i, address := range u.Normalized.Recipient.Addresses {
				parsed := document.ParsePostalAddress(address.Lines)
				if address.StreetName != "" {
					parsed.StreetName = address.StreetName
				}
				if address.HouseNumber != "" {
					parsed.HouseNumber = address.HouseNumber
				}
				if address.PostalCode != "" {
					parsed.PostalCode = address.PostalCode
				}
				if address.City != "" {
					parsed.City = address.City
				}
				u.Normalized.Recipient.Addresses[i] = parsed
			}
		}
		var c classify.Classification
		if strings.TrimSpace(v.classification) == "" {
			v.classification = "{}"
		}
		if err = json.Unmarshal([]byte(v.classification), &c); err != nil {
			return err
		}
		classify.ApplyUnified(&c, &u)
		if c.RecipientProfileID != 0 {
			profiles, e := s.RecipientProfiles(ctx)
			if e != nil {
				return e
			}
			for _, p := range profiles {
				if p.ID == c.RecipientProfileID {
					c.Metadata.Recipient = document.NormalizeIdentity(document.Party{Names: []string{p.Name}, Addresses: p.Addresses}, p.Scope == "personal" || p.Scope == "sole_proprietor")
					c.Metadata.Recipient.Names = []string{p.Name}
					c.Metadata.Recipient.ProfileID = p.ID
					c.Metadata.Recipient.Origin = "profile"
					c.RecipientDisplay = p.Name
				}
			}
		}
		raw, _ := json.Marshal(u)
		classification, _ := json.Marshal(c)
		tx, e := s.conn.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE document_blocks SET unified_json=? WHERE job_id=?`, string(raw), v.id); e == nil {
			_, e = tx.ExecContext(ctx, `UPDATE jobs SET classification_json=?,updated_at=? WHERE id=?`, string(classification), Now(), v.id)
		}
		if e != nil {
			tx.Rollback()
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
	}
	return nil
}

func (s *Store) SaveReviewedClassification(ctx context.Context, p sqlc.SetClassifiedParams, metadata *document.Metadata) error {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.Queries.WithTx(tx).SetClassified(ctx, p); err != nil {
		return err
	}
	if metadata != nil {
		raw, e := json.Marshal(metadata)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `UPDATE document_blocks SET unified_json=json_set(unified_json,'$.normalized',json(?)) WHERE job_id=? AND unified_json<>'null'`, string(raw), p.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
