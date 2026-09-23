package app

import (
	"context"
	"paperless/internal/config"
	"paperless/internal/ocr"
	"paperless/internal/progress"
)

func ocrWorkerCount(cfg config.Config) int {
	if cfg.OCR.Workers <= 0 {
		return 2
	}
	return min(cfg.OCR.Workers, 8)
}

func acquireStage(ctx context.Context, slots chan struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case slots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-slots
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Processor) runOCR(ctx context.Context, input, workDir string, reporter progress.Reporter) (ocr.Result, error) {
	reporter.Info("ocr", "waiting", "Waiting for an available OCR slot.", 0, 0, 12)
	if err := acquireStage(ctx, p.ocrSlots); err != nil {
		return ocr.Result{}, err
	}
	defer func() { <-p.ocrSlots }()
	result, err := p.processOCR(ctx, p.cfg, input, workDir, reporter)
	if err != nil {
		return result, err
	}
	result.BlockDocument, err = ocr.ReadBlockDocument(workDir, result.TextPath, result.PageCount, 1)
	return result, err
}

func (p *Processor) waitForClassification(ctx context.Context, reporter progress.Reporter) error {
	reporter.Info("classify", "waiting", "Text is ready; waiting for classification. Other documents can continue OCR.", 0, 0, 86)
	return acquireStage(ctx, p.classificationSlots)
}
