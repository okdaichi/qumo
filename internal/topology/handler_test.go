package topology

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewNodeHandlerFunc_PUT(t *testing.T) {
	topo := &Topology{}
	handler := NewNodeHandlerFunc(topo)

	body := registerRequest{
		Region:    "us-east-1",
		Neighbors: map[string]float64{"relay-b": 2.5, "relay-c": 1.0},
	}
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPut, "/relay/relay-a", bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()

	handler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")

	var resp map[string]any
	err = json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "registered", resp["status"])
	assert.Equal(t, "relay-a", resp["relay"])

	// Verify relay was registered in topology
	g := topo.Snapshot()
	node := g.Nodes["relay-a"]
	require.NotNil(t, node)
	assert.Equal(t, "us-east-1", node.Region)
	assert.Len(t, node.Edges, 2)
}

func TestNewNodeHandlerFunc_PUT_InvalidJSON(t *testing.T) {
	topo := &Topology{}
	handler := NewNodeHandlerFunc(topo)

	req := httptest.NewRequest(http.MethodPut, "/relay/relay-a", bytes.NewReader([]byte("invalid json")))
	rec := httptest.NewRecorder()

	handler(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "invalid JSON")
}

func TestNewNodeHandlerFunc_PUT_EmptyName(t *testing.T) {
	topo := &Topology{}
	handler := NewNodeHandlerFunc(topo)

	body := registerRequest{Neighbors: map[string]float64{"relay-b": 1}}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPut, "/relay/", bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()

	handler(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "relay name is required")
}

func TestNewNodeHandlerFunc_DELETE(t *testing.T) {
	topo := &Topology{}
	topo.Register(RelayInfo{
		Name:      "relay-a",
		Neighbors: map[string]float64{"relay-b": 1},
	})

	handler := NewNodeHandlerFunc(topo)

	req := httptest.NewRequest(http.MethodDelete, "/relay/relay-a", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "deregistered", resp["status"])
	assert.Equal(t, "relay-a", resp["relay"])

	// Verify relay was removed
	g := topo.Snapshot()
	assert.Nil(t, g.Nodes["relay-a"])
}

func TestNewNodeHandlerFunc_DELETE_NotFound(t *testing.T) {
	topo := &Topology{}
	handler := NewNodeHandlerFunc(topo)

	req := httptest.NewRequest(http.MethodDelete, "/relay/nonexistent", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "relay not found")
}

func TestNewNodeHandlerFunc_InvalidMethod(t *testing.T) {
	topo := &Topology{}
	handler := NewNodeHandlerFunc(topo)

	req := httptest.NewRequest(http.MethodPost, "/relay/relay-a", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestRouteHandlerFunc(t *testing.T) {
	topo := &Topology{}
	topo.Register(RelayInfo{Name: "A", Neighbors: map[string]float64{"B": 1}})
	topo.Register(RelayInfo{Name: "B", Neighbors: map[string]float64{"C": 1}})
	topo.Register(RelayInfo{Name: "C", Neighbors: map[string]float64{}})

	handler := RouteHandlerFunc(topo)

	req := httptest.NewRequest(http.MethodGet, "/route?from=A&to=C", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")

	var result RouteResult
	err := json.NewDecoder(rec.Body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, "A", result.From)
	assert.Equal(t, "C", result.To)
	assert.Equal(t, "B", result.NextHop)
	assert.Equal(t, 2.0, result.Cost)
	assert.Equal(t, []string{"A", "B", "C"}, result.FullPath)
}

func TestRouteHandlerFunc_MissingParams(t *testing.T) {
	topo := &Topology{}
	handler := RouteHandlerFunc(topo)

	tests := map[string]struct {
		url string
	}{
		"missing from": {url: "/route?to=B"},
		"missing to":   {url: "/route?from=A"},
		"missing both": {url: "/route"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			rec := httptest.NewRecorder()

			handler(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code)

			var resp map[string]string
			err := json.NewDecoder(rec.Body).Decode(&resp)
			require.NoError(t, err)
			assert.Contains(t, resp["error"], "'from' and 'to'")
		})
	}
}

func TestRouteHandlerFunc_NotFound(t *testing.T) {
	topo := &Topology{}
	handler := RouteHandlerFunc(topo)

	req := httptest.NewRequest(http.MethodGet, "/route?from=X&to=Y", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp["error"])
}

func TestRouteHandlerFunc_InvalidMethod(t *testing.T) {
	topo := &Topology{}
	handler := RouteHandlerFunc(topo)

	req := httptest.NewRequest(http.MethodPost, "/route?from=A&to=B", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestGraphHandlerFunc(t *testing.T) {
	topo := &Topology{}
	topo.Register(RelayInfo{Name: "A", Region: "us-east-1", Neighbors: map[string]float64{"B": 1}})
	topo.Register(RelayInfo{Name: "B", Region: "us-west-1", Neighbors: map[string]float64{}})

	handler := GraphHandlerFunc(topo)

	req := httptest.NewRequest(http.MethodGet, "/graph", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")

	var resp GraphResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Len(t, resp.Nodes, 2)

	// Check nodes
	nodeMap := make(map[string]NodeResponse)
	for _, n := range resp.Nodes {
		nodeMap[n.ID] = n
	}
	assert.Equal(t, "us-east-1", nodeMap["A"].Region)
	assert.Equal(t, "us-west-1", nodeMap["B"].Region)

	// Check adjacency
	require.NotNil(t, resp.Adjacency["A"])
	assert.Equal(t, 1.0, resp.Adjacency["A"]["B"])
}

func TestGraphHandlerFunc_InvalidMethod(t *testing.T) {
	topo := &Topology{}
	handler := GraphHandlerFunc(topo)

	req := httptest.NewRequest(http.MethodPost, "/graph", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestJsonError(t *testing.T) {
	rec := httptest.NewRecorder()

	jsonError(rec, http.StatusNotFound, "test error message")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "test error message", resp["error"])
}
