// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/realtime"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// ---- KB-010: suggest ----

func TestKBSuggest_RespectsVisibility(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		visibilityFixture(e)
		var got []summaryDTO
		r := e.do("GET", "/api/v1/portal/kb/suggest?q=router", "customer", nil)
		e.want(r, 200)
		r.json(t, &got)
		var slugs []string
		for _, s := range got {
			slugs = append(slugs, s.Slug)
			if utf8.RuneCountInString(s.Excerpt) > excerptLen {
				t.Errorf("excerpt %d runes > %d", utf8.RuneCountInString(s.Excerpt), excerptLen)
			}
		}
		sort.Strings(slugs)
		if !slices.Equal(slugs, []string{"cust-pub", "pub-cust", "pub-pub"}) {
			t.Fatalf("customer suggest %v", slugs)
		}
		// Below 3 characters: no query, empty list.
		e.do("GET", "/api/v1/portal/kb/suggest?q=ro", "customer", nil).json(t, &got)
		if len(got) != 0 {
			t.Fatalf("short query returned %v", got)
		}
		// Staff and anonymous never reach the portal route (I-1 / 401).
		e.guard("GET", "/api/v1/portal/kb/suggest?q=router", "agent", nil, 404)
		e.want(e.do("GET", "/api/v1/portal/kb/suggest?q=router", "", nil), 401)
		// The cache never outlives a visibility change.
		e.do("GET", "/api/v1/portal/kb/suggest?q=warranty", "customer", nil).json(t, &got)
		if len(got) != 1 {
			t.Fatalf("warranty %v", got)
		}
		id := slugID(e, "cust-pub")
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+id, "supervisor", map[string]any{"visibility": "internal"}), 200)
		e.do("GET", "/api/v1/portal/kb/suggest?q=warranty", "customer", nil).json(t, &got)
		if len(got) != 0 {
			t.Fatalf("stale cache leaked a now-internal article: %v", got)
		}
	})
}

func TestKBSuggest_RateLimited429(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		cat := e.category("rl", VisPublic, "")
		e.article(cat, "rate", VisPublic, "published", "Rate limit", "x")
		limited := 0
		for i := 0; i < 30; i++ {
			r := e.do("GET", "/api/v1/portal/kb/suggest?q=rate", "customer", nil)
			if r.Code == http.StatusTooManyRequests {
				limited++
				if r.Header().Get("Retry-After") == "" {
					t.Fatal("429 without Retry-After")
				}
			}
		}
		if limited == 0 {
			t.Fatal("30 instant requests must exceed the 10/s burst-20 limit")
		}
	})
}

// TestKBSuggest_P95Under30ms: on the 5k-article × 3-locale fixture, uncached
// suggest queries answer with p95 < 30 ms on both engines. The fixture must
// also build in < 10 s (KB-015).
func TestKBSuggest_P95Under30ms(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("perf fixture (skipped with -short and -race)")
	}
	engines(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		start := time.Now()
		if err := SeedPerf(ctx, e.db, 5000, []string{"en", "it", "de"}); err != nil {
			t.Fatal(err)
		}
		build := time.Since(start)
		if build > 10*time.Second {
			t.Errorf("perf fixture built in %v (> 10 s)", build)
		}
		if e.db.Dialect == store.Postgres {
			_, _ = e.db.W.ExecContext(ctx, `ANALYZE`)
		}
		r := rand.New(rand.NewPCG(3, 4))
		p := e.p("customer")
		// warm the connection pools and statement caches
		for i := 0; i < 5; i++ {
			_, _ = e.m.Suggest(ctx, p, PerfVocabulary[i], "en", false)
		}
		var durs []time.Duration
		hits := 0
		for i := 0; i < 300; i++ {
			q := PerfVocabulary[r.IntN(len(PerfVocabulary))]
			switch i % 4 { // whole words, prefixes while typing, two words
			case 1:
				q = q[:3]
			case 2:
				q = q[:4]
			case 3:
				q += " " + PerfVocabulary[r.IntN(len(PerfVocabulary))][:3]
			}
			t0 := time.Now()
			items, err := e.m.Suggest(ctx, p, q, "en", false)
			durs = append(durs, time.Since(t0))
			if err != nil {
				t.Fatal(err)
			}
			if len(items) > 0 {
				hits++
			}
			for _, it := range items {
				if it.Visibility == VisInternal || it.Status != "published" {
					t.Fatalf("suggest leaked %s/%s", it.Visibility, it.Status)
				}
			}
		}
		sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
		p50, p95 := durs[len(durs)/2], durs[len(durs)*95/100]
		t.Logf("%s: fixture %v, suggest p50 %v p95 %v, %d/%d queries with hits", e.db.Dialect, build.Round(time.Millisecond), p50, p95, hits, len(durs))
		if p95 > 30*time.Millisecond {
			t.Errorf("suggest p95 %v ≥ 30 ms", p95)
		}
		if hits < len(durs)/2 {
			t.Errorf("only %d/%d queries matched; the benchmark would be meaningless", hits, len(durs))
		}
	})
}

