package snooper

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

// Client represents a generic websocket client connection to an rpc-snooper instance
type Client struct {
	url    string
	logger logrus.FieldLogger
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Connection state
	conn              *websocket.Conn
	connMu            sync.RWMutex
	connected         atomic.Bool
	reconnectDelay    time.Duration
	maxReconnectDelay time.Duration

	// Request/response handling
	requestCounter  uint64
	pendingRequests map[uint64]chan *WSMessageWithBinary
	requestMu       sync.RWMutex

	// Module management
	modules   []*ModuleRegistration
	modulesMu sync.RWMutex
}

// ClientConfig holds configuration for the generic client
type ClientConfig struct {
	URL               string
	Logger            logrus.FieldLogger
	ReconnectDelay    time.Duration
	MaxReconnectDelay time.Duration
}

// NewClient creates a new generic snooper client
func NewClient(config ClientConfig) *Client {
	ctx, cancel := context.WithCancel(context.Background())

	if config.ReconnectDelay == 0 {
		config.ReconnectDelay = 5 * time.Second
	}
	if config.MaxReconnectDelay == 0 {
		config.MaxReconnectDelay = 5 * time.Minute
	}

	return &Client{
		url:               config.URL,
		logger:            config.Logger.WithField("component", "snooper-client"),
		ctx:               ctx,
		cancel:            cancel,
		reconnectDelay:    config.ReconnectDelay,
		maxReconnectDelay: config.MaxReconnectDelay,
		pendingRequests:   make(map[uint64]chan *WSMessageWithBinary),
		modules:           make([]*ModuleRegistration, 0),
	}
}

// RegisterModule registers a module configuration that will be registered with the snooper
func (c *Client) RegisterModule(config ModuleConfig) (*ModuleRegistration, error) {
	c.modulesMu.Lock()
	defer c.modulesMu.Unlock()

	module := &ModuleRegistration{
		Config: config,
	}
	c.modules = append(c.modules, module)

	c.logger.WithFields(logrus.Fields{
		"module_type": config.Type,
		"module_name": config.Name,
	}).Debug("module registered")

	// If already connected, register the module immediately
	if c.connected.Load() {
		go c.registerModuleWithSnooper(module)
	}

	return module, nil
}

// UnregisterModule unregisters a module
func (c *Client) UnregisterModule(module *ModuleRegistration) error {
	c.modulesMu.Lock()
	defer c.modulesMu.Unlock()

	// Find and remove module from list
	for i, m := range c.modules {
		if m == module {
			// If connected and module has been registered with snooper, send unregister request
			if c.connected.Load() && module.ModuleID != 0 {
				if err := c.unregisterModuleFromSnooper(module.ModuleID); err != nil {
					c.logger.WithError(err).WithFields(logrus.Fields{
						"module_type": module.Config.Type,
						"module_name": module.Config.Name,
					}).Warn("failed to unregister module from snooper")
					// Continue with local cleanup even if remote unregister fails
				}
			}

			// Remove from slice
			c.modules = append(c.modules[:i], c.modules[i+1:]...)

			c.logger.WithFields(logrus.Fields{
				"module_type": module.Config.Type,
				"module_name": module.Config.Name,
			}).Debug("module unregistered")
			return nil
		}
	}

	return fmt.Errorf("module not found")
}

// Start begins the client connection with automatic reconnection
func (c *Client) Start() error {
	c.wg.Add(1)
	go c.runWithReconnection()
	return nil
}

// Stop gracefully shuts down the client
func (c *Client) Stop() error {
	c.cancel()

	c.connMu.Lock()
	if c.conn != nil {
		c.conn.Close()
	}
	c.connMu.Unlock()

	c.wg.Wait()
	return nil
}

// IsConnected returns whether the client is currently connected
func (c *Client) IsConnected() bool {
	return c.connected.Load()
}

// runWithReconnection manages the connection lifecycle with exponential backoff
func (c *Client) runWithReconnection() {
	defer c.wg.Done()

	backoffDelay := c.reconnectDelay

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		c.logger.Debug("attempting to connect to snooper")

		err := c.connect()
		if err != nil {
			c.logger.WithError(err).WithField("backoff", backoffDelay).Warn("failed to connect to snooper, retrying")

			select {
			case <-c.ctx.Done():
				return
			case <-time.After(backoffDelay):
			}

			// Exponential backoff
			backoffDelay = time.Duration(float64(backoffDelay) * 1.5)
			if backoffDelay > c.maxReconnectDelay {
				backoffDelay = c.maxReconnectDelay
			}
			continue
		}

		// Reset backoff on successful connection
		backoffDelay = c.reconnectDelay
		c.connected.Store(true)
		c.logger.Debug("connected to snooper")

		// Register all modules
		go c.registerAllModules()

		// Handle messages until disconnection
		c.handleMessages()

		c.connected.Store(false)

		select {
		case <-c.ctx.Done():
			return
		default:
			c.logger.Debug("disconnected from snooper, will attempt reconnection")
		}
	}
}

