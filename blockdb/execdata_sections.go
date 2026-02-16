package blockdb

import (
	"encoding/binary"
	"fmt"
	"math/big"
)

// Call type constants matching the callTracer output.
const (
	CallTypeCall         = 0
	CallTypeStaticCall   = 1
	CallTypeDelegateCall = 2
	CallTypeCreate       = 3
	CallTypeCreate2      = 4
	CallTypeSelfDestruct = 5
)

// Call status constants for binary encoding.
const (
	CallStatusSuccess  = 0
	CallStatusReverted = 1
	CallStatusError    = 2
)

// EventData holds the data for a single event log to be encoded into the
// events section of the execution data object.
type EventData struct {
	EventIndex uint32
	Source     [20]byte
	Topics     [][]byte // Each topic is 32 bytes
	Data       []byte
}

// EncodeEventsSection encodes a list of events into the binary events section
// format. The returned bytes are uncompressed; the caller is responsible for
// snappy compression.
//
// Format:
//
//	Event Count:     uint32
//	Per event:
//	  Event Index:   uint32
//	  Source Address: [20]byte
//	  Topic Count:   uint8 (0-5)
//	  Topics:        [TopicCount][32]byte
//	  Data Length:    uint32
//	  Data:          [DataLength]byte
func EncodeEventsSection(events []EventData) []byte {
	// Calculate total size
	size := 4 // event count
	for i := range events {
		size += 4                          // event index
		size += 20                         // source address
		size += 1                          // topic count
		size += len(events[i].Topics) * 32 // topics
		size += 4                          // data length
		size += len(events[i].Data)        // data
	}

	buf := make([]byte, size)
	offset := 0

	// Event count
	binary.BigEndian.PutUint32(buf[offset:offset+4], uint32(len(events)))
	offset += 4

	for i := range events {
		ev := &events[i]

		// Event index
		binary.BigEndian.PutUint32(buf[offset:offset+4], ev.EventIndex)
		offset += 4

		// Source address
		copy(buf[offset:offset+20], ev.Source[:])
		offset += 20

		// Topic count
		topicCount := min(len(ev.Topics), 5)
		buf[offset] = uint8(topicCount)
		offset++

		// Topics
		for j := range topicCount {
			if len(ev.Topics[j]) >= 32 {
				copy(buf[offset:offset+32], ev.Topics[j][:32])
			}
			offset += 32
		}

		// Data length + data
		binary.BigEndian.PutUint32(buf[offset:offset+4], uint32(len(ev.Data)))
		offset += 4
		copy(buf[offset:offset+len(ev.Data)], ev.Data)
		offset += len(ev.Data)
	}

	return buf[:offset]
}

// FlatCallFrame is a single call frame in a flattened depth-first call trace.
type FlatCallFrame struct {
	Depth   uint16
	Type    uint8 // CallType* constants
	From    [20]byte
	To      [20]byte
	Value   *big.Int // nil or zero means no value
	Gas     uint64
	GasUsed uint64
	Status  uint8 // CallStatus* constants
	Input   []byte
	Output  []byte
	Error   string
	Logs    []CallFrameLog
}

// CallFrameLog is a log emitted within a call frame.
type CallFrameLog struct {
	Address [20]byte
	Topics  [][]byte // Each topic is 32 bytes
	Data    []byte
}