func BenchmarkKBSuggest(b *testing.B) {
	db := storetest.Open(b, store.SQLite)
	m, err := New(Deps{DB: db})
	if err != nil {
		b.Fatal(err)
	}
	if err := SeedPerf(context.Background(), db, 5000, []string{"en", "it", "de"}); err != nil {
		b.Fatal(err)
	}
	p := &domain.Principal{UserID: "c", Kind: domain.KindCustomer}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = m.Suggest(context.Background(), p, PerfVocabulary[i%len(PerfVocabulary)], "en", false)
	}
}

// ---- KB-011 / KB-012: counters ----

func TestKBFeedback_CoalescedWrites(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		cat := e.category("fb", VisPublic, "")
		id := e.article(cat, "helpful", VisPublic, "published", "Helpful?", "x")
		for i := 0; i < 1000; i++ {
			e.want(e.do("POST", "/api/v1/kb/articles/"+id+"/feedback", "", map[string]any{"helpful": i%5 != 0}, "X-Test-IP", fmt.Sprintf("10.%d.%d.1", i/250, i%250)), 204)
		}
		var yes, no int64
		_ = e.db.Read().QueryRowContext(ctx, `SELECT helpful_yes, helpful_no FROM kb_articles WHERE id = ?`, id).Scan(&yes, &no)
		if yes != 0 || no != 0 || e.m.counters.Flushes.Load() != 0 {
			t.Fatalf("votes written before the flush window: %d/%d, %d flushes", yes, no, e.m.counters.Flushes.Load())
		}
		if err := e.m.FlushCounters(ctx); err != nil {
			t.Fatal(err)
		}
		if f := e.m.counters.Flushes.Load(); f != 1 {
			t.Fatalf("1000 votes took %d flush transactions, want 1", f)
		}
		_ = e.db.Read().QueryRowContext(ctx, `SELECT helpful_yes, helpful_no FROM kb_articles WHERE id = ?`, id).Scan(&yes, &no)
		if yes != 800 || no != 200 {
			t.Fatalf("counters %d/%d, want 800/200", yes, no)
		}
		// Staff see the counters in the editor.
		var a articleDTO
		e.do("GET", "/api/v1/agent/kb/articles/"+id, "agent", nil).json(t, &a)
		if a.HelpfulYes == nil || *a.HelpfulYes != 800 || *a.HelpfulNo != 200 {
			t.Fatalf("editor counters %+v", a)
		}
		// Nothing pending → no transaction.
		_ = e.m.FlushCounters(ctx)
		if f := e.m.counters.Flushes.Load(); f != 1 {
			t.Fatalf("empty flush wrote: %d", f)
		}
	})
}

func TestKBFeedback_DuplicateVoteIgnored(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		cat := e.category("dup", VisPublic, "")
		id := e.article(cat, "dup-vote", VisPublic, "published", "Dup", "x")
		for i := 0; i < 3; i++ {
			e.want(e.do("POST", "/api/v1/kb/articles/"+id+"/feedback", "", map[string]any{"helpful": true}), 204)
		}
		e.want(e.do("POST", "/api/v1/kb/articles/"+id+"/feedback", "", map[string]any{"helpful": false}, "X-Test-IP", "203.0.113.9"), 204)
		_ = e.m.FlushCounters(ctx)
		var yes, no int64
		_ = e.db.Read().QueryRowContext(ctx, `SELECT helpful_yes, helpful_no FROM kb_articles WHERE id = ?`, id).Scan(&yes, &no)
		if yes != 1 || no != 1 {
			t.Fatalf("one vote per IP per article: got %d/%d", yes, no)
		}
		// Per-IP rate limit (1/s, burst 10).
		limited := false
		for i := 0; i < 15; i++ {
			if e.do("POST", "/api/v1/kb/articles/"+id+"/feedback", "", map[string]any{"helpful": true}, "X-Test-IP", "198.51.100.7").Code == 429 {
				limited = true
			}
		}
		if !limited {
			t.Fatal("feedback must be rate-limited per IP")
		}
	})
}

