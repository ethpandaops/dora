package replay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// elBlockHistory is how many recent block hashes are kept for block filters. It only
// has to cover the gap between two filter polls.
const elBlockHistory = 4096

// blockTagParam maps the JSON-RPC methods that take a block tag to the index of that
// parameter, so `latest` can be pinned to the virtual execution head.
var blockTagParam = map[string]int{
	"eth_getBlockByNumber":                 0,
	"eth_getBlockTransactionCountByNumber": 0,
	"eth_getUncleCountByBlockNumber":       0,
	"eth_getBlockReceipts":                 0,
	"eth_getBalance":                       1,
	"eth_getCode":                          1,
	"eth_getTransactionCount":              1,
	"eth_call":                             1,
	"eth_estimateGas":                      1,
	"eth_createAccessList":                 1,
	"eth_getStorageAt":                     2,
	"eth_getProof":                         2,
}

// blockTags are the symbolic block references that must be pinned to the virtual head.
// `finalized` and `safe` are approximated by the head, which is the closest the replay
// can get without tracking execution finality separately.
var blockTags = map[string]bool{
	"latest":    true,
	"pending":   true,
	"safe":      true,
	"finalized": true,
}

// elBlock is the part of an execution block the replay tracks.
type elBlock struct {
	number    uint64
	hash      string
	timestamp time.Time
}

// elProxy is the fake execution node: a JSON-RPC proxy with a virtual head that follows
// the replayed consensus head, so the explorer never sees a block from the future.
type elProxy struct {
	logger logrus.FieldLogger
	url    string
	client *http.Client

	mutex        sync.RWMutex
	headNumber   uint64
	headHash     string
	blockHashes  map[uint64]string
	filters      map[string]uint64
	filterSerial uint64
	requestID    uint64
}

var _ http.Handler = (*elProxy)(nil)

func newELProxy(logger logrus.FieldLogger, url string) *elProxy {
	return &elProxy{
		logger:      logger,
		url:         strings.TrimSuffix(url, "/"),
		client:      newPooledClient(5 * time.Minute),
		blockHashes: make(map[uint64]string, elBlockHistory),
		filters:     make(map[string]uint64),
	}
}

// init positions the virtual execution head at the last block produced at or before the
// replay's start time, found by bisecting the upstream chain.
func (p *elProxy) init(ctx context.Context, startTime time.Time) error {
	latest, err := p.blockByTag(ctx, "latest")
	if err != nil {
		return err
	}

	if latest == nil {
		return fmt.Errorf("execution upstream has no latest block")
	}

	if !latest.timestamp.After(startTime) {
		p.setHead(latest)
		return nil
	}

	low, high := uint64(0), latest.number

	for low < high {
		mid := (low + high + 1) / 2

		block, err := p.blockByNumber(ctx, mid)
		if err != nil {
			return err
		}

		if block == nil || block.timestamp.After(startTime) {
			high = mid - 1
		} else {
			low = mid
		}
	}

	block, err := p.blockByNumber(ctx, low)
	if err != nil {
		return err
	}

	if block == nil {
		return fmt.Errorf("could not resolve execution block %v", low)
	}

	p.setHead(block)

	p.logger.WithFields(logrus.Fields{
		"block": block.number,
		"time":  block.timestamp.UTC().Format(time.RFC3339),
	}).Info("resolved execution head")

	return nil
}

// advanceTo moves the virtual head forward over every block produced at or before the
// given slot time, and returns the blocks it took in.
func (p *elProxy) advanceTo(ctx context.Context, slotTime time.Time) ([]elBlock, error) {
	added := []elBlock{}

	for {
		next, err := p.blockByNumber(ctx, p.head()+1)
		if err != nil {
			return added, err
		}

		if next == nil || next.timestamp.After(slotTime) {
			return added, nil
		}

		p.setHead(next)
		added = append(added, *next)
	}
}

func (p *elProxy) head() uint64 {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	return p.headNumber
}

func (p *elProxy) setHead(block *elBlock) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.headNumber = block.number
	p.headHash = block.hash
	p.blockHashes[block.number] = block.hash

	if block.number >= elBlockHistory {
		delete(p.blockHashes, block.number-elBlockHistory)
	}
}

// -- JSON-RPC ----------------------------------------------------------------------