// EncodeCallTraceSection encodes a flattened call trace into the binary call
// trace section format. The returned bytes are uncompressed.
//
// Format:
//
//	Version:       uint16
//	CallCount:     uint32
//	Per call (depth-first order):
//	  Depth:       uint16
//	  CallType:    uint8
//	  From:        [20]byte
//	  To:          [20]byte
//	  ValueLen:    uint8
//	  Value:       [ValueLen]byte (big-endian uint256)
//	  Gas:         uint64
//	  GasUsed:     uint64
//	  Status:      uint8
//	  InputLen:    uint32
//	  Input:       [InputLen]byte
//	  OutputLen:   uint32
//	  Output:      [OutputLen]byte
//	  ErrorLen:    uint16
//	  Error:       [ErrorLen]byte
//	  LogCount:    uint16
//	  Per log:
//	    Address:   [20]byte
//	    TopicCount: uint8
//	    Topics:    [TopicCount][32]byte
//	    DataLen:   uint32
//	    Data:      [DataLen]byte
func EncodeCallTraceSection(calls []FlatCallFrame) []byte {
	// Calculate total size
	size := 2 + 4 // version + call count
	for i := range calls {
		c := &calls[i]
		size += 2 + 1 + 20 + 20 // depth + type + from + to
		size += 1               // valueLen
		size += bigIntCompactLen(c.Value)
		size += 8 + 8 + 1 // gas + gasUsed + status
		size += 4 + len(c.Input)
		size += 4 + len(c.Output)
		size += 2 + len(c.Error)
		size += 2 // log count
		for j := range c.Logs {
			size += 20                         // address
			size += 1                          // topic count
			size += len(c.Logs[j].Topics) * 32 // topics
			size += 4 + len(c.Logs[j].Data)    // data len + data
		}
	}

	buf := make([]byte, size)
	offset := 0

	// Version
	binary.BigEndian.PutUint16(buf[offset:offset+2], 1)
	offset += 2

	// Call count
	binary.BigEndian.PutUint32(buf[offset:offset+4], uint32(len(calls)))
	offset += 4

	for i := range calls {
		c := &calls[i]

		// Depth
		binary.BigEndian.PutUint16(buf[offset:offset+2], c.Depth)
		offset += 2

		// CallType
		buf[offset] = c.Type
		offset++

		// From
		copy(buf[offset:offset+20], c.From[:])
		offset += 20

		// To
		copy(buf[offset:offset+20], c.To[:])
		offset += 20

		// Value (compact big-endian encoding)
		valBytes := bigIntCompactBytes(c.Value)
		buf[offset] = uint8(len(valBytes))
		offset++
		copy(buf[offset:offset+len(valBytes)], valBytes)
		offset += len(valBytes)

		// Gas
		binary.BigEndian.PutUint64(buf[offset:offset+8], c.Gas)
		offset += 8

		// GasUsed
		binary.BigEndian.PutUint64(buf[offset:offset+8], c.GasUsed)
		offset += 8

		// Status
		buf[offset] = c.Status
		offset++

		// Input
		binary.BigEndian.PutUint32(buf[offset:offset+4], uint32(len(c.Input)))
		offset += 4
		copy(buf[offset:offset+len(c.Input)], c.Input)
		offset += len(c.Input)

		// Output
		binary.BigEndian.PutUint32(buf[offset:offset+4], uint32(len(c.Output)))
		offset += 4
		copy(buf[offset:offset+len(c.Output)], c.Output)
		offset += len(c.Output)

		// Error
		errBytes := []byte(c.Error)
		binary.BigEndian.PutUint16(buf[offset:offset+2], uint16(len(errBytes)))
		offset += 2
		copy(buf[offset:offset+len(errBytes)], errBytes)
		offset += len(errBytes)

		// Logs
		binary.BigEndian.PutUint16(buf[offset:offset+2], uint16(len(c.Logs)))
		offset += 2
		for j := range c.Logs {
			log := &c.Logs[j]

			// Address
			copy(buf[offset:offset+20], log.Address[:])
			offset += 20

			// Topic count
			topicCount := min(len(log.Topics), 5)
			buf[offset] = uint8(topicCount)
			offset++

			// Topics
			for k := range topicCount {
				if len(log.Topics[k]) >= 32 {
					copy(buf[offset:offset+32], log.Topics[k][:32])
				}
				offset += 32
			}

			// Data
			binary.BigEndian.PutUint32(buf[offset:offset+4], uint32(len(log.Data)))
			offset += 4
			copy(buf[offset:offset+len(log.Data)], log.Data)
			offset += len(log.Data)
		}
	}

	return buf[:offset]
}

// DecodeEventsSection decodes the binary events section format.
func DecodeEventsSection(data []byte) ([]EventData, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("events section too short: %d bytes", len(data))
	}

	eventCount := binary.BigEndian.Uint32(data[0:4])
	offset := 4

	events := make([]EventData, 0, eventCount)
	for i := range eventCount {
		_ = i
		if offset+25 > len(data) {
			return nil, fmt.Errorf("events section truncated at event header")
		}

		var ev EventData
		ev.EventIndex = binary.BigEndian.Uint32(data[offset : offset+4])
		offset += 4

		copy(ev.Source[:], data[offset:offset+20])
		offset += 20

		topicCount := int(data[offset])
		offset++

		if offset+topicCount*32 > len(data) {
			return nil, fmt.Errorf("events section truncated at topics")
		}

		ev.Topics = make([][]byte, topicCount)
		for j := range topicCount {
			topic := make([]byte, 32)
			copy(topic, data[offset:offset+32])
			ev.Topics[j] = topic
			offset += 32
		}

		if offset+4 > len(data) {
			return nil, fmt.Errorf("events section truncated at data length")
		}
		dataLen := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		offset += 4

		if offset+dataLen > len(data) {
			return nil, fmt.Errorf("events section truncated at data")
		}
		ev.Data = make([]byte, dataLen)
		copy(ev.Data, data[offset:offset+dataLen])
		offset += dataLen

		events = append(events, ev)
	}

	return events, nil
}

