package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

// v1 是对外（第三方网站）调用的稳定接口层：CORS + API Key + 限流 + 按 job_id 回查。
// 页面用的 /api/* 保持原样，不加鉴权，只监听本机/局域网。

const (
	maxSyncWaitSeconds = 120
	maxSyncLimit       = 5
	jobHistorySize     = 20
)

// startJob 创建并启动任务；返回 (nil, runningID) 表示已有任务在跑。
func (s *server) startJob(keywords []string, hot, limit int, detail bool) (*Job, string) {
	s.jobMu.Lock()
	if s.current != nil && s.current.isRunning() {
		id := s.current.ID
		s.jobMu.Unlock()
		return nil, id
	}
	j := newJob(keywords, hot, limit, detail)
	s.current = j
	if s.jobs == nil {
		s.jobs = make(map[string]*Job)
	}
	s.jobs[j.ID] = j
	s.jobOrder = append(s.jobOrder, j.ID)
	for len(s.jobOrder) > jobHistorySize {
		delete(s.jobs, s.jobOrder[0])
		s.jobOrder = s.jobOrder[1:]
	}
	s.jobMu.Unlock()

	go s.crawl.run(j, s.hub.broadcast)
	return j, ""
}

func (s *server) jobByID(id string) *Job {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	if j := s.jobs[id]; j != nil {
		return j
	}
	if s.current != nil && s.current.ID == id {
		return s.current
	}
	return nil
}

// ---- CORS / 鉴权 / 限流 ----

type apiV1 struct {
	s       *server
	key     string
	origins []string
	rl      *rateLimiter
}

func newAPIV1(s *server) *apiV1 {
	a := &apiV1{s: s, key: strings.TrimSpace(os.Getenv("CI_API_KEY"))}
	if raw := strings.TrimSpace(os.Getenv("CI_CORS_ORIGINS")); raw != "" {
		for _, o := range strings.Split(raw, ",") {
			if o = strings.TrimSpace(o); o != "" {
				a.origins = append(a.origins, o)
			}
		}
	}
	perMin := 60
	if v := strings.TrimSpace(os.Getenv("CI_RATE_LIMIT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			perMin = n
		}
	}
	if perMin > 0 {
		a.rl = newRateLimiter(perMin, time.Minute)
	}
	return a
}

func (a *apiV1) routes(mux *http.ServeMux) {
	g := a.guard
	mux.HandleFunc("GET /api/v1/health", g(a.handleHealth))
	mux.HandleFunc("GET /api/v1/openapi.json", g(a.handleOpenAPI))
	mux.HandleFunc("POST /api/v1/jobs", g(a.handleCreateJob))
	mux.HandleFunc("GET /api/v1/jobs/{id}", g(a.handleJobStatus))
	mux.HandleFunc("GET /api/v1/jobs/{id}/results", g(a.handleJobResults))
	mux.HandleFunc("GET /api/v1/jobs/{id}/events", g(a.handleJobEvents))
	mux.HandleFunc("GET /api/v1/search", g(a.handleSyncSearch))
	mux.HandleFunc("GET /api/v1/hot", g(a.handleHot))
}

func (a *apiV1) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a.applyCORS(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if a.key != "" && !a.authorized(r) {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized", "")
			return
		}
		if a.rl != nil && !a.rl.allow(clientIP(r)) {
			writeJSONError(w, http.StatusTooManyRequests, "rate_limited", "")
			return
		}
		next(w, r)
	}
}

func (a *apiV1) applyCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	switch {
	case len(a.origins) == 0:
		w.Header().Set("Access-Control-Allow-Origin", "*")
	case origin != "" && contains(a.origins, origin):
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key, Authorization")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.Header().Set("X-API-Version", "v1")
}

func (a *apiV1) authorized(r *http.Request) bool {
	if strings.TrimSpace(r.Header.Get("X-API-Key")) == a.key {
		return true
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")) == a.key
	}
	return false
}

func contains(list []string, v string) bool {
	for _, it := range list {
		if it == v {
			return true
		}
	}
	return false
}

func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

type rateLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{hits: make(map[string][]time.Time), limit: limit, window: window}
}

func (l *rateLimiter) allow(key string) bool {
	now := time.Now()
	cut := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	kept := make([]time.Time, 0, len(l.hits[key])+1)
	for _, t := range l.hits[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)

	if len(l.hits) > 4096 {
		for k, v := range l.hits {
			if len(v) == 0 {
				delete(l.hits, k)
			}
		}
	}
	return true
}

// ---- handlers ----