func TestKBFeedback_InvisibleArticle404(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		visibilityFixture(e)
		for _, slug := range []string{"int-pub", "pub-draft", "pub-cust", "pub-archived"} {
			e.want(e.do("POST", "/api/v1/kb/articles/"+slugID(e, slug)+"/feedback", "", map[string]any{"helpful": true}), 404)
		}
		e.want(e.do("POST", "/api/v1/kb/articles/"+slugID(e, "pub-cust")+"/feedback", "customer", map[string]any{"helpful": true}), 204)
		e.want(e.do("POST", "/api/v1/kb/articles/"+slugID(e, "pub-pub")+"/feedback", "", map[string]any{}), 422)
		e.want(e.do("POST", "/api/v1/kb/articles/not-a-uuid/feedback", "", map[string]any{"helpful": true}), 404)
	})
}

func TestKBViews_StaffNotCounted(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		cat := e.category("views", VisPublic, "")
		id := e.article(cat, "viewed", VisPublic, "published", "Viewed", "x")
		for _, who := range []string{"agent", "supervisor", "admin", "agent"} {
			e.want(e.do("GET", "/api/v1/kb/articles/viewed", who, nil), 200)
		}
		if v, _, _ := e.m.counters.Pending(id); v != 0 {
			t.Fatalf("staff previews counted: %d", v)
		}
		e.want(e.do("GET", "/api/v1/kb/articles/viewed", "", nil), 200)
		e.want(e.do("GET", "/api/v1/kb/articles/viewed", "customer", nil), 200)
		_ = e.m.FlushCounters(ctx)
		var views int64
		_ = e.db.Read().QueryRowContext(ctx, `SELECT views FROM kb_articles WHERE id = ?`, id).Scan(&views)
		if views != 2 {
			t.Fatalf("views %d, want 2", views)
		}
	})
}

func TestKBViews_CoalescedFlush(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		cat := e.category("hot", VisPublic, "")
		id := e.article(cat, "hot-article", VisPublic, "published", "Hot", "x")
		const reads, workers = 10000, 8
		var wg sync.WaitGroup
		var mu sync.Mutex
		bad := 0
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < reads/workers; i++ {
					rec := httptest.NewRecorder()
					e.srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/kb/articles/hot-article", nil))
					if rec.Code != 200 {
						mu.Lock()
						bad++
						mu.Unlock()
					}
				}
			}()
		}
		wg.Wait()
		if bad > 0 {
			t.Fatalf("%d reads failed", bad)
		}
		if f := e.m.counters.Flushes.Load(); f != 0 {
			t.Fatalf("reads wrote %d transactions before the flush", f)
		}
		_ = e.m.FlushCounters(ctx)
		if f := e.m.counters.Flushes.Load(); f != 1 {
			t.Fatalf("10000 reads → %d writer transactions, want 1", f)
		}
		var views int64
		_ = e.db.Read().QueryRowContext(ctx, `SELECT views FROM kb_articles WHERE id = ?`, id).Scan(&views)
		if views != reads {
			t.Fatalf("views %d, want %d", views, reads)
		}
	})
}

// ---- KB-014: realtime ----

func TestKBWS_StaffReceivesUpdate(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		srv := httptest.NewServer(e.hub)
		defer srv.Close()
		staff := wsDial(t, srv, "supervisor")
		ack := wsSubscribe(t, staff, Topic)
		if ack["ok"] != true || ack["denied"] != nil {
			t.Fatalf("staff subscribe: %v", ack)
		}
		cat := e.category("live", VisPublic, "")
		f := wsRecv(t, staff)
		if f["event"] != "kb.updated" || f["topic"] != Topic {
			t.Fatalf("category frame %v", f)
		}
		id := e.article(cat, "live-edit", VisPublic, "draft", "Live", "x")
		f = wsRecv(t, staff)
		data, _ := f["data"].(map[string]any)
		if f["event"] != "kb.updated" || data["article_id"] != id || data["status"] != "draft" {
			t.Fatalf("article frame %v", f)
		}
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+id, "supervisor", map[string]any{"status": "published"}), 200)
		f = wsRecv(t, staff)
		data, _ = f["data"].(map[string]any)
		if data["status"] != "published" || data["actor_id"] != e.p("supervisor").UserID {
			t.Fatalf("publish frame %v", f)
		}
	})
}

func TestKBWS_CustomerCannotSubscribe(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		srv := httptest.NewServer(e.hub)
		defer srv.Close()
		// kb is a staff-only topic class: a customer asking for it breaks the
		// protocol and is closed with 4403 (realtime_spec §7), before any frame.
		c := wsDial(t, srv, "customer")
		b, _ := json.Marshal(map[string]any{"op": "subscribe", "id": "s1", "topics": []string{Topic}})
		if err := c.Write(context.Background(), websocket.MessageText, b); err != nil {
			t.Fatal(err)
		}
		e.category("quiet", VisPublic, "")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, frame, err := c.Read(ctx)
		if code := websocket.CloseStatus(err); code != realtime.CloseForbidden {
			t.Fatalf("customer subscribing kb: frame %s, close %d, want 4403", frame, code)
		}
	})
}