// DecodeCallTraceSection decodes the binary call trace section format.
func DecodeCallTraceSection(data []byte) ([]FlatCallFrame, error) {
	if len(data) < 6 {
		return nil, fmt.Errorf("call trace section too short: %d bytes", len(data))
	}

	version := binary.BigEndian.Uint16(data[0:2])
	if version != 1 {
		return nil, fmt.Errorf("unsupported call trace version: %d", version)
	}

	callCount := binary.BigEndian.Uint32(data[2:6])
	offset := 6

	calls := make([]FlatCallFrame, 0, callCount)
	for i := range callCount {
		_ = i
		if offset+44 > len(data) {
			return nil, fmt.Errorf("call trace truncated at call header")
		}

		var c FlatCallFrame
		c.Depth = binary.BigEndian.Uint16(data[offset : offset+2])
		offset += 2

		c.Type = data[offset]
		offset++

		copy(c.From[:], data[offset:offset+20])
		offset += 20

		copy(c.To[:], data[offset:offset+20])
		offset += 20

		// Value
		valLen := int(data[offset])
		offset++
		if offset+valLen > len(data) {
			return nil, fmt.Errorf("call trace truncated at value")
		}
		if valLen > 0 {
			c.Value = new(big.Int).SetBytes(data[offset : offset+valLen])
		} else {
			c.Value = big.NewInt(0)
		}
		offset += valLen

		if offset+17 > len(data) {
			return nil, fmt.Errorf("call trace truncated at gas fields")
		}

		c.Gas = binary.BigEndian.Uint64(data[offset : offset+8])
		offset += 8
		c.GasUsed = binary.BigEndian.Uint64(data[offset : offset+8])
		offset += 8
		c.Status = data[offset]
		offset++

		// Input
		if offset+4 > len(data) {
			return nil, fmt.Errorf("call trace truncated at input length")
		}
		inputLen := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		offset += 4
		if offset+inputLen > len(data) {
			return nil, fmt.Errorf("call trace truncated at input")
		}
		c.Input = make([]byte, inputLen)
		copy(c.Input, data[offset:offset+inputLen])
		offset += inputLen

		// Output
		if offset+4 > len(data) {
			return nil, fmt.Errorf("call trace truncated at output length")
		}
		outputLen := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		offset += 4
		if offset+outputLen > len(data) {
			return nil, fmt.Errorf("call trace truncated at output")
		}
		c.Output = make([]byte, outputLen)
		copy(c.Output, data[offset:offset+outputLen])
		offset += outputLen

		// Error
		if offset+2 > len(data) {
			return nil, fmt.Errorf("call trace truncated at error length")
		}
		errLen := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		offset += 2
		if offset+errLen > len(data) {
			return nil, fmt.Errorf("call trace truncated at error")
		}
		c.Error = string(data[offset : offset+errLen])
		offset += errLen

		// Logs
		if offset+2 > len(data) {
			return nil, fmt.Errorf("call trace truncated at log count")
		}
		logCount := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		offset += 2

		c.Logs = make([]CallFrameLog, 0, logCount)
		for j := range logCount {
			_ = j
			if offset+21 > len(data) {
				return nil, fmt.Errorf("call trace truncated at log header")
			}

			var log CallFrameLog
			copy(log.Address[:], data[offset:offset+20])
			offset += 20

			topicCount := int(data[offset])
			offset++

			if offset+topicCount*32 > len(data) {
				return nil, fmt.Errorf("call trace truncated at log topics")
			}
			log.Topics = make([][]byte, topicCount)
			for k := range topicCount {
				topic := make([]byte, 32)
				copy(topic, data[offset:offset+32])
				log.Topics[k] = topic
				offset += 32
			}

			if offset+4 > len(data) {
				return nil, fmt.Errorf("call trace truncated at log data length")
			}
			logDataLen := int(binary.BigEndian.Uint32(data[offset : offset+4]))
			offset += 4
			if offset+logDataLen > len(data) {
				return nil, fmt.Errorf("call trace truncated at log data")
			}
			log.Data = make([]byte, logDataLen)
			copy(log.Data, data[offset:offset+logDataLen])
			offset += logDataLen

			c.Logs = append(c.Logs, log)
		}

		calls = append(calls, c)
	}

	return calls, nil
}

// bigIntCompactLen returns the compact byte length for a big.Int value.
func bigIntCompactLen(v *big.Int) int {
	if v == nil || v.Sign() == 0 {
		return 0
	}
	return len(v.Bytes())
}

// bigIntCompactBytes returns the compact big-endian bytes for a big.Int value.
func bigIntCompactBytes(v *big.Int) []byte {
	if v == nil || v.Sign() == 0 {
		return nil
	}
	return v.Bytes()
}
