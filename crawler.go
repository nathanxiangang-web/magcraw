package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Item 是 C-ITEM@1 的逐字结构，缺失字段一律空串。
type Item struct {
	Keyword  string `json:"keyword"`
	Page     string `json:"page"`
	Title    string `json:"title"`
	Filename string `json:"filename"`
	ListSize string `json:"list_size"`
	Size     string `json:"size"`
	Date     string `json:"date"`
	Btih     string `json:"btih"`
	Magnet   string `json:"magnet"`
}

type statusPayload struct {
	JobID   string `json:"job_id"`
	State   string `json:"state"`
	Stage   string `json:"stage"`
	Keyword string `json:"keyword"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	Note    string `json:"note"`
}

type delayPayload struct {
	Seconds float64 `json:"seconds"`
	Reason  string  `json:"reason"`
}

type donePayload struct {
	JobID string `json:"job_id"`
	Count int    `json:"count"`
	Total int    `json:"total"`
	State string `json:"state"`
}

type emitter func(event string, data any)

var errStopped = errors.New("stopped")

type Job struct {
	ID       string
	Keywords []string
	Hot      int
	Limit    int
	Detail   bool
	Created  time.Time

	mu            sync.Mutex
	hotWords      []string
	state         string
	stage         string
	keyword       string
	done          int
	total         int
	note          string
	stopRequested bool
	ended         bool
	filtered      int
	items         []Item
}

func newJob(keywords []string, hot, limit int, detail bool) *Job {
	return &Job{
		ID:       "j" + strconv.FormatInt(time.Now().UnixMilli(), 10),
		Keywords: keywords,
		Hot:      hot,
		Limit:    limit,
		Detail:   detail,
		Created:  time.Now(),
		state:    "running",
		total:    limit,
	}
}

func (j *Job) finished() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.ended
}

func (j *Job) createdAt() string {
	return j.Created.Format(time.RFC3339)
}

func (j *Job) setHot(words []string) {
	j.mu.Lock()
	j.hotWords = words
	j.mu.Unlock()
}

func (j *Job) snapshotHot() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.hotWords == nil {
		return []string{}
	}
	out := make([]string, len(j.hotWords))
	copy(out, j.hotWords)
	return out
}

func (j *Job) addFiltered() {
	j.mu.Lock()
	j.filtered++
	j.mu.Unlock()
}

func (j *Job) filteredCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.filtered
}

func (j *Job) snapshotStatus() statusPayload {
	j.mu.Lock()
	defer j.mu.Unlock()
	return statusPayload{
		JobID:   j.ID,
		State:   j.state,
		Stage:   j.stage,
		Keyword: j.keyword,
		Done:    j.done,
		Total:   j.total,
		Note:    j.note,
	}
}

func (j *Job) isRunning() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return !j.ended
}

func (j *Job) stopped() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.stopRequested
}

func (j *Job) requestStop() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.ended {
		return false
	}
	j.stopRequested = true
	return true
}

func (j *Job) setStage(stage, keyword string) {
	j.mu.Lock()
	j.stage = stage
	j.keyword = keyword
	j.mu.Unlock()
}

func (j *Job) setNote(note string) {
	j.mu.Lock()
	j.note = note
	j.mu.Unlock()
}

func (j *Job) addItem(it Item) {
	j.mu.Lock()
	j.items = append(j.items, it)
	j.done = len(j.items)
	j.mu.Unlock()
}

func (j *Job) count() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.done
}

func (j *Job) finish(state string) {
	j.mu.Lock()
	j.state = state
	j.ended = true
	j.mu.Unlock()
}

func (j *Job) snapshotItems() []Item {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.items) == 0 {
		return []Item{}
	}
	out := make([]Item, len(j.items))
	copy(out, j.items)
	return out
}

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
}

type crawler struct {
	client *http.Client
	base   string
	uaSeq  atomic.Uint64
}

func newCrawler() *crawler {
	transport := &http.Transport{}
	if proxy := strings.TrimSpace(os.Getenv("CI_PROXY")); proxy != "" {
		if u, err := url.Parse(proxy); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}

	base := strings.TrimRight(strings.TrimSpace(os.Getenv("CI_BASE")), "/")
	if base == "" {
		base = "https://cilisousuo.co"
	}

	return &crawler{
		client: &http.Client{Transport: transport, Timeout: 30 * time.Second},
		base:   base,
	}
}

// wait 先广播 delay 事件，再按该秒数实际休眠（3.000–15.000 秒均匀分布）。
func (c *crawler) wait(j *Job, emit emitter, reason string) {
	seconds := math.Round((3+rand.Float64()*12)*100) / 100
	emit("delay", delayPayload{Seconds: seconds, Reason: reason})
	time.Sleep(time.Duration(seconds * float64(time.Second)))
}

func (c *crawler) get(j *Job, emit emitter, path, reason string) (string, error) {
	if j.stopped() {
		return "", errStopped
	}
	c.wait(j, emit, reason)
	if j.stopped() {
		return "", errStopped
	}

	req, err := http.NewRequest(http.MethodGet, c.base+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgents[int(c.uaSeq.Add(1))%len(userAgents)])
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (c *crawler) fetchHTML(j *Job, emit emitter, path string) (string, error) {
	body, err := c.get(j, emit, path, "request")
	if err == nil || errors.Is(err, errStopped) {
		return body, err
	}
	first := err

	body, err = c.get(j, emit, path, "retry")
	if err == nil || errors.Is(err, errStopped) {
		return body, err
	}
	return "", fmt.Errorf("%v; retry: %v", first, err)
}

func (c *crawler) run(j *Job, emit emitter) {
	state := "done"
	failStreak := 0
	defer func() {
		if n := j.filteredCount(); n > 0 && state == "done" {
			j.setNote(strconv.Itoa(n) + " 条被过滤（广告 / 小于100MB / 非 mp4）")
		}
		j.finish(state)
		emit("status", j.snapshotStatus())
		emit("done", donePayload{JobID: j.ID, Count: j.count(), Total: j.Limit, State: state})
		persistJob(j)
	}()

	emit("status", j.snapshotStatus())

	keywords := j.Keywords
	if len(keywords) == 0 {
		j.setStage("hot", "")
		emit("status", j.snapshotStatus())

		body, err := c.fetchHTML(j, emit, "/search/hot")
		if errors.Is(err, errStopped) {
			state = "stopped"
			return
		}
		if err != nil {
			if fail(j, emit, &failStreak, err) {
				state = "error"
			}
			return
		}
		keywords = parseHotKeywords(body, j.Hot)
		j.setHot(keywords)
		if len(keywords) == 0 {
			if fail(j, emit, &failStreak, errors.New("hot list empty")) {
				state = "error"
			}
			return
		}
		failStreak = 0
	}

	for _, kw := range keywords {
		if j.stopped() {
			state = "stopped"
			return
		}
		if j.count() >= j.Limit {
			break
		}

		j.setStage("list", kw)
		emit("status", j.snapshotStatus())

		body, err := c.fetchHTML(j, emit, "/search?q="+url.QueryEscape(kw))
		if errors.Is(err, errStopped) {
			state = "stopped"
			return
		}
		if err != nil {
			if fail(j, emit, &failStreak, err) {
				state = "error"
				return
			}
			continue
		}

		entries := parseListPage(body)
		if len(entries) == 0 {
			if fail(j, emit, &failStreak, errors.New("no entries parsed from list page")) {
				state = "error"
				return
			}
			continue
		}
		failStreak = 0

		for _, e := range entries {
			if j.stopped() {
				state = "stopped"
				return
			}
			if j.count() >= j.Limit {
				break
			}
			if !j.Detail {
				// 无详情页就拿不到磁链，而无磁链的条目不上屏、不入导出
				continue
			}
			// 列表页就能判定的两项：标题/文件名里的广告、明显小于阈值的种子
			if isAdText(e.Title) || isAdText(e.Filename) {
				j.addFiltered()
				continue
			}
			if n := sizeToBytes(e.ListSize); n > 0 && n < minSeedBytes {
				j.addFiltered()
				continue
			}

			j.setStage("detail", kw)
			emit("status", j.snapshotStatus())

			detailBody, err := c.fetchHTML(j, emit, "/magnet/"+url.PathEscape(e.Page))
			if errors.Is(err, errStopped) {
				state = "stopped"
				return
			}
			if err != nil {
				if fail(j, emit, &failStreak, err) {
					state = "error"
					return
				}
				continue
			}

			detail := parseDetailPage(detailBody)
			if detail.Magnet == "" {
				j.setNote("no magnet on " + e.Page)
				emit("status", j.snapshotStatus())
				failStreak = 0
				continue
			}
			if reason := filterSeed(e, detail); reason != "" {
				j.addFiltered()
				j.setNote(reason + " on " + e.Page)
				emit("status", j.snapshotStatus())
				failStreak = 0
				continue
			}
			failStreak = 0

			item := Item{
				Keyword:  kw,
				Page:     e.Page,
				Title:    e.Title,
				Filename: e.Filename,
				ListSize: e.ListSize,
				Size:     detail.Size,
				Date:     detail.Date,
				Btih:     detail.Btih,
				Magnet:   detail.Magnet,
			}
			j.addItem(item)
			emit("item", item)
			emit("status", j.snapshotStatus())
		}
	}
}

func fail(j *Job, emit emitter, streak *int, err error) bool {
	*streak = *streak + 1
	j.setNote(err.Error())
	emit("status", j.snapshotStatus())
	return *streak >= 3
}

func persistJob(j *Job) {
	items := j.snapshotItems()
	if err := os.MkdirAll("data", 0o755); err != nil {
		return
	}
	body, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return
	}
	name := "job-" + strings.TrimPrefix(j.ID, "j") + ".json"
	_ = os.WriteFile(filepath.Join("data", name), body, 0o644)
}

var (
	// 列表页每个结果是一个 <li class="item"> 块，字段在块内、磁链锚点（<a class="link" href="/magnet/{id}">）在块尾
	listItemRe  = regexp.MustCompile(`(?is)<li[^>]*class="[^"]*\bitem\b[^"]*"[^>]*>(.*?)</li>`)
	listIDRe    = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*\blink\b[^"]*"[^>]*href="(?:https?://[^/"]+)?/magnet/([^"/?#]+)"`)
	listTitleRe = regexp.MustCompile(`(?is)<div[^>]*class="[^"]*result-title[^"]*"[^>]*>(.*?)</div>`)
	listFileRe  = regexp.MustCompile(`(?is)<div[^>]*class="[^"]*filename[^"]*"[^>]*>(.*?)</div>`)
	listSizeRe  = regexp.MustCompile(`(?is)<div[^>]*class="[^"]*\bsize\b[^"]*"[^>]*>(.*?)</div>`)
	hotKeywordRe = regexp.MustCompile(`(?is)href="[^"]*[?&]q=([^"&]+)[^"]*"`)
	sizeRe       = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?\s*(?:[KMGT]i?B|[KMGT]B))`)
	detailSizeRe = regexp.MustCompile(`(?is)(?:文件大小|文件尺寸|大小|file\s*size|size)\s*[:：]?\s*(\d+(?:\.\d+)?\s*(?:[KMGT]i?B|[KMGT]B))`)
	dateRe       = regexp.MustCompile(`(\d{4}-\d{2}-\d{2})`)
	magnetRe     = regexp.MustCompile(`(?i)magnet:\?[^"'<>\s]+`)
	btihRe       = regexp.MustCompile(`(?i)urn:btih:([0-9a-z]+)`)
	tagRe        = regexp.MustCompile(`(?s)<[^>]*>`)
)