type rpcRequest struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      json.RawMessage   `json:"id"`
	Method  string            `json:"method"`
	Params  []json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (p *elProxy) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "only POST is supported", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		http.Error(w, "empty request", http.StatusBadRequest)
		return
	}

	if trimmed[0] == '[' {
		requests := []rpcRequest{}
		if err := json.Unmarshal(trimmed, &requests); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		writeJSON(w, http.StatusOK, p.handleBatch(req.Context(), requests))

		return
	}

	request := rpcRequest{}
	if err := json.Unmarshal(trimmed, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, http.StatusOK, p.handle(req.Context(), &request))
}

// handleBatch answers a batched call, forwarding everything it cannot answer locally as
// one batch. Splitting a batch into individual upstream calls would turn a single round
// trip into one per entry, which is exactly what the caller batched to avoid.
func (p *elProxy) handleBatch(ctx context.Context, requests []rpcRequest) []*rpcResponse {
	responses := make([]*rpcResponse, len(requests))
	forwarded := make([]*rpcRequest, 0, len(requests))
	positions := make([]int, 0, len(requests))

	for i := range requests {
		local, forward := p.prepare(&requests[i])
		if local != nil {
			responses[i] = local
			continue
		}

		forwarded = append(forwarded, forward)
		positions = append(positions, i)
	}

	if len(forwarded) == 0 {
		return responses
	}

	results := p.forwardBatch(ctx, forwarded)
	for n, position := range positions {
		responses[position] = results[n]
	}

	return responses
}

func (p *elProxy) handle(ctx context.Context, request *rpcRequest) *rpcResponse {
	local, forward := p.prepare(request)
	if local != nil {
		return local
	}

	return p.forward(ctx, forward)
}

// prepare answers a call from the replay's own state where it can, and otherwise
// returns the (possibly rewritten) request to forward upstream.
func (p *elProxy) prepare(request *rpcRequest) (*rpcResponse, *rpcRequest) {
	switch request.Method {
	case "eth_blockNumber":
		return p.reply(request, hexUint(p.head())), nil

	case "eth_syncing":
		return p.reply(request, false), nil

	case "eth_newBlockFilter":
		return p.reply(request, p.newBlockFilter()), nil

	case "eth_getFilterChanges":
		return p.handleFilterChanges(request), nil

	case "eth_uninstallFilter":
		return p.handleUninstallFilter(request), nil

	case "eth_getLogs":
		return p.clampGetLogs(request)
	}

	if index, hasTag := blockTagParam[request.Method]; hasTag {
		pinned, visible, err := p.pinBlockTag(request, index)
		if err != nil {
			return p.replyError(request, -32602, err.Error()), nil
		}

		if !visible {
			// the requested block is beyond the virtual head; a node that does not
			// have it yet answers with no result
			return p.reply(request, nil), nil
		}

		return nil, pinned
	}

	return nil, request
}

// pinBlockTag rewrites a symbolic block tag to the virtual head and reports whether a
// numeric block reference is at or below it.
func (p *elProxy) pinBlockTag(request *rpcRequest, index int) (*rpcRequest, bool, error) {
	if index > len(request.Params) {
		return nil, false, fmt.Errorf("%v is missing parameter %v", request.Method, index)
	}

	if index == len(request.Params) {
		// the tag is optional and was omitted, which means `latest`; appending the
		// pinned head keeps the meaning the caller intended
		pinned := *request
		pinned.Params = append(append([]json.RawMessage{}, request.Params...), quotedHex(p.head()))

		return &pinned, true, nil
	}

	raw := request.Params[index]

	tag := ""
	if err := json.Unmarshal(raw, &tag); err != nil {
		// not a string: eth_getBlockByNumber never sees this, but block-hash objects
		// (eth_getProof style) are passed through untouched
		return request, true, nil
	}

	if blockTags[tag] {
		pinned := *request
		pinned.Params = append([]json.RawMessage{}, request.Params...)
		pinned.Params[index] = quotedHex(p.head())

		return &pinned, true, nil
	}

	if tag == "earliest" || !strings.HasPrefix(tag, "0x") {
		return request, true, nil
	}

	number, err := strconv.ParseUint(strings.TrimPrefix(tag, "0x"), 16, 64)
	if err != nil {
		return nil, false, fmt.Errorf("invalid block number %q", tag)
	}

	return request, number <= p.head(), nil
}

func (p *elProxy) newBlockFilter() string {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.filterSerial++
	id := fmt.Sprintf("0x%016x", p.filterSerial)
	p.filters[id] = p.headNumber

	return id
}

