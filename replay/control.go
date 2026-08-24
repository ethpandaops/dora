package replay

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// controlUI is the replay control panel the explorer side-loads. Serving it from here
// keeps the explorer's side of the integration down to a single script tag.
//
//go:embed assets/replay-ui.js
var controlUI []byte

// Command is a control instruction, as posted to /replay/command. It mirrors what the
// interactive console offers, so the explorer UI and the console drive the same replay
// through the same code.
type Command struct {
	Action string  `json:"action"`
	Speed  float64 `json:"speed,omitempty"`
	Slots  uint64  `json:"slots,omitempty"`
	Slot   uint64  `json:"slot,omitempty"`
}

// controlHandler serves the replay's own API: the virtual clock the explorer follows, a
// status snapshot, a status event stream, and the commands that drive the replay.
func (r *Replay) controlHandler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/replay/clock", r.serveClock)
	mux.HandleFunc("/replay/status", r.serveStatus)
	mux.HandleFunc("/replay/events", r.control.serveHTTP)
	mux.HandleFunc("/replay/command", r.serveCommand)
	mux.HandleFunc("/replay/ui.js", serveControlUI)

	return withCORS(mux)
}

// withCORS lets the control API be called from the explorer's own origin, which is a
// different host and port than this server.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func serveControlUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(controlUI); err != nil {
		return
	}
}

func (r *Replay) serveClock(w http.ResponseWriter, _ *http.Request) {
	now, rate := r.clock.state()

	writeJSON(w, http.StatusOK, map[string]any{
		"time_ms": now.UnixMilli(),
		"rate":    rate,
	})
}

func (r *Replay) serveStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, r.Status())
}

func (r *Replay) serveCommand(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "commands are posted")
		return
	}

	command := Command{}
	if err := json.NewDecoder(req.Body).Decode(&command); err != nil {
		writeAPIError(w, http.StatusBadRequest, fmt.Sprintf("invalid command: %v", err))
		return
	}

	if err := r.Execute(command); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, r.Status())
}

// Execute applies a control command.
func (r *Replay) Execute(command Command) error {
	switch strings.ToLower(command.Action) {
	case "play":
		// a speed of 0 means "as fast as upstream allows", which is what the UI sends
		// for its `max` setting
		r.Play(command.Speed)

	case "speed":
		r.SetSpeed(command.Speed)

	case "step":
		r.Step(command.Slots)

	case "forward":
		return r.Forward(command.Slot, command.Speed)

	case "stop", "pause":
		r.Pause()

	case "start", "resume":
		r.Resume()

	default:
		return fmt.Errorf("unknown action %q", command.Action)
	}

	return nil
}

// notifyStatus pushes the current status to everything watching the control stream. It
// is lossy by design: a status is a snapshot, so a client that fell behind wants the
// newest one rather than the backlog, and the driver must never wait on a browser tab.
func (r *Replay) notifyStatus() {
	if r.control.subscriberCount() == 0 {
		return
	}

	data, err := json.Marshal(r.Status())
	if err != nil {
		r.logger.WithError(err).Debug("could not encode replay status")
		return
	}

	r.control.publish(sseEvent{topic: "status", data: data})
}