type listEntry struct {
	Page     string
	Title    string
	Filename string
	ListSize string
}

type detailInfo struct {
	Magnet string
	Btih   string
	Size   string
	Date   string
	Files  []string
}

func cleanText(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(html.UnescapeString(s))
}

func firstMatch(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(html.UnescapeString(m[1]))
}

func parseHotKeywords(body string, n int) []string {
	if n <= 0 {
		return nil
	}
	matches := hotKeywordRe.FindAllStringSubmatch(body, -1)
	out := make([]string, 0, n)
	seen := make(map[string]bool, len(matches))
	for _, m := range matches {
		kw, err := url.QueryUnescape(m[1])
		if err != nil {
			kw = m[1]
		}
		kw = strings.TrimSpace(html.UnescapeString(kw))
		if kw == "" || seen[kw] {
			continue
		}
		seen[kw] = true
		out = append(out, kw)
		if len(out) >= n {
			break
		}
	}
	return out
}

func parseListPage(body string) []listEntry {
	blocks := listItemRe.FindAllStringSubmatch(body, -1)
	entries := make([]listEntry, 0, len(blocks))
	seen := make(map[string]bool, len(blocks))
	for _, b := range blocks {
		blk := b[1]
		page := firstMatch(listIDRe, blk)
		if page == "" || seen[page] {
			continue
		}
		seen[page] = true

		title := tidyName(cleanText(firstMatch(listTitleRe, blk)))
		filename := tidyName(cleanText(firstMatch(listFileRe, blk)))
		if filename == "" {
			filename = title
		}

		entries = append(entries, listEntry{
			Page:     page,
			Title:    title,
			Filename: filename,
			ListSize: cleanText(firstMatch(listSizeRe, blk)),
		})
	}
	return entries
}