// handleFilterChanges returns the hashes of the blocks that entered the virtual chain
// since the filter was last polled.
func (p *elProxy) handleFilterChanges(request *rpcRequest) *rpcResponse {
	id, err := stringParam(request, 0)
	if err != nil {
		return p.replyError(request, -32602, err.Error())
	}

	p.mutex.Lock()
	delivered, exists := p.filters[id]
	if !exists {
		p.mutex.Unlock()

		return p.replyError(request, -32000, "filter not found")
	}

	hashes := []string{}

	for number := delivered + 1; number <= p.headNumber; number++ {
		if hash, ok := p.blockHashes[number]; ok {
			hashes = append(hashes, hash)
		}
	}

	p.filters[id] = p.headNumber
	p.mutex.Unlock()

	return p.reply(request, hashes)
}

func (p *elProxy) handleUninstallFilter(request *rpcRequest) *rpcResponse {
	id, err := stringParam(request, 0)
	if err != nil {
		return p.replyError(request, -32602, err.Error())
	}

	p.mutex.Lock()
	_, exists := p.filters[id]
	delete(p.filters, id)
	p.mutex.Unlock()

	return p.reply(request, exists)
}

// clampGetLogs clamps the requested range to the virtual head before forwarding, so a
// scan that asks for `latest` stops where the replay currently stands.
func (p *elProxy) clampGetLogs(request *rpcRequest) (*rpcResponse, *rpcRequest) {
	if len(request.Params) == 0 {
		return nil, request
	}

	filter := map[string]json.RawMessage{}
	if err := json.Unmarshal(request.Params[0], &filter); err != nil {
		return p.replyError(request, -32602, "invalid log filter"), nil
	}

	if _, byHash := filter["blockHash"]; byHash {
		return nil, request
	}

	head := p.head()

	from, err := blockNumberFromFilter(filter, "fromBlock", 0)
	if err != nil {
		return p.replyError(request, -32602, err.Error()), nil
	}

	if from > head {
		return p.reply(request, []any{}), nil
	}

	to, err := blockNumberFromFilter(filter, "toBlock", head)
	if err != nil {
		return p.replyError(request, -32602, err.Error()), nil
	}

	if to > head {
		to = head
	}

	filter["fromBlock"] = quotedHex(from)
	filter["toBlock"] = quotedHex(to)

	encoded, err := json.Marshal(filter)
	if err != nil {
		return p.replyError(request, -32603, err.Error()), nil
	}

	clamped := *request
	clamped.Params = append([]json.RawMessage{encoded}, request.Params[1:]...)

	return nil, &clamped
}

// blockNumberFromFilter reads a log filter bound, mapping symbolic tags to fallback.
func blockNumberFromFilter(filter map[string]json.RawMessage, field string, fallback uint64) (uint64, error) {
	raw, exists := filter[field]
	if !exists {
		return fallback, nil
	}

	value := ""
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("invalid %v in log filter", field)
	}

	if value == "earliest" {
		return 0, nil
	}

	if blockTags[value] {
		return fallback, nil
	}

	number, err := strconv.ParseUint(strings.TrimPrefix(value, "0x"), 16, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %v %q in log filter", field, value)
	}

	return number, nil
}

// forward passes a request to the real execution node and returns its answer verbatim.
func (p *elProxy) forward(ctx context.Context, request *rpcRequest) *rpcResponse {
	body, err := p.post(ctx, request)
	if err != nil {
		return p.replyError(request, -32603, err.Error())
	}

	response := rpcResponse{}
	if err := json.Unmarshal(body, &response); err != nil {
		return p.replyError(request, -32603, fmt.Sprintf("invalid upstream response: %s", truncate(body, 200)))
	}

	response.ID = request.ID
	response.JSONRPC = "2.0"

	return &response
}

// post sends a JSON-RPC payload to the execution upstream and returns the raw answer.
func (p *elProxy) post(ctx context.Context, payload any) ([]byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	rsp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rsp.Body.Close() }()

	return io.ReadAll(rsp.Body)
}

