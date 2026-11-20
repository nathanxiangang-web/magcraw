package main

import (
	"encoding/csv"
	"encoding/json"
	_ "embed"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

//go:embed web/index.html
var indexHTML []byte

//go:embed web/app.js
var appJS []byte

//go:embed web/styles.css
var stylesCSS []byte

type hub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func newHub() *hub {
	return &hub{subs: make(map[chan []byte]struct{})}
}

func (h *hub) subscribe() chan []byte {
	ch := make(chan []byte, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *hub) unsubscribe(ch chan []byte) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

func (h *hub) broadcast(event string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	frame := []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, payload))
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- frame:
		default:
			// 慢客户端丢弃该帧，避免阻塞抓取流程
		}
	}
}

type server struct {
	hub   *hub
	crawl *crawler
	api   *apiV1

	jobMu    sync.Mutex
	current  *Job
	jobs     map[string]*Job
	jobOrder []string
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /app.js", s.handleAppJS)
	mux.HandleFunc("GET /styles.css", s.handleStylesCSS)
	mux.HandleFunc("POST /api/job", s.handleCreateJob)
	mux.HandleFunc("POST /api/job/{id}/stop", s.handleStopJob)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("GET /api/items", s.handleItems)
	mux.HandleFunc("GET /api/export", s.handleExport)
	if s.api != nil {
		s.api.routes(mux)
	}
	// 预检请求不进入 ServeMux：Go 1.22 的 "GET /path" 模式对 OPTIONS 会直接 405。
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.api != nil && strings.HasPrefix(r.URL.Path, "/api/v1/") {
			s.api.applyCORS(w, r)
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}

func (s *server) handleAppJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write(appJS)
}

func (s *server) handleStylesCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write(stylesCSS)
}

type jobRequest struct {
	Keywords []string `json:"keywords"`
	Hot      int      `json:"hot"`
	Limit    int      `json:"limit"`
	Detail   *bool    `json:"detail"`
}

type errorResponse struct {
	Error string `json:"error"`
	JobID string `json:"job_id,omitempty"`
}

type itemsResponse struct {
	JobID string `json:"job_id"`
	Items []Item `json:"items"`
}

func (s *server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	req, errCode := parseJobRequest(r)
	if errCode != "" {
		writeJSONError(w, http.StatusBadRequest, errCode, "")
		return
	}
	j, runningID := s.startJob(req.Keywords, req.Hot, req.Limit, *req.Detail)
	if j == nil {
		writeJSONError(w, http.StatusConflict, "job_running", runningID)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"job_id": j.ID})
}

func (s *server) handleStopJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.jobMu.Lock()
	j := s.current
	s.jobMu.Unlock()

	stopped := j != nil && j.ID == id && j.requestStop()
	writeJSON(w, http.StatusOK, map[string]bool{"stopped": stopped})
}

func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fl.Flush()

	ch := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)

	s.writeCurrentStatus(w, fl)

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case frame := <-ch:
			_, _ = w.Write(frame)
			fl.Flush()
		case <-ticker.C:
			_, _ = io.WriteString(w, ": ping\n\n")
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *server) writeCurrentStatus(w io.Writer, fl http.Flusher) {
	s.jobMu.Lock()
	j := s.current
	s.jobMu.Unlock()

	status := statusPayload{State: "idle"}
	if j != nil {
		status = j.snapshotStatus()
	}
	payload, err := json.Marshal(status)
	if err != nil {
		return
	}
	_, _ = io.WriteString(w, "event: status\ndata: "+string(payload)+"\n\n")
	fl.Flush()
}

func (s *server) snapshot() (string, []Item) {
	s.jobMu.Lock()
	j := s.current
	s.jobMu.Unlock()
	if j == nil {
		return "", []Item{}
	}
	return j.ID, j.snapshotItems()
}

func (s *server) handleItems(w http.ResponseWriter, r *http.Request) {
	id, items := s.snapshot()
	writeJSON(w, http.StatusOK, itemsResponse{JobID: id, Items: items})
}

func (s *server) handleExport(w http.ResponseWriter, r *http.Request) {
	id, items := s.snapshot()
	name := "magnet-items"
	if id != "" {
		name = "magnet-" + id
	}

	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.csv"`)
		_, _ = w.Write([]byte("\xEF\xBB\xBF"))
		cw := csv.NewWriter(w)
		cw.UseCRLF = true
		_ = cw.Write([]string{"keyword", "title", "filename", "list_size", "size", "date", "btih", "magnet"})
		for _, it := range items {
			_ = cw.Write([]string{
				it.Keyword, it.Title, it.Filename, it.ListSize,
				it.Size, it.Date, it.Btih, it.Magnet,
			})
		}
		cw.Flush()
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.json"`)
	_ = json.NewEncoder(w).Encode(items)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, errCode, jobID string) {
	writeJSON(w, status, errorResponse{Error: errCode, JobID: jobID})
}

func main() {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8900"
	}

	s := &server{hub: newHub(), crawl: newCrawler()}
	s.api = newAPIV1(s)
	log.Printf("magnet-crawler listening on :%s", port)
	if err := http.ListenAndServe(":"+port, s.routes()); err != nil {
		log.Fatal(err)
	}
}