type v1JobResponse struct {
	JobID       string `json:"job_id"`
	State       string `json:"state"`
	Stage       string `json:"stage"`
	Keyword     string `json:"keyword"`
	Done        int    `json:"done"`
	Total       int    `json:"total"`
	Count       int    `json:"count"`
	Note        string `json:"note"`
	HotKeywords []string `json:"hot_keywords,omitempty"`
	CreatedAt   string `json:"created_at"`
	ResultsURL  string `json:"results_url"`
	PollURL     string `json:"poll_url"`
}

type v1SearchResponse struct {
	JobID string `json:"job_id"`
	State string `json:"state"`
	Count int    `json:"count"`
	Items []Item `json:"items"`
}

func (a *apiV1) handleHealth(w http.ResponseWriter, r *http.Request) {
	a.s.jobMu.Lock()
	running := a.s.current != nil && a.s.current.isRunning()
	a.s.jobMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"api":           "v1",
		"job_running":   running,
		"auth_required": a.key != "",
	})
}

func (a *apiV1) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	req, errCode := parseJobRequest(r)
	if errCode != "" {
		writeJSONError(w, http.StatusBadRequest, errCode, "")
		return
	}
	j, runningID := a.s.startJob(req.Keywords, req.Hot, req.Limit, *req.Detail)
	if j == nil {
		writeJSONError(w, http.StatusConflict, "job_running", runningID)
		return
	}
	writeJSON(w, http.StatusAccepted, a.jobStatus(j))
}

func (a *apiV1) handleJobStatus(w http.ResponseWriter, r *http.Request) {
	j := a.s.jobByID(r.PathValue("id"))
	if j == nil {
		writeJSONError(w, http.StatusNotFound, "job_not_found", "")
		return
	}
	writeJSON(w, http.StatusOK, a.jobStatus(j))
}

func (a *apiV1) handleJobResults(w http.ResponseWriter, r *http.Request) {
	j := a.s.jobByID(r.PathValue("id"))
	if j == nil {
		writeJSONError(w, http.StatusNotFound, "job_not_found", "")
		return
	}
	st := j.snapshotStatus()
	writeJSON(w, http.StatusOK, v1SearchResponse{
		JobID: j.ID,
		State: st.State,
		Count: j.count(),
		Items: j.snapshotItems(),
	})
}

func (a *apiV1) handleJobEvents(w http.ResponseWriter, r *http.Request) {
	if j := a.s.jobByID(r.PathValue("id")); j == nil {
		writeJSONError(w, http.StatusNotFound, "job_not_found", "")
		return
	}
	a.s.handleEvents(w, r)
}

func (a *apiV1) handleSyncSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSONError(w, http.StatusBadRequest, "no_keywords", "")
		return
	}
	limit := intQuery(r, "limit", 2, 1, maxSyncLimit)
	wait := intQuery(r, "wait", 45, 5, maxSyncWaitSeconds)

	j, runningID := a.s.startJob([]string{q}, 0, limit, true)
	if j == nil {
		writeJSONError(w, http.StatusConflict, "job_running", runningID)
		return
	}
	a.waitJob(w, r, j, wait)
}

func (a *apiV1) handleHot(w http.ResponseWriter, r *http.Request) {
	n := intQuery(r, "n", 10, 1, 50)
	wait := intQuery(r, "wait", 45, 5, maxSyncWaitSeconds)

	j, runningID := a.s.startJob(nil, n, 1, false)
	if j == nil {
		writeJSONError(w, http.StatusConflict, "job_running", runningID)
		return
	}
	deadline := time.After(time.Duration(wait) * time.Second)
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			writeJSON(w, http.StatusGatewayTimeout, map[string]any{
				"error": "timeout", "job_id": j.ID, "keywords": j.snapshotHot(),
			})
			return
		case <-r.Context().Done():
			return
		case <-tick.C:
			if j.finished() {
				writeJSON(w, http.StatusOK, map[string]any{
					"job_id": j.ID, "count": len(j.snapshotHot()), "keywords": j.snapshotHot(),
				})
				return
			}
		}
	}
}

func (a *apiV1) waitJob(w http.ResponseWriter, r *http.Request, j *Job, waitSeconds int) {
	deadline := time.After(time.Duration(waitSeconds) * time.Second)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			st := j.snapshotStatus()
			writeJSON(w, http.StatusGatewayTimeout, v1SearchResponse{
				JobID: j.ID, State: st.State, Count: j.count(), Items: j.snapshotItems(),
			})
			return
		case <-r.Context().Done():
			return
		case <-tick.C:
			if j.finished() {
				st := j.snapshotStatus()
				writeJSON(w, http.StatusOK, v1SearchResponse{
					JobID: j.ID, State: st.State, Count: j.count(), Items: j.snapshotItems(),
				})
				return
			}
		}
	}
}

