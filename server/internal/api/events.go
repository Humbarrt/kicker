package api

import (
	"encoding/json"
	"fmt"
	"github.com/Humbarrt/kicker/server/internal/backup"
	"github.com/Humbarrt/kicker/server/internal/update"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/table"
	"github.com/Humbarrt/kicker/server/internal/tournaments"
)

// Bus topics besides table.TopicTable.
const (
	// TopicModules is published (without data) when the module list changed; clients re-read
	// GET /api/modules.
	TopicModules = "modules"
	// TopicConsole carries ConsoleLine values: the live module console (D8).
	TopicConsole = "console"
	// TopicStats is stats.TopicStats: ratings and stats changed (spec 02 §8 step 4); clients
	// re-read what they show.
	TopicStats = "stats"
)

// GET /api/events is the SSE stream (D3). Event names are the bus topics:
//   - "table": a table.View; the first message is always a full snapshot. Ignore views with a
//     rev lower than the last one seen.
//   - "stats": {from_game} – ratings/stats changed; re-read them.
//   - "modules": {} – re-read /api/modules (admins only).
//   - "console": a ConsoleLine (admins only).
//   - "accounts": {} – sign-ups arrived or were decided; re-read /api/auth/me (admins only).
//
// A client that falls behind is disconnected; EventSource reconnects and gets a new snapshot.
func (o Options) eventsRoute(api *gin.RouterGroup) {
	heartbeat := o.Heartbeat
	if heartbeat == 0 {
		heartbeat = 15 * time.Second
	}
	api.GET("/events", o.Require(auth.ViewLive), func(c *gin.Context) {
		topics := []string{table.TopicTable, TopicStats}
		if o.perms(c).Has(auth.ViewTournaments) {
			topics = append(topics, tournaments.TopicTournament)
		}
		if account(c).IsAdmin() && !account(c).MustChangePassword {
			topics = append(topics, TopicModules, TopicConsole, TopicAccounts, backup.Topic, update.Topic)
		}
		sub := o.Bus.Subscribe(256, topics...)
		defer sub.Close()

		w := c.Writer
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "retry: 2000\n\n")
		if o.Table != nil {
			writeEvent(w, table.TopicTable, o.Table.View())
		}
		w.Flush()

		ping := time.NewTicker(heartbeat)
		defer ping.Stop()
		for {
			select {
			case <-c.Request.Context().Done():
				return
			case <-o.Done:
				return
			case m, ok := <-sub.C:
				if !ok {
					return // fell behind
				}
				data := m.Data
				if data == nil {
					data = struct{}{}
				}
				writeEvent(w, m.Topic, data)
				w.Flush()
			case <-ping.C:
				_, _ = fmt.Fprint(w, ": ping\n\n")
				w.Flush()
			}
		}
	})
}

func writeEvent(w http.ResponseWriter, name string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
}
