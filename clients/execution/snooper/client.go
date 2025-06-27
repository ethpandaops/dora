package snooper

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/utils"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

// Client represents a websocket client connection to an rpc-snooper instance
type Client struct {
	url      string
	clientID uint16
	conn     *websocket.Conn
	logger   logrus.FieldLogger
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup

	// event dispatchers
	executionTimeDispatcher utils.Dispatcher[*ExecutionTimeEvent]

	// Request/response handling
	requestCounter  uint64
	pendingRequests map[uint64]chan *WSMessageWithBinary
	requestMu       sync.RWMutex

	// Module state
	moduleID uint64
}

// ExecutionTimeEvent represents a block execution time event
type ExecutionTimeEvent struct {
	BlockHash     common.Hash
	BlockNumber   uint64
	ClientID      uint16
	ExecutionTime time.Duration
	Timestamp     time.Time
}

// NewClient creates a new snooper client
func NewClient(url string, clientID uint16, logger logrus.FieldLogger) *Client {
	ctx, cancel := context.WithCancel(context.Background())

	return &Client{
		url:             url,
		clientID:        clientID,
		logger:          logger.WithField("component", "snooper-client").WithField("client_id", clientID),
		ctx:             ctx,
		cancel:          cancel,
		pendingRequests: make(map[uint64]chan *WSMessageWithBinary),
	}
}

func (client *Client) SubscribeExecutionTimeEvent(capacity int, blocking bool) *utils.Subscription[*ExecutionTimeEvent] {
	return client.executionTimeDispatcher.Subscribe(capacity, blocking)
}

// Connect establishes the websocket connection and registers the response tracer module
func (c *Client) Connect() error {
	// Parse and validate URL
	u, err := url.Parse(c.url)
	if err != nil {
		return fmt.Errorf("invalid snooper URL: %w", err)
	}

	// switch to websocket
	if strings.HasPrefix(u.Scheme, "http") {
		u.Scheme = strings.Replace(u.Scheme, "http", "ws", 1)
	}

	// Ensure the path ends with the control endpoint
	if u.Path == "" || u.Path == "/" {
		u.Path = "/_snooper/control"
	} else if u.Path != "/_snooper/control" {
		u.Path = u.Path + "/_snooper/control"
	}

	c.logger.WithField("url", u.String()).Debug("connecting to snooper control endpoint")

	// Establish websocket connection
	conn, _, err := websocket.DefaultDialer.DialContext(c.ctx, u.String(), nil)
	if err != nil {
		return fmt.Errorf("websocket dial failed: %w", err)
	}

	c.conn = conn
	c.logger.Debug("snooper client connected")

	// Start message handling goroutine
	c.wg.Add(1)
	go c.handleMessages()

	// Register as response tracer module
	if err := c.registerModule(); err != nil {
		c.conn.Close()
		return fmt.Errorf("module registration failed: %w", err)
	}

	c.logger.WithField("module_id", c.moduleID).Debug("snooper client connected and set up")
	return nil
}

// Run keeps the client running until context is cancelled
func (c *Client) Run() error {
	<-c.ctx.Done()
	return c.ctx.Err()
}

// Close gracefully shuts down the client
func (c *Client) Close() error {
	c.cancel()
	if c.conn != nil {
		c.conn.Close()
	}
	c.wg.Wait()
	return nil
}

// registerModule registers this client as a response_tracer module
func (c *Client) registerModule() error {
	regReq := RegisterModuleRequest{
		Type: "response_tracer",
		Name: fmt.Sprintf("dora-execution-time-%d", c.clientID),
		Config: map[string]interface{}{
			// Extract block information from engine_newPayloadV4 requests
			"request_select": `select(.method == "engine_newPayloadV4") | {method: .method, blockNumber: .params[0].blockNumber, blockHash: .params[0].blockHash}`,
		},
	}

	response, err := c.sendRequest("register_module", regReq, nil)
	if err != nil {
		return err
	}

	if response.Error != nil {
		return fmt.Errorf("registration failed: %s", *response.Error)
	}

	if regResp, ok := response.Data.(map[string]interface{}); ok {
		if success, ok := regResp["success"].(bool); ok && success {
			if moduleID, ok := regResp["module_id"].(float64); ok {
				c.moduleID = uint64(moduleID)
				return nil
			}
		}
		if msg, ok := regResp["message"].(string); ok {
			return fmt.Errorf("registration failed: %s", msg)
		}
	}

	return fmt.Errorf("invalid registration response")
}