// forwardBatch passes several requests upstream in a single call and puts the answers
// back in the order they were asked, matching by id.
func (p *elProxy) forwardBatch(ctx context.Context, requests []*rpcRequest) []*rpcResponse {
	if len(requests) == 1 {
		return []*rpcResponse{p.forward(ctx, requests[0])}
	}

	body, err := p.post(ctx, requests)
	if err != nil {
		return p.batchError(requests, err)
	}

	upstreamResponses := []*rpcResponse{}
	if err := json.Unmarshal(body, &upstreamResponses); err != nil {
		return p.batchError(requests, fmt.Errorf("invalid upstream batch response: %s", truncate(body, 200)))
	}

	byID := make(map[string]*rpcResponse, len(upstreamResponses))
	for _, response := range upstreamResponses {
		byID[string(response.ID)] = response
	}

	responses := make([]*rpcResponse, len(requests))

	for i, request := range requests {
		response, matched := byID[string(request.ID)]
		if !matched {
			if i < len(upstreamResponses) {
				// an upstream that does not echo ids is still answering in order
				response = upstreamResponses[i]
			} else {
				responses[i] = p.replyError(request, -32603, "upstream did not answer this batch entry")
				continue
			}
		}

		response.ID = request.ID
		response.JSONRPC = "2.0"
		responses[i] = response
	}

	return responses
}

func (p *elProxy) batchError(requests []*rpcRequest, err error) []*rpcResponse {
	responses := make([]*rpcResponse, len(requests))
	for i, request := range requests {
		responses[i] = p.replyError(request, -32603, err.Error())
	}

	return responses
}

// call issues a request of the replay's own to the execution upstream.
func (p *elProxy) call(ctx context.Context, method string, params ...any) (json.RawMessage, error) {
	encoded := make([]json.RawMessage, 0, len(params))

	for _, param := range params {
		raw, err := json.Marshal(param)
		if err != nil {
			return nil, err
		}

		encoded = append(encoded, raw)
	}

	p.mutex.Lock()
	p.requestID++
	id := p.requestID
	p.mutex.Unlock()

	request := &rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(strconv.FormatUint(id, 10)),
		Method:  method,
		Params:  encoded,
	}

	response := p.forward(ctx, request)
	if response.Error != nil {
		return nil, fmt.Errorf("%v: %v", method, response.Error.Message)
	}

	return response.Result, nil
}

func (p *elProxy) blockByTag(ctx context.Context, tag string) (*elBlock, error) {
	return p.decodeBlock(p.call(ctx, "eth_getBlockByNumber", tag, false))
}

func (p *elProxy) blockByNumber(ctx context.Context, number uint64) (*elBlock, error) {
	return p.decodeBlock(p.call(ctx, "eth_getBlockByNumber", fmt.Sprintf("0x%x", number), false))
}

func (p *elProxy) decodeBlock(result json.RawMessage, err error) (*elBlock, error) {
	if err != nil {
		return nil, err
	}

	if len(result) == 0 || string(result) == "null" {
		return nil, nil
	}

	header := struct {
		Number    string `json:"number"`
		Hash      string `json:"hash"`
		Timestamp string `json:"timestamp"`
	}{}

	if err := json.Unmarshal(result, &header); err != nil {
		return nil, fmt.Errorf("error parsing execution block: %w", err)
	}

	number, err := strconv.ParseUint(strings.TrimPrefix(header.Number, "0x"), 16, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid block number %q: %w", header.Number, err)
	}

	timestamp, err := strconv.ParseInt(strings.TrimPrefix(header.Timestamp, "0x"), 16, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid block timestamp %q: %w", header.Timestamp, err)
	}

	return &elBlock{
		number:    number,
		hash:      header.Hash,
		timestamp: time.Unix(timestamp, 0).UTC(),
	}, nil
}

func (p *elProxy) reply(request *rpcRequest, result any) *rpcResponse {
	encoded, err := json.Marshal(result)
	if err != nil {
		return p.replyError(request, -32603, err.Error())
	}

	return &rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: encoded}
}

func (p *elProxy) replyError(request *rpcRequest, code int, message string) *rpcResponse {
	return &rpcResponse{
		JSONRPC: "2.0",
		ID:      request.ID,
		Error:   &rpcError{Code: code, Message: message},
	}
}

func stringParam(request *rpcRequest, index int) (string, error) {
	if index >= len(request.Params) {
		return "", fmt.Errorf("missing parameter %v", index)
	}

	value := ""
	if err := json.Unmarshal(request.Params[index], &value); err != nil {
		return "", fmt.Errorf("parameter %v is not a string", index)
	}

	return value, nil
}

func hexUint(value uint64) string {
	return fmt.Sprintf("0x%x", value)
}

func quotedHex(value uint64) json.RawMessage {
	return json.RawMessage(fmt.Sprintf("%q", hexUint(value)))
}