func parseDetailPage(body string) detailInfo {
	var d detailInfo
	if raw := magnetRe.FindString(body); raw != "" {
		// 详情页把 & 转义成 &amp;，契约要求还原为完整磁链
		d.Magnet = html.UnescapeString(raw)
	}
	if btih := btihRe.FindStringSubmatch(d.Magnet); len(btih) == 2 {
		d.Btih = strings.ToLower(btih[1])
	}
	d.Size = firstMatch(detailSizeRe, body)
	if d.Size == "" {
		d.Size = firstMatch(sizeRe, body)
	}
	d.Date = firstMatch(dateRe, body)
	d.Files = parseFileList(body)
	return d
}

// minSeedBytes 是用户要求的种子总大小下限：100MB。
const minSeedBytes = 100 << 20

var (
	fileRowRe   = regexp.MustCompile(`(?is)<tr[^>]*>\s*<td[^>]*>(.*?)</td>`)
	emailMaskRe = regexp.MustCompile(`(?i)\[email[^]]*protected\]`)
	domainRe    = regexp.MustCompile(`(?i)[a-z0-9][a-z0-9-]{1,62}\.(?:xyz|com|cc|top|vip|net|org|io|me|tv|club|shop|info|cn|co|link)\b`)
)

// adWords 为站点常见的推广话术，命中即视为广告条目。
var adWords = []string{
	"手机搜", "加群", "发布页", "最新地址", "永久域名", "防止失联", "备用地址",
	"导航", "官网", "福利", "扫码", "收费", "邀请注册", "免费观看", "在线看", "下载",
}

