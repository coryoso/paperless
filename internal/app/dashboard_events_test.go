package app

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestDashboardEventsCoalesceAndClose(t *testing.T) {
	var events dashboardEvents
	slow, unsubscribe := events.subscribe()
	defer unsubscribe()
	fast, unsubscribeFast := events.subscribe()
	defer unsubscribeFast()
	<-slow
	<-fast
	for i := 0; i < 1000; i++ {
		events.publish()
	}
	if len(slow) != 1 || len(fast) != 1 {
		t.Fatal("events were not coalesced")
	}
	<-fast
	events.publish()
	if len(fast) != 1 {
		t.Fatal("slow subscriber blocked another client")
	}
	events.close()
	events.publish()
	events.close()
	<-slow
	if _, ok := <-slow; ok {
		t.Fatal("subscriber still open after shutdown")
	}
	closed, cleanup := events.subscribe()
	defer cleanup()
	if _, ok := <-closed; ok {
		t.Fatal("subscription after shutdown is open")
	}
}

type eventClient struct {
	response *http.Response
	messages <-chan string
}

func openEventClient(t *testing.T, url string) eventClient {
	t.Helper()
	client := &http.Client{Timeout: 8 * time.Second}
	response, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	if response.Header.Get("Content-Type") != "text/event-stream" || response.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("stream headers: %v", response.Header)
	}
	messages := make(chan string, 32)
	go func() {
		defer close(messages)
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event:") {
				messages <- line
			}
		}
	}()
	return eventClient{response, messages}
}
func (c eventClient) next(t *testing.T) {
	t.Helper()
	select {
	case message, ok := <-c.messages:
		if !ok || message != "event: dashboard" {
			t.Fatalf("SSE event: %q (open=%v)", message, ok)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no dashboard event")
	}
}

func TestDashboardSSEBroadcastReconnectAndShutdown(t *testing.T) {
	cfg := testServerConfig(t.TempDir())
	p, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(p.handleDashboardEvents))
	defer server.Close()
	defer p.dashboardEvents.close()
	a := openEventClient(t, server.URL)
	a.next(t)
	b := openEventClient(t, server.URL)
	b.next(t)
	input := filepath.Join(cfg.Paths.Inbox, "scan.pdf")
	if err := os.WriteFile(input, []byte("scan"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.createJob(ctx, "scan", input, info, nil); err != nil {
		t.Fatal(err)
	}
	a.next(t)
	b.next(t)
	a.response.Body.Close()
	if err := p.failJob(ctx, "scan", errors.New("OCR failed")); err != nil {
		t.Fatal(err)
	}
	b.next(t)
	a = openEventClient(t, server.URL)
	a.next(t) // always refresh on reconnect, even without a new write
	p.dashboardEvents.close()
	for _, c := range []eventClient{a, b} {
		select {
		case _, ok := <-c.messages:
			if ok {
				t.Fatal("unexpected event on shutdown")
			}
		case <-time.After(time.Second):
			t.Fatal("SSE kept shutdown open")
		}
	}
}

func TestDashboardSSEIdleHeartbeat(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		p := &Processor{}
		writer := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			defer close(done)
			p.handleDashboardEvents(writer, httptest.NewRequest(http.MethodGet, "/api/dashboard/events", nil).WithContext(ctx))
		}()
		synctest.Wait()
		time.Sleep(15 * time.Second)
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("request cancellation did not stop stream")
		}
		if !strings.Contains(writer.Body.String(), "retry: 1000\n\n") || !strings.Contains(writer.Body.String(), "event: dashboard\ndata: {}\n\n") {
			t.Fatalf("initial frames: %q", writer.Body.String())
		}
		if !strings.Contains(writer.Body.String(), ": keep-alive\n\n") {
			t.Fatal("no idle heartbeat")
		}
	})
}
