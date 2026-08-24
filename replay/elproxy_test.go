package replay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func testELProxy(head uint64) *elProxy {
	proxy := newELProxy(logrus.New(), "http://localhost:0")
	proxy.setHead(&elBlock{number: head, hash: "0xdeadbeef"})

	return proxy
}

func rawParams(t *testing.T, values ...any) []json.RawMessage {
	t.Helper()

	params := make([]json.RawMessage, 0, len(values))

	for _, value := range values {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)

		params = append(params, encoded)
	}

	return params
}

func TestPinBlockTag(t *testing.T) {
	proxy := testELProxy(500)

	tests := []struct {
		name        string
		method      string
		params      []any
		wantParam   string
		wantVisible bool
	}{
		{
			name:        "latest is pinned to the virtual head",
			method:      "eth_getBlockByNumber",
			params:      []any{"latest", false},
			wantParam:   "0x1f4",
			wantVisible: true,
		},
		{
			name:        "finalized is approximated by the virtual head",
			method:      "eth_getBlockByNumber",
			params:      []any{"finalized", false},
			wantParam:   "0x1f4",
			wantVisible: true,
		},
		{
			name:        "a block at the head stays visible",
			method:      "eth_getBlockByNumber",
			params:      []any{"0x1f4", false},
			wantParam:   "0x1f4",
			wantVisible: true,
		},
		{
			name:        "a block beyond the head is hidden",
			method:      "eth_getBlockByNumber",
			params:      []any{"0x1f5", false},
			wantVisible: false,
		},
		{
			name:        "earliest is left alone",
			method:      "eth_getBlockByNumber",
			params:      []any{"earliest", false},
			wantParam:   "earliest",
			wantVisible: true,
		},
		{
			name:        "an omitted tag is appended as the head",
			method:      "eth_getBalance",
			params:      []any{"0x0000000000000000000000000000000000000001"},
			wantParam:   "0x1f4",
			wantVisible: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := &rpcRequest{Method: test.method, Params: rawParams(t, test.params...)}
			index := blockTagParam[test.method]

			pinned, visible, err := proxy.pinBlockTag(request, index)
			require.NoError(t, err)
			require.Equal(t, test.wantVisible, visible)

			if !visible {
				return
			}

			value := ""
			require.NoError(t, json.Unmarshal(pinned.Params[index], &value))
			require.Equal(t, test.wantParam, value)
		})
	}
}

func TestBlockNumberFromFilter(t *testing.T) {
	filter := map[string]json.RawMessage{
		"fromBlock": json.RawMessage(`"0x10"`),
		"toBlock":   json.RawMessage(`"latest"`),
	}

	from, err := blockNumberFromFilter(filter, "fromBlock", 0)
	require.NoError(t, err)
	require.Equal(t, uint64(16), from)

	to, err := blockNumberFromFilter(filter, "toBlock", 999)
	require.NoError(t, err)
	require.Equal(t, uint64(999), to)

	missing, err := blockNumberFromFilter(filter, "blockHash", 42)
	require.NoError(t, err)
	require.Equal(t, uint64(42), missing)

	_, err = blockNumberFromFilter(map[string]json.RawMessage{
		"fromBlock": json.RawMessage(`"zzz"`),
	}, "fromBlock", 0)
	require.Error(t, err)
}

func TestBlockFilterDeliversNewBlocks(t *testing.T) {
	proxy := testELProxy(100)

	filterID := proxy.newBlockFilter()

	// nothing has been produced since the filter was created
	response := proxy.handleFilterChanges(&rpcRequest{Params: rawParams(t, filterID)})
	require.Nil(t, response.Error)
	require.JSONEq(t, `[]`, string(response.Result))

	proxy.setHead(&elBlock{number: 101, hash: "0xaa"})
	proxy.setHead(&elBlock{number: 102, hash: "0xbb"})

	response = proxy.handleFilterChanges(&rpcRequest{Params: rawParams(t, filterID)})
	require.Nil(t, response.Error)
	require.JSONEq(t, `["0xaa","0xbb"]`, string(response.Result))

	// a second poll without new blocks returns nothing again
	response = proxy.handleFilterChanges(&rpcRequest{Params: rawParams(t, filterID)})
	require.JSONEq(t, `[]`, string(response.Result))

	// an unknown filter is reported the way a node reports an expired one
	response = proxy.handleFilterChanges(&rpcRequest{Params: rawParams(t, "0xdead")})
	require.NotNil(t, response.Error)
	require.Contains(t, response.Error.Message, "filter not found")
}

