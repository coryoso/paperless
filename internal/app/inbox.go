package app

import (
	"context"
	"errors"
	"os"
	"time"
)

func (p *Processor) ProcessInboxOnce(ctx context.Context) (int, error) {
	queue := make(chan processingWork, cap(p.processingQueue))
	done := make(chan struct{})
	go func() { defer close(done); p.processQueue(ctx, queue) }()
	count, err := p.queueInboxOnce(ctx, queue)
	close(queue)
	<-done
	return count, errors.Join(err, ctx.Err())
}

// Observe the whole batch over one stability interval, then hand it to the
// shared workers. The service can scan again while those documents process.
func (p *Processor) queueInboxOnce(ctx context.Context, queue chan<- processingWork) (int, error) {
	if p.cfg.NeedsSetup() {
		return 0, nil
	}
	paths, err := p.candidateFiles()
	if err != nil {
		return 0, err
	}
	first := make(map[string]os.FileInfo, len(paths))
	for _, path := range paths {
		p.processingMu.Lock()
		pending := p.inboxPending[path]
		p.processingMu.Unlock()
		if pending {
			continue
		}
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, err
		}
		first[path] = info
	}
	if len(first) == 0 {
		return 0, nil
	}
	timer := time.NewTimer(time.Duration(p.cfg.Service.FileStabilitySeconds) * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-timer.C:
	}
	count := 0
	for _, path := range paths {
		before := first[path]
		if before == nil {
			continue
		}
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return count, err
		}
		if before.Size() != info.Size() || !before.ModTime().Equal(info.ModTime()) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return count, err
		}
		p.processingMu.Lock()
		if p.inboxPending[path] {
			p.processingMu.Unlock()
			continue
		}
		jobID := randomID()
		err = p.createJob(ctx, jobID, path, info, nil)
		if err == nil {
			p.inboxPending[path] = true
		}
		p.processingMu.Unlock()
		if err != nil {
			return count, err
		}
		select {
		case queue <- processingWork{inbox: true, jobID: jobID, inputPath: path, scanTime: info.ModTime()}:
			count++
		case <-ctx.Done():
			p.processingMu.Lock()
			delete(p.inboxPending, path)
			p.processingMu.Unlock()
			return count, ctx.Err()
		}
	}
	return count, nil
}