// tidyName 清掉站点对邮箱的 Cloudflare 混淆占位与多余分隔符。
func tidyName(s string) string {
	s = emailMaskRe.ReplaceAllString(strings.ReplaceAll(s, "\u00a0", " "), " ")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(strings.TrimRight(s, "/ "))
}

func parseFileList(body string) []string {
	var out []string
	for _, m := range fileRowRe.FindAllStringSubmatch(body, -1) {
		name := cleanText(m[1])
		if name == "" || sizeRe.MatchString(name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

func isAdText(s string) bool {
	if s == "" {
		return false
	}
	t := emailMaskRe.ReplaceAllString(strings.ToLower(strings.ReplaceAll(s, "\u00a0", " ")), " ")
	if domainRe.MatchString(t) {
		return true
	}
	for _, w := range adWords {
		if strings.Contains(t, w) {
			return true
		}
	}
	return false
}

func sizeToBytes(s string) int64 {
	m := sizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if len(m) < 2 {
		return 0
	}
	v := strings.ToUpper(strings.TrimSpace(m[1]))
	num, mult := v, int64(1)
	for _, u := range []struct {
		suffix string
		mul    int64
	}{
		{"TIB", 1 << 40}, {"TB", 1 << 40}, {"GIB", 1 << 30}, {"GB", 1 << 30},
		{"MIB", 1 << 20}, {"MB", 1 << 20}, {"KIB", 1 << 10}, {"KB", 1 << 10}, {"B", 1},
	} {
		if strings.HasSuffix(v, u.suffix) {
			mult = u.mul
			num = strings.TrimSpace(strings.TrimSuffix(v, u.suffix))
			break
		}
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0
	}
	return int64(f * float64(mult))
}

func hasMp4(files []string) bool {
	for _, f := range files {
		if strings.HasSuffix(strings.ToLower(strings.TrimSpace(f)), ".mp4") {
			return true
		}
	}
	return false
}

// filterSeed 返回非空字符串表示该条应被剔除（原因是给人看的）。
// 规则：只要 mp4、种子总大小 > 100MB、剔除广告。
func filterSeed(e listEntry, d detailInfo) string {
	if isAdText(e.Title) || isAdText(e.Filename) {
		return "ad"
	}
	size := sizeToBytes(d.Size)
	if size == 0 {
		size = sizeToBytes(e.ListSize)
	}
	switch {
	case size == 0:
		return "size unknown"
	case size < minSeedBytes:
		return "under 100MB"
	}
	if hasMp4(d.Files) {
		return ""
	}
	if len(d.Files) == 0 && strings.Contains(strings.ToLower(e.Filename), ".mp4") {
		return ""
	}
	if len(d.Files) == 0 {
		return "file list unavailable"
	}
	return "no mp4"
}