func (a *apiV1) jobStatus(j *Job) v1JobResponse {
	st := j.snapshotStatus()
	base := "/api/v1/jobs/" + j.ID
	return v1JobResponse{
		JobID:       j.ID,
		State:       st.State,
		Stage:       st.Stage,
		Keyword:     st.Keyword,
		Done:        st.Done,
		Total:       st.Total,
		Count:       j.count(),
		Note:        st.Note,
		HotKeywords: j.snapshotHot(),
		CreatedAt:   j.createdAt(),
		ResultsURL:  base + "/results",
		PollURL:     base,
	}
}

func parseJobRequest(r *http.Request) (jobRequest, string) {
	var req jobRequest
	if err := decodeJSON(r, &req); err != nil {
		return req, "bad_request"
	}
	keywords := make([]string, 0, len(req.Keywords))
	for _, k := range req.Keywords {
		if k = strings.TrimSpace(k); k != "" {
			keywords = append(keywords, k)
		}
	}
	if len(keywords) == 0 && req.Hot == 0 {
		return req, "no_keywords"
	}
	if req.Limit < 1 || req.Limit > 24 {
		return req, "bad_limit"
	}
	if req.Hot < 0 || req.Hot > 50 {
		return req, "bad_hot"
	}
	req.Keywords = keywords
	if req.Detail == nil {
		t := true
		req.Detail = &t
	}
	return req, ""
}

func intQuery(r *http.Request, key string, def, min, max int) int {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

// handleOpenAPI 给对接方一份机器可读的接口描述（精简版）。
func (a *apiV1) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	items := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"keyword":   map[string]any{"type": "string"},
			"page":      map[string]any{"type": "string"},
			"title":     map[string]any{"type": "string"},
			"filename":  map[string]any{"type": "string"},
			"list_size": map[string]any{"type": "string"},
			"size":      map[string]any{"type": "string"},
			"date":      map[string]any{"type": "string"},
			"btih":      map[string]any{"type": "string"},
			"magnet":    map[string]any{"type": "string", "description": "完整 magnet 链接"},
		},
	}
	params := []map[string]any{{"name": "q", "in": "query", "required": true, "schema": map[string]any{"type": "string"}}}
	writeJSON(w, http.StatusOK, map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "magnet-crawler API",
			"version":     "v1",
			"description": "抓取结果统一按规则过滤：仅 .mp4、单种子 >100MB、剔除广告。任务串行执行，每次上游请求前随机等待 3–15 秒。",
		},
		"servers": []map[string]any{{"url": "http://127.0.0.1:8900"}},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"ApiKeyAuth": map[string]any{"type": "apiKey", "in": "header", "name": "X-API-Key"},
			},
			"schemas": map[string]any{"Item": items},
		},
		"security": []map[string]any{{"ApiKeyAuth": []string{}}},
		"paths": map[string]any{
			"/api/v1/health": map[string]any{"get": map[string]any{"summary": "健康检查"}},
			"/api/v1/jobs": map[string]any{"post": map[string]any{
				"summary": "创建抓取任务（异步）",
				"requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{
					"schema": map[string]any{"type": "object", "properties": map[string]any{
						"keywords": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"hot":      map[string]any{"type": "integer", "minimum": 0, "maximum": 50},
						"limit":    map[string]any{"type": "integer", "minimum": 1, "maximum": 24},
						"detail":   map[string]any{"type": "boolean", "default": true},
					}},
				}}},
				"responses": map[string]any{"202": map[string]any{"description": "任务已创建"}, "409": map[string]any{"description": "已有任务在跑"}},
			}},
			"/api/v1/jobs/{id}": map[string]any{"get": map[string]any{"summary": "查询任务状态（轮询）"}},
			"/api/v1/jobs/{id}/results": map[string]any{"get": map[string]any{"summary": "取任务结果", "responses": map[string]any{
				"200": map[string]any{"description": "OK", "content": map[string]any{"application/json": map[string]any{
					"schema": map[string]any{"type": "object", "properties": map[string]any{
						"job_id": map[string]any{"type": "string"},
						"state":  map[string]any{"type": "string"},
						"count":  map[string]any{"type": "integer"},
						"items":  map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/Item"}},
					}},
				}}},
			}}},
			"/api/v1/jobs/{id}/events": map[string]any{"get": map[string]any{"summary": "SSE 实时事件"}},
			"/api/v1/search": map[string]any{"get": map[string]any{
				"summary": "同步抓取（最长 120 秒，limit ≤ 5）",
				"parameters": append(params,
					map[string]any{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer", "default": 2, "maximum": 5}},
					map[string]any{"name": "wait", "in": "query", "schema": map[string]any{"type": "integer", "default": 45, "maximum": 120}},
				),
			}},
			"/api/v1/hot": map[string]any{"get": map[string]any{"summary": "热榜关键词"}},
		},
	})
}