// connect establishes the websocket connection
func (c *Client) connect() error {
	// Parse and validate URL
	u, err := url.Parse(c.url)
	if err != nil {
		return fmt.Errorf("invalid snooper URL: %w", err)
	}

	// Switch to websocket
	if strings.HasPrefix(u.Scheme, "http") {
		u.Scheme = strings.Replace(u.Scheme, "http", "ws", 1)
	}

	// Ensure the path ends with the control endpoint
	if !strings.HasSuffix(u.Path, "/_snooper/control") {
		u.Path = u.Path + "/_snooper/control"
	}

	// Extract credentials if present
	var headers http.Header
	if u.User != nil {
		headers = make(http.Header)
		username := u.User.Username()
		password, _ := u.User.Password()
		auth := username + ":" + password
		basicAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
		headers.Set("Authorization", basicAuth)

		// Remove credentials from URL
		u.User = nil
	}

	c.logger.WithField("url", u.String()).Debug("connecting to snooper control endpoint")

	// Establish websocket connection
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.DialContext(c.ctx, u.String(), headers)
	if err != nil {
		return fmt.Errorf("websocket dial failed: %w", err)
	}

	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()

	return nil
}

// registerAllModules registers all configured modules with the snooper
func (c *Client) registerAllModules() {
	c.modulesMu.RLock()
	modules := make([]*ModuleRegistration, len(c.modules))
	copy(modules, c.modules)
	c.modulesMu.RUnlock()

	for _, module := range modules {
		if err := c.registerModuleWithSnooper(module); err != nil {
			c.logger.WithError(err).WithFields(logrus.Fields{
				"module_type": module.Config.Type,
				"module_name": module.Config.Name,
			}).Error("failed to register module")
		}
	}
}

// registerModuleWithSnooper registers a single module with the snooper
func (c *Client) registerModuleWithSnooper(module *ModuleRegistration) error {
	regReq := RegisterModuleRequest{
		Type:   module.Config.Type,
		Name:   module.Config.Name,
		Config: module.Config.Config,
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
			if modID, ok := regResp["module_id"].(float64); ok {
				module.mu.Lock()
				module.ModuleID = uint64(modID)
				module.mu.Unlock()

				c.logger.WithFields(logrus.Fields{
					"module_type":       module.Config.Type,
					"module_name":       module.Config.Name,
					"snooper_module_id": module.ModuleID,
				}).Debug("module registered with snooper")

				return nil
			}
		}
		if msg, ok := regResp["message"].(string); ok {
			return fmt.Errorf("registration failed: %s", msg)
		}
	}

	return fmt.Errorf("invalid registration response")
}

// unregisterModuleFromSnooper unregisters a module from the snooper
func (c *Client) unregisterModuleFromSnooper(snooperModuleID uint64) error {
	unregReq := map[string]interface{}{
		"module_id": snooperModuleID,
	}

	response, err := c.sendRequest("unregister_module", unregReq, nil)
	if err != nil {
		return err
	}

	if response.Error != nil {
		return fmt.Errorf("unregistration failed: %s", *response.Error)
	}

	c.logger.WithField("snooper_module_id", snooperModuleID).Debug("module unregistered from snooper")
	return nil
}

// sendRequest sends a request and waits for the response
func (c *Client) sendRequest(method string, data interface{}, binaryData []byte) (*WSMessageWithBinary, error) {
	if !c.connected.Load() {
		return nil, fmt.Errorf("not connected")
	}

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
	c.connMu.RLock()
	conn := c.conn
	c.connMu.RUnlock()

	if conn == nil {
		return nil, fmt.Errorf("connection closed")
	}

	if err := conn.WriteJSON(msg); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if binaryData != nil {
		if err := conn.WriteMessage(websocket.BinaryMessage, binaryData); err != nil {
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
	var expectingBinary bool
	var lastJSONMessage *WSMessage

	// Handle context cancellation
	go func() {
		<-c.ctx.Done()
		c.connMu.RLock()
		if c.conn != nil {
			c.conn.Close()
		}
		c.connMu.RUnlock()
	}()

	for {
		c.connMu.RLock()
		conn := c.conn
		c.connMu.RUnlock()

		if conn == nil {
			return
		}

		messageType, data, err := conn.ReadMessage()
		if err != nil {
			select {
			case <-c.ctx.Done():
				return
			default:
				if websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					c.logger.Info("snooper connection closed")
				} else {
					c.logger.WithError(err).Error("snooper read error")
				}
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

	//c.logger.WithField("client_id", c.url).Infof("received message: %+v", msg.WSMessage)

	// Handle incoming events - route to appropriate module based on ModuleID
	if msg.ModuleID != 0 {
		c.routeEventToModule(msg)
	} else {
		c.logger.WithField("method", msg.Method).Debug("received event without module ID")
	}
}

// routeEventToModule routes events to the appropriate module
func (c *Client) routeEventToModule(msg *WSMessageWithBinary) {
	c.modulesMu.RLock()
	defer c.modulesMu.RUnlock()

	// Find module by snooper module ID
	for _, module := range c.modules {
		module.mu.RLock()
		moduleID := module.ModuleID
		module.mu.RUnlock()

		if moduleID == msg.ModuleID {
			// Fire event to module's dispatcher
			func() {
				defer func() {
					if r := recover(); r != nil {
						c.logger.WithField("panic", r).Error("panic in event dispatcher")
					}
				}()
				module.FireEvent(msg)
			}()
			return
		}
	}

	c.logger.WithFields(logrus.Fields{
		"module_id": msg.ModuleID,
		"method":    msg.Method,
	}).Debug("received event for unknown module")
}
