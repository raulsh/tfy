package api

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/sse"

	"github.com/raulsh/tfy/internal/events"
	"github.com/raulsh/tfy/internal/store/db"
)

// terminalRun reports whether a run status is final.
func terminalRun(status string) bool {
	switch status {
	case "queued", "running":
		return false
	}
	return true
}

const replayPage = 500

// runStream streams one run: first the stored history after Last-Event-ID,
// then live events, then a final status event. It subscribes before
// replaying and skips anything already sent, so nothing falls in the gap.
// A subscriber that falls behind is cut off and reconnects to replay.
func (s *Server) runStream() fiber.Handler {
	return sse.New(sse.Config{
		HeartbeatInterval: 20 * time.Second,
		Retry:             2 * time.Second,
		Handler: func(c fiber.Ctx, stream *sse.Stream) error {
			runID := c.Params("id")
			ctx := stream.Context()
			sub := s.Hub.Subscribe(events.RunTopic(runID), 512)
			defer sub.Close()

			run, err := s.Store.Q.GetRun(ctx, runID)
			if err != nil {
				return stream.Event(sse.Event{Name: "error", Data: map[string]string{"error": "run not found"}})
			}
			after, _ := strconv.ParseInt(stream.LastEventID(), 10, 64)
			if q, err := strconv.ParseInt(c.Query("after"), 10, 64); err == nil && q > after {
				after = q
			}
			for {
				evs, err := s.Store.Q.ListRunEvents(ctx, db.ListRunEventsParams{RunID: runID, After: after, Lim: replayPage})
				if err != nil {
					return err
				}
				for _, e := range evs {
					if err := stream.Event(sse.Event{ID: strconv.FormatInt(e.Seq, 10), Name: "event", Data: runEventView(e)}); err != nil {
						return err
					}
					after = e.Seq
				}
				if len(evs) < replayPage {
					break
				}
			}
			// Re-read: the run may have finished during the replay.
			if run, err = s.Store.Q.GetRun(ctx, runID); err == nil && terminalRun(run.Status) {
				return stream.Event(sse.Event{Name: "status", Data: map[string]string{"status": run.Status, "reason": run.Reason}})
			}
			for {
				select {
				case <-stream.Done():
					return nil
				case m, ok := <-sub.C:
					if !ok {
						return nil // too slow: the client reconnects and replays
					}
					switch m.Kind {
					case "event":
						if m.Seq <= after {
							continue
						}
						after = m.Seq
						if err := stream.Event(sse.Event{ID: strconv.FormatInt(m.Seq, 10), Name: "event", Data: []byte(m.Data)}); err != nil {
							return err
						}
					case "status":
						if err := stream.Event(sse.Event{Name: "status", Data: []byte(m.Data)}); err != nil {
							return err
						}
						var st struct {
							Status string `json:"status"`
						}
						_ = json.Unmarshal(m.Data, &st)
						if terminalRun(st.Status) {
							return nil
						}
					}
				}
			}
		},
	})
}

// globalStream tells the UI which entities changed, so it can refetch them.
func (s *Server) globalStream() fiber.Handler {
	return sse.New(sse.Config{
		HeartbeatInterval: 20 * time.Second,
		Retry:             2 * time.Second,
		Handler: func(c fiber.Ctx, stream *sse.Stream) error {
			sub := s.Hub.Subscribe(events.TopicGlobal, 256)
			defer sub.Close()
			if err := stream.Event(sse.Event{Name: "hello", Data: map[string]string{"version": s.Version}}); err != nil {
				return err
			}
			for {
				select {
				case <-stream.Done():
					return nil
				case m, ok := <-sub.C:
					if !ok {
						return nil
					}
					ev := sse.Event{Name: "change", Data: map[string]string{"kind": m.Kind, "id": m.ID}}
					if m.Kind == "agents" {
						// The agent board comes whole: it changes with every
						// step an agent takes.
						ev = sse.Event{Name: "agents", Data: m.Data}
					}
					if err := stream.Event(ev); err != nil {
						return err
					}
				}
			}
		},
	})
}