// sendRequest sends a request and waits for the response
func (c *Client) sendRequest(method string, data interface{}, binaryData []byte) (*WSMessageWithBinary, error) {
	requestID := atomic.AddUint64(&c.requestCounter, 1)

	msg := WSMessage{
		RequestID: requestID,
		Method:    method,
		Data:      data,
		Timestamp: time.Now().UnixNano(),
		Binary:    binaryData != nil,
	}

	// Register pending request
	responseChan := make(chan *WSMessageWithBinary, 1)
	c.requestMu.Lock()
	c.pendingRequests[requestID] = responseChan
	c.requestMu.Unlock()

	// Cleanup on exit
	defer func() {
		c.requestMu.Lock()
		delete(c.pendingRequests, requestID)
		c.requestMu.Unlock()
	}()

	// Send the request
	if err := c.conn.WriteJSON(msg); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if binaryData != nil {
		if err := c.conn.WriteMessage(websocket.BinaryMessage, binaryData); err != nil {
			return nil, fmt.Errorf("failed to send binary message: %w", err)
		}
	}

	// Wait for response with timeout
	ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()

	select {
	case response := <-responseChan:
		return response, nil
	case <-ctx.Done():
		if c.ctx.Err() != nil {
			return nil, fmt.Errorf("context cancelled: %w", c.ctx.Err())
		}
		return nil, fmt.Errorf("request timeout")
	}
}

// handleMessages processes incoming websocket messages
func (c *Client) handleMessages() {
	defer c.wg.Done()
	defer c.conn.Close()

	var expectingBinary bool
	var lastJSONMessage *WSMessage

	// Handle context cancellation
	go func() {
		<-c.ctx.Done()
		c.conn.Close()
	}()

	for {
		messageType, data, err := c.conn.ReadMessage()
		if err != nil {
			select {
			case <-c.ctx.Done():
				return
			default:
				if websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					c.logger.Info("snooper client connection closed")
				} else {
					c.logger.WithError(err).Error("snooper client read error")
				}
				c.cancel()
				return
			}
		}

		switch messageType {
		case websocket.TextMessage:
			var msg WSMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				c.logger.WithError(err).Debug("failed to unmarshal JSON message")
				continue
			}

			if msg.Binary {
				expectingBinary = true
				lastJSONMessage = &msg
			} else {
				c.handleJSONMessage(&WSMessageWithBinary{
					WSMessage:  &msg,
					BinaryData: nil,
				})
			}

		case websocket.BinaryMessage:
			if expectingBinary && lastJSONMessage != nil {
				msgWithBinary := &WSMessageWithBinary{
					WSMessage:  lastJSONMessage,
					BinaryData: data,
				}
				c.handleJSONMessage(msgWithBinary)
				expectingBinary = false
				lastJSONMessage = nil
			} else {
				c.logger.Warn("received unexpected binary message")
			}
		}
	}
}

// handleJSONMessage routes incoming messages
func (c *Client) handleJSONMessage(msg *WSMessageWithBinary) {
	if msg.ResponseID != 0 {
		// Handle response to our request
		c.requestMu.RLock()
		responseChan, exists := c.pendingRequests[msg.ResponseID]
		c.requestMu.RUnlock()

		if exists {
			select {
			case responseChan <- msg:
			default:
				c.logger.WithField("response_id", msg.ResponseID).Warn("failed to deliver response")
			}
		}
		return
	}

	// Handle incoming events
	switch msg.Method {
	case "tracer_event":
		c.handleTracerEvent(msg)
	default:
		c.logger.WithField("method", msg.Method).Debug("unknown message method")
	}
}

// handleTracerEvent processes tracer events for execution time tracking
func (c *Client) handleTracerEvent(msg *WSMessageWithBinary) {
	tracerData, ok := msg.Data.(map[string]interface{})
	if !ok {
		c.logger.Debug("invalid tracer event data")
		return
	}

	// Extract timing information
	duration, ok := tracerData["duration_ms"].(float64)
	if !ok {
		c.logger.Debug("missing duration_ms in tracer event")
		return
	}

	// Extract request data to get block information
	requestData, ok := tracerData["request_data"].(map[string]interface{})
	if !ok {
		c.logger.Debug("missing request_data in tracer event")
		return
	}

	// Extract block hash and number
	blockHashStr, ok := requestData["blockHash"].(string)
	if !ok {
		c.logger.Debug("missing blockHash in request_data")
		return
	}

	blockNumberStr, ok := requestData["blockNumber"].(string)
	if !ok {
		c.logger.Debug("missing blockNumber in request_data")
		return
	}

	// Parse block hash
	blockHash := common.HexToHash(blockHashStr)
	if blockHash == (common.Hash{}) {
		c.logger.WithField("block_hash", blockHashStr).Debug("invalid block hash")
		return
	}

	// Parse block number (hex string)
	var blockNumber uint64
	if _, err := fmt.Sscanf(blockNumberStr, "0x%x", &blockNumber); err != nil {
		c.logger.WithField("block_number", blockNumberStr).WithError(err).Debug("failed to parse block number")
		return
	}

	// Create execution time event
	event := &ExecutionTimeEvent{
		BlockHash:     blockHash,
		BlockNumber:   blockNumber,
		ClientID:      c.clientID,
		ExecutionTime: time.Duration(duration) * time.Millisecond,
		Timestamp:     time.Now(),
	}

	c.logger.WithFields(logrus.Fields{
		"block_hash":     blockHash.Hex(),
		"block_number":   blockNumber,
		"execution_time": event.ExecutionTime,
	}).Debug("received execution time event")

	c.executionTimeDispatcher.Fire(event)
}
