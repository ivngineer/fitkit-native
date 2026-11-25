// Package jobs runs periodic background work.
package jobs

import (
	"context"
	"log/slog"
	"time"

	"fitkit/server/internal/workspace/service"
)

// Run expires unanswered price confirmations (auto-refund after 24 h) and flags stalled
// store parcels (no tracking for 3 days, no movement for 5) every interval until ctx ends.
func Run(ctx context.Context, svc *service.Service, every time.Duration, log *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if n, err := svc.ExpireConfirmations(ctx); err != nil {
			log.Error("job: expire confirmations", "err", err)
		} else if n > 0 {
			log.Info("job: auto-refunded unanswered price confirmations", "items", n)
		}
		if n, err := svc.FlagStalled(ctx); err != nil {
			log.Error("job: flag stalled parcels", "err", err)
		} else if n > 0 {
			log.Info("job: flagged stalled parcels", "shipments", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
