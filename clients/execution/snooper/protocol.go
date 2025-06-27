package snooper

// WSMessage represents a websocket message in the snooper protocol
type WSMessage struct {
	RequestID  uint64  `json:"reqid,omitempty"`
	ResponseID uint64  `json:"rspid,omitempty"`
	ModuleID   uint64  `json:"modid,omitempty"`
	Method     string  `json:"method"`
	Data       any     `json:"data,omitempty"`
	Error      *string `json:"error,omitempty"`
	Timestamp  int64   `json:"time"`
	Binary     bool    `json:"binary,omitempty"`
}

// WSMessageWithBinary represents a websocket message with optional binary data
type WSMessageWithBinary struct {
	*WSMessage
	BinaryData []byte `json:"binary_data,omitempty"`
}

// RegisterModuleRequest represents a module registration request
type RegisterModuleRequest struct {
	Type   string         `json:"type"`
	Name   string         `json:"name"`
	Config map[string]any `json:"config"`
}

// RegisterModuleResponse represents a module registration response
type RegisterModuleResponse struct {
	Success  bool   `json:"success"`
	ModuleID uint64 `json:"module_id,omitempty"`
	Message  string `json:"message,omitempty"`
}

// TracerEvent represents a tracer event from the snooper
type TracerEvent struct {
	ModuleID     uint64 `json:"module_id"`
	RequestID    uint64 `json:"request_id"`
	Duration     int64  `json:"duration_ms"`
	ResponseSize int64  `json:"response_size"`
	RequestSize  int64  `json:"request_size"`
	StatusCode   int    `json:"status_code"`
	RequestData  any    `json:"request_data,omitempty"`
	ResponseData any    `json:"response_data,omitempty"`
}