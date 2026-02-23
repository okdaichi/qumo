package topology

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncHandlerFunc_GET(t *testing.T) {
	topo := &Topology{}
	topo.Register(RelayInfo{Name: "A", Region: "us-east-1", Neighbors: map[string]float64{"B": 1}})
	topo.Register(RelayInfo{Name: "B", Region: "us-west-1", Neighbors: map[string]float64{}})

	handler := SyncHandlerFunc(topo)

	req := httptest.NewRequest(http.MethodGet, "/sync", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")

	var resp GraphResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Len(t, resp.Nodes, 2)

	// Verify nodes
	nodeMap := make(map[string]NodeResponse)
	for _, n := range resp.Nodes {
		nodeMap[n.ID] = n
	}
	assert.Equal(t, "us-east-1", nodeMap["A"].Region)
	assert.Equal(t, "us-west-1", nodeMap["B"].Region)
}

func TestSyncHandlerFunc_PUT(t *testing.T) {
	topo := &Topology{}
	handler := SyncHandlerFunc(topo)

	// Create a graph response to restore
	graphResp := GraphResponse{
		Nodes: []NodeResponse{
			{ID: "X", Region: "eu-west-1"},
			{ID: "Y", Region: "eu-central-1"},
		},
		Adjacency: map[string]map[string]float64{
			"X": {"Y": 3.5},
		},
	}

	body, err := json.Marshal(graphResp)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPut, "/sync", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]any
	err = json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "synced", resp["status"])
	assert.Equal(t, float64(2), resp["nodes"])

	// Verify topology was restored
	g := topo.Snapshot()
	assert.Len(t, g.Nodes, 2)
	assert.NotNil(t, g.Nodes["X"])
	assert.NotNil(t, g.Nodes["Y"])
}

func TestSyncHandlerFunc_PUT_InvalidJSON(t *testing.T) {
	topo := &Topology{}
	handler := SyncHandlerFunc(topo)

	req := httptest.NewRequest(http.MethodPut, "/sync", bytes.NewReader([]byte("invalid json")))
	rec := httptest.NewRecorder()

	handler(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "invalid JSON")
}

func TestSyncHandlerFunc_InvalidMethod(t *testing.T) {
	topo := &Topology{}
	handler := SyncHandlerFunc(topo)

	req := httptest.NewRequest(http.MethodPost, "/sync", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestSyncHandler_ServeHTTP(t *testing.T) {
	topo := &Topology{}
	topo.Register(RelayInfo{Name: "A", Neighbors: map[string]float64{"B": 1}})

	handler := &SyncHandler{Topology: topo}

	req := httptest.NewRequest(http.MethodGet, "/sync", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp GraphResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Nodes)
}

func TestNewPeerSyncer(t *testing.T) {
	topo := &Topology{}
	syncer := NewPeerSyncer("http://peer:8090", topo, 5*time.Second)

	assert.Equal(t, "http://peer:8090", syncer.PeerURL)
	assert.Equal(t, 5*time.Second, syncer.Interval)
	assert.NotNil(t, syncer.Topology)
	assert.NotNil(t, syncer.client)
}

func TestPeerSyncer_Pull(t *testing.T) {
	// Create target topology that will receive sync
	targetTopo := &Topology{}

	// Create mock peer server with source data
	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/sync", r.URL.Path)

		resp := GraphResponse{
			Nodes: []NodeResponse{
				{ID: "peer-a", Region: "us-east-1"},
				{ID: "peer-b", Region: "us-west-1"},
			},
			Adjacency: map[string]map[string]float64{
				"peer-a": {"peer-b": 2.0},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer peerServer.Close()

	syncer := NewPeerSyncer(peerServer.URL, targetTopo, 1*time.Second)

	err := syncer.pull()
	require.NoError(t, err)

	// Verify topology was synced
	g := targetTopo.Snapshot()
	assert.Len(t, g.Nodes, 2)
	assert.NotNil(t, g.Nodes["peer-a"])
	assert.NotNil(t, g.Nodes["peer-b"])
}

func TestPeerSyncer_Pull_ServerError(t *testing.T) {
	targetTopo := &Topology{}

	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer peerServer.Close()

	syncer := NewPeerSyncer(peerServer.URL, targetTopo, 1*time.Second)

	err := syncer.pull()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestPeerSyncer_Pull_InvalidJSON(t *testing.T) {
	targetTopo := &Topology{}

	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("invalid json"))
	}))
	defer peerServer.Close()

	syncer := NewPeerSyncer(peerServer.URL, targetTopo, 1*time.Second)

	err := syncer.pull()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "decode response")
}

func TestPeerSyncer_Push(t *testing.T) {
	// Create source topology with data to push
	sourceTopo := &Topology{}
	sourceTopo.Register(RelayInfo{Name: "local-a", Region: "eu-west-1", Neighbors: map[string]float64{"local-b": 1.5}})
	sourceTopo.Register(RelayInfo{Name: "local-b", Region: "eu-central-1", Neighbors: map[string]float64{}})

	// Create mock peer server that will receive push
	var receivedGraph GraphResponse
	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Equal(t, "/sync", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		err := json.NewDecoder(r.Body).Decode(&receivedGraph)
		require.NoError(t, err)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"status": "synced", "nodes": len(receivedGraph.Nodes)})
	}))
	defer peerServer.Close()

	syncer := NewPeerSyncer(peerServer.URL, sourceTopo, 1*time.Second)

	err := syncer.Push()
	require.NoError(t, err)

	// Verify pushed data
	assert.Len(t, receivedGraph.Nodes, 2)
	nodeMap := make(map[string]NodeResponse)
	for _, n := range receivedGraph.Nodes {
		nodeMap[n.ID] = n
	}
	assert.Equal(t, "eu-west-1", nodeMap["local-a"].Region)
	assert.Equal(t, "eu-central-1", nodeMap["local-b"].Region)
	assert.Equal(t, 1.5, receivedGraph.Adjacency["local-a"]["local-b"])
}

func TestPeerSyncer_Push_ServerError(t *testing.T) {
	sourceTopo := &Topology{}
	sourceTopo.Register(RelayInfo{Name: "A", Neighbors: map[string]float64{}})

	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer peerServer.Close()

	syncer := NewPeerSyncer(peerServer.URL, sourceTopo, 1*time.Second)

	err := syncer.Push()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestPeerSyncer_Run(t *testing.T) {
	targetTopo := &Topology{}

	// Mock peer that increments a counter each time it's called
	pullCount := 0
	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pullCount++
		resp := GraphResponse{
			Nodes: []NodeResponse{
				{ID: "sync-test", Region: "test"},
			},
			Adjacency: map[string]map[string]float64{},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer peerServer.Close()

	syncer := NewPeerSyncer(peerServer.URL, targetTopo, 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	syncer.Run(ctx)

	// Should have pulled at least 2 times (at 0ms, 50ms, 100ms)
	assert.GreaterOrEqual(t, pullCount, 2)
}

func TestPeerSyncer_Run_CancelsImmediately(t *testing.T) {
	targetTopo := &Topology{}

	pullCount := 0
	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pullCount++
		resp := GraphResponse{Nodes: []NodeResponse{}, Adjacency: map[string]map[string]float64{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer peerServer.Close()

	syncer := NewPeerSyncer(peerServer.URL, targetTopo, 1*time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	syncer.Run(ctx)

	// Should not have pulled since context was cancelled before first tick
	assert.Equal(t, 0, pullCount)
}

func TestMustParseURL(t *testing.T) {
	u := mustParseURL("http://example.com:8080/path")

	assert.Equal(t, "http", u.Scheme)
	assert.Equal(t, "example.com:8080", u.Host)
	assert.Equal(t, "/path", u.Path)
}

func TestMustParseURL_Panic(t *testing.T) {
	assert.Panics(t, func() {
		mustParseURL("://invalid")
	})
}
