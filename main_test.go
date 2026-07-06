package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestRegisterReturnsPubKey verifies the POST /session/:id response carries the
// pub_key that a previously-registered peer published.
func TestRegisterReturnsPubKey(t *testing.T) {
	srv := httptest.NewServer(newRouter())
	defer srv.Close()

	const pubA = "QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVphYmNkZWY=" // arbitrary base64

	// Peer A registers with its pubkey.
	post(t, srv.URL+"/session/round-trip", map[string]string{
		"udp_addr": "203.0.113.1:40001",
		"pub_key":  pubA,
	})

	// Peer B registers and must see A (with A's pubkey) in the response.
	body := post(t, srv.URL+"/session/round-trip", map[string]string{
		"udp_addr": "203.0.113.2:40002",
		"pub_key":  "b2999999999999999999999999999999999999999999=",
	})

	var resp struct {
		Peers []struct {
			IP     string `json:"ip"`
			PubKey string `json:"pub_key"`
		} `json:"peers"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal response: %v (%s)", err, body)
	}
	if len(resp.Peers) != 1 {
		t.Fatalf("expected 1 existing peer, got %d", len(resp.Peers))
	}
	if resp.Peers[0].PubKey != pubA {
		t.Fatalf("expected peer A pubkey %q, got %q", pubA, resp.Peers[0].PubKey)
	}
}

// TestStreamReturnsPubKey verifies the SSE stream delivers a peer's pub_key.
func TestStreamReturnsPubKey(t *testing.T) {
	srv := httptest.NewServer(newRouter())
	defer srv.Close()

	const pubA = "c3RyZWFtLXB1YmtleS1yb3VuZC10cmlwLXRlc3QtMDAwMDA="

	post(t, srv.URL+"/session/stream-rt", map[string]string{
		"udp_addr": "203.0.113.3:40003",
		"pub_key":  pubA,
	})

	// Open the stream as a different peer; A should be replayed with its pubkey.
	req, _ := http.NewRequest("GET", srv.URL+"/session/stream-rt/stream?udp_addr=203.0.113.4:40004", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()

	got := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if data, ok := strings.CutPrefix(line, "data:"); ok {
				got <- strings.TrimSpace(data)
				return
			}
		}
	}()

	select {
	case data := <-got:
		var peer struct {
			IP     string `json:"ip"`
			PubKey string `json:"pub_key"`
		}
		if err := json.Unmarshal([]byte(data), &peer); err != nil {
			t.Fatalf("unmarshal SSE peer: %v (%s)", err, data)
		}
		if peer.PubKey != pubA {
			t.Fatalf("expected pubkey %q in stream, got %q", pubA, peer.PubKey)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not receive a peer event from the stream")
	}
}

func post(t *testing.T, url string, body map[string]string) []byte {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	out := new(bytes.Buffer)
	out.ReadFrom(resp.Body)
	return out.Bytes()
}
