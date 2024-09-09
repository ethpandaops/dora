package utils

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"net"
	"strconv"

	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/p2p/enr"
	"github.com/ethereum/go-ethereum/rlp"
)

func DecodeENR(raw string) (*enr.Record, error) {
	b := []byte(raw)
	if bytes.HasPrefix(b, []byte("enr:")) {
		b = b[4:]
	}
	dec := make([]byte, base64.RawURLEncoding.DecodedLen(len(b)))
	n, err := base64.RawURLEncoding.Decode(dec, b)
	if err != nil {
		return nil, err
	}
	var r enr.Record
	err = rlp.DecodeBytes(dec[:n], &r)
	return &r, err
}

func GetKeyValuesFromENR(r *enr.Record) map[string]interface{} {
	fields := make(map[string]interface{})
	// Get sequence number
	fields["seq"] = r.Seq()

	// Get signature
	n, err := enode.New(enode.ValidSchemes, r)
	if err == nil {
		record := n.Record()
		if record != nil {
			fields["signature"] = "0x" + hex.EncodeToString(record.Signature())
		}
	}

	// Get all key value pairs
	kv := r.AppendElements(nil)[1:]
	for i := 0; i < len(kv); i += 2 {
		key := kv[i].(string)
		val := kv[i+1].(rlp.RawValue)
		formatter := attrFormatters[key]
		if formatter == nil {
			formatter = formatAttrRaw
		}
		fmtval, ok := formatter(val)
		if ok {
			fields[key] = fmtval
		} else {
			fields[key] = "0x" + hex.EncodeToString(val) + " (!)"
		}
	}
	return fields
}

func GetNodeIDFromENR(r *enr.Record) string {
	n, err := enode.New(enode.ValidSchemes, r)
	if err != nil {
		return ""
	}
	return n.ID().String()
}

// attrFormatters contains formatting functions for well-known ENR keys.
var attrFormatters = map[string]func(rlp.RawValue) (string, bool){
	"id":   formatAttrString,
	"ip":   formatAttrIP,
	"ip6":  formatAttrIP,
	"tcp":  formatAttrUint,
	"tcp6": formatAttrUint,
	"udp":  formatAttrUint,
	"udp6": formatAttrUint,
}

func formatAttrRaw(v rlp.RawValue) (string, bool) {
	content, _, err := rlp.SplitString(v)
	return "0x" + hex.EncodeToString(content), err == nil
}

func formatAttrString(v rlp.RawValue) (string, bool) {
	content, _, err := rlp.SplitString(v)
	return string(content), err == nil
}

func formatAttrIP(v rlp.RawValue) (string, bool) {
	content, _, err := rlp.SplitString(v)
	if err != nil || len(content) != 4 && len(content) != 6 {
		return "", false
	}
	return net.IP(content).String(), true
}

func formatAttrUint(v rlp.RawValue) (string, bool) {
	var x uint64
	if err := rlp.DecodeBytes(v, &x); err != nil {
		return "", false
	}
	return strconv.FormatUint(x, 10), true
}
