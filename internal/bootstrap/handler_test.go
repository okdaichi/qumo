package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// decodePeers is a helper that decodes the {"peers":[...]} wrapper.
func decodePeers(t *testing.T, w *httptest.ResponseRecorder) []Node {
	t.Helper()
	var wrapper struct {
		Peers []Node `json:"peers"`
	}
	if err := json.NewDecoder(w.Body).Decode(&wrapper); err != nil {
		t.Fatalf("failed to decode peers response: %v", err)
	}
	return wrapper.Peers
}

func TestRegisterHandler_Success(t *testing.T) {
	store := NewStore(30 * time.Second)
	h := &RegisterHandler{Store: store}

	body := `{"id":"n1","addr":"0.0.0.0:443","region":"us-east","role":"edge"}`
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Verify the node was stored with server-detected IP and role.
	peers := store.Peers(PeerQuery{})
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if peers[0].Addr != "10.0.0.1:443" {
		t.Errorf("expected addr 10.0.0.1:443 (server-corrected), got %s", peers[0].Addr)
	}
	if peers[0].Role != "edge" {
		t.Errorf("expected role edge, got %s", peers[0].Role)
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

	peers := store.Peers(PeerQuery{})
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

	peers := decodePeers(t, w)
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
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

	peers := decodePeers(t, w)
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if peers[0].Region != "us-east" {
		t.Errorf("expected us-east, got %s", peers[0].Region)
	}
}

func TestPeersHandler_FiltersByRole(t *testing.T) {
	store := NewStore(30 * time.Second)
	store.Register(Node{ID: "n1", Addr: "1.1.1.1:443", Role: "edge"})
	store.Register(Node{ID: "n2", Addr: "2.2.2.2:443", Role: "hub"})
	store.Register(Node{ID: "n3", Addr: "3.3.3.3:443", Role: "edge"})

	h := &PeersHandler{Store: store, MaxPeers: 20}

	req := httptest.NewRequest(http.MethodGet, "/peers?role=hub", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	peers := decodePeers(t, w)
	if len(peers) != 1 {
		t.Fatalf("expected 1 hub peer, got %d", len(peers))
	}
	if peers[0].ID != "n2" {
		t.Errorf("expected n2 (hub), got %s", peers[0].ID)
	}
}

func TestPeersHandler_AllowRemote(t *testing.T) {
	store := NewStore(30 * time.Second)
	store.Register(Node{ID: "n1", Addr: "1.1.1.1:443", Region: "us-east"})
	store.Register(Node{ID: "n2", Addr: "2.2.2.2:443", Region: "ap-northeast"})

	h := &PeersHandler{Store: store, MaxPeers: 20}

	// Without allow_remote: only preferred region.
	req := httptest.NewRequest(http.MethodGet, "/peers?region=us-east", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	peers := decodePeers(t, w)
	if len(peers) != 1 {
		t.Fatalf("without allow_remote: expected 1 peer, got %d", len(peers))
	}

	// With allow_remote=true: both regions.
	req = httptest.NewRequest(http.MethodGet, "/peers?region=us-east&allow_remote=true", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	peers = decodePeers(t, w)
	if len(peers) != 2 {
		t.Fatalf("with allow_remote: expected 2 peers, got %d", len(peers))
	}
}

func TestPeersHandler_Limit(t *testing.T) {
	store := NewStore(30 * time.Second)
	for i := 0; i < 5; i++ {
		store.Register(Node{ID: strings.Repeat(string(rune('a'+i)), 2), Addr: "1.1.1.1:443"})
	}

	h := &PeersHandler{Store: store, MaxPeers: 20}

	req := httptest.NewRequest(http.MethodGet, "/peers?limit=2", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	peers := decodePeers(t, w)
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers (limit), got %d", len(peers))
	}
}