func TestClampGetLogsToVirtualHead(t *testing.T) {
	proxy := testELProxy(500)

	tests := []struct {
		name      string
		filter    string
		wantFrom  string
		wantTo    string
		wantLocal string
	}{
		{
			name:     "latest is clamped to the virtual head",
			filter:   `{"fromBlock":"0x10","toBlock":"latest"}`,
			wantFrom: `"0x10"`,
			wantTo:   `"0x1f4"`,
		},
		{
			name:     "a range beyond the head is truncated",
			filter:   `{"fromBlock":"0x10","toBlock":"0x999"}`,
			wantFrom: `"0x10"`,
			wantTo:   `"0x1f4"`,
		},
		{
			name:     "a missing toBlock defaults to the head",
			filter:   `{"fromBlock":"0x10"}`,
			wantFrom: `"0x10"`,
			wantTo:   `"0x1f4"`,
		},
		{
			name:      "a range entirely beyond the head yields no logs",
			filter:    `{"fromBlock":"0x500","toBlock":"0x600"}`,
			wantLocal: `[]`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := &rpcRequest{
				Method: "eth_getLogs",
				Params: []json.RawMessage{json.RawMessage(test.filter)},
			}

			local, forward := proxy.clampGetLogs(request)

			if test.wantLocal != "" {
				require.NotNil(t, local)
				require.JSONEq(t, test.wantLocal, string(local.Result))

				return
			}

			require.Nil(t, local)
			require.NotNil(t, forward)

			clamped := map[string]json.RawMessage{}
			require.NoError(t, json.Unmarshal(forward.Params[0], &clamped))
			require.Equal(t, test.wantFrom, string(clamped["fromBlock"]))
			require.Equal(t, test.wantTo, string(clamped["toBlock"]))
		})
	}
}

// TestBatchIsForwardedAsOneCall guards the property that makes batching worth anything:
// a batched call must reach the upstream as a single request, not as one per entry.
func TestBatchIsForwardedAsOneCall(t *testing.T) {
	upstreamCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		requests := []rpcRequest{}
		require.NoError(t, json.Unmarshal(body, &requests))

		responses := make([]rpcResponse, 0, len(requests))
		for _, request := range requests {
			responses = append(responses, rpcResponse{
				JSONRPC: "2.0",
				ID:      request.ID,
				Result:  json.RawMessage(`"ok"`),
			})
		}

		writeJSON(w, http.StatusOK, responses)
	}))
	defer server.Close()

	proxy := newELProxy(logrus.New(), server.URL)
	proxy.setHead(&elBlock{number: 500, hash: "0xdead"})

	batch := []rpcRequest{
		{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "eth_getTransactionReceipt", Params: rawParams(t, "0xaa")},
		{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "eth_getTransactionReceipt", Params: rawParams(t, "0xbb")},
		{JSONRPC: "2.0", ID: json.RawMessage(`3`), Method: "eth_blockNumber"},
	}

	responses := proxy.handleBatch(context.Background(), batch)

	require.Len(t, responses, 3)
	require.Equal(t, 1, upstreamCalls, "the two forwarded entries must share one upstream call")
	require.JSONEq(t, `"ok"`, string(responses[0].Result))
	require.JSONEq(t, `"ok"`, string(responses[1].Result))
	require.JSONEq(t, `"0x1f4"`, string(responses[2].Result), "eth_blockNumber is answered locally")
}

func TestUninstallFilter(t *testing.T) {
	proxy := testELProxy(100)
	filterID := proxy.newBlockFilter()

	response := proxy.handleUninstallFilter(&rpcRequest{Params: rawParams(t, filterID)})
	require.JSONEq(t, `true`, string(response.Result))

	response = proxy.handleUninstallFilter(&rpcRequest{Params: rawParams(t, filterID)})
	require.JSONEq(t, `false`, string(response.Result))
}
