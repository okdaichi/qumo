package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRegisterHandler_Success(t *testing.T) {
	store := NewStore(30 * time.Second)
	h := &RegisterHandler{Store: store}

	body := `{"id":"n1","addr":"0.0.0.0:443","region":"us-east"}`
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Verify the node was stored with server-detected IP.
	peers := store.Peers("", "", 0)
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if peers[0].Addr != "10.0.0.1:443" {
		t.Errorf("expected addr 10.0.0.1:443 (server-corrected), got %s", peers[0].Addr)
	}
}

func TestRegisterHandler_XForwardedFor(t *testing.T) {
	store := NewStore(30 * time.Second)
	h := &RegisterHandler{Store: store}

	body := `{"id":"n1","addr":"0.0.0.0:443","region":"us-east"}`
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	peers := store.Peers("", "", 0)
	if peers[0].Addr != "203.0.113.50:443" {
		t.Errorf("expected X-Forwarded-For IP, got %s", peers[0].Addr)
	}
}

func TestRegisterHandler_InvalidJSON(t *testing.T) {
	store := NewStore(30 * time.Second)
	h := &RegisterHandler{Store: store}

	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader("not json"))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestRegisterHandler_MissingID(t *testing.T) {
	store := NewStore(30 * time.Second)
	h := &RegisterHandler{Store: store}

	body := `{"addr":"1.2.3.4:443"}`
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestRegisterHandler_WrongMethod(t *testing.T) {
	store := NewStore(30 * time.Second)
	h := &RegisterHandler{Store: store}

	req := httptest.NewRequest(http.MethodGet, "/register", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestPeersHandler_ReturnsAll(t *testing.T) {
	store := NewStore(30 * time.Second)
	store.Register(Node{ID: "n1", Addr: "1.1.1.1:443", Region: "us-east"})
	store.Register(Node{ID: "n2", Addr: "2.2.2.2:443", Region: "ap-northeast"})

	h := &PeersHandler{Store: store, MaxPeers: 20}

	req := httptest.NewRequest(http.MethodGet, "/peers", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var peers []Node
	if err := json.NewDecoder(w.Body).Decode(&peers); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}
}

func TestPeersHandler_SelfExclusion(t *testing.T) {
	store := NewStore(30 * time.Second)
	store.Register(Node{ID: "n1", Addr: "1.1.1.1:443"})
	store.Register(Node{ID: "n2", Addr: "2.2.2.2:443"})

	h := &PeersHandler{Store: store, MaxPeers: 20}

	req := httptest.NewRequest(http.MethodGet, "/peers?self_id=n1", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	var peers []Node
	if err := json.NewDecoder(w.Body).Decode(&peers); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer (self excluded), got %d", len(peers))
	}
	if peers[0].ID != "n2" {
		t.Errorf("expected n2, got %s", peers[0].ID)
	}
}

func TestPeersHandler_RegionFilter(t *testing.T) {
	store := NewStore(30 * time.Second)
	store.Register(Node{ID: "n1", Addr: "1.1.1.1:443", Region: "us-east"})
	store.Register(Node{ID: "n2", Addr: "2.2.2.2:443", Region: "ap-northeast"})

	h := &PeersHandler{Store: store, MaxPeers: 20}

	req := httptest.NewRequest(http.MethodGet, "/peers?region=us-east", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	var peers []Node
	if err := json.NewDecoder(w.Body).Decode(&peers); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if peers[0].Region != "us-east" {
		t.Errorf("expected us-east, got %s", peers[0].Region)
	}
}
