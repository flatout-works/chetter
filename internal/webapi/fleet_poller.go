package webapi

import (
	"context"
	"log/slog"
	"time"

	apiv1 "github.com/flatout-works/chetter/gen/proto/api/v1"
	"github.com/flatout-works/chetter/internal/service"
)

// StartFleetCursorPoller runs one goroutine per server process that polls the
// newest task-event timestamp from the database and publishes a fleet update
// into the local event bus whenever it advances. This lets fleet-dashboard
// streams observe task activity committed by other server replicas without
// each stream performing its own database query. The goroutine exits when
// stopCh closes.
func StartFleetCursorPoller(stopCh <-chan struct{}, svc *service.Service, bus *EventBus) {
	if svc == nil || bus == nil || stopCh == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(fleetPollInterval)
		defer ticker.Stop()
		var (
			last     time.Time
			seeded   bool
			lastWarn time.Time
		)
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				current, err := svc.FleetUpdateCursor(ctx)
				cancel()
				if err != nil {
					// Rate-limit warnings: a DB outage would otherwise log
					// every tick.
					if time.Since(lastWarn) > time.Minute {
						lastWarn = time.Now()
						slog.Warn("fleet cursor poll failed", "err", err)
					}
					continue
				}
				if !seeded {
					// Seed the baseline so an already-active queue does not
					// emit a spurious update at startup.
					seeded = true
					last = current
					continue
				}
				if current.After(last) {
					last = current
					bus.PublishFleetUpdate(&apiv1.FleetUpdate{Type: "task_status_change"})
				}
			}
		}
	}()
}
