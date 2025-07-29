package utils

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"strconv"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/p2p/enr"
	"github.com/ethereum/go-ethereum/rlp"
	libp2pCrypto "github.com/libp2p/go-libp2p/core/crypto"
	libp2pPeer "github.com/libp2p/go-libp2p/core/peer"
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
	return getKeyValuesFromENR(r, false)
}

func GetKeyValuesFromENRFiltered(r *enr.Record) map[string]interface{} {
	return getKeyValuesFromENR(r, true)
}

func getKeyValuesFromENR(r *enr.Record, filterSensitive bool) map[string]interface{} {
	fields := make(map[string]interface{})
	
	sensitiveFields := map[string]bool{
		"ip":  true,
		"ip6": true,
	}
	
	fields["seq"] = r.Seq()
	fields["signature"] = "0x" + hex.EncodeToString(r.Signature())

	kv := r.AppendElements(nil)[1:]
	for i := 0; i < len(kv); i += 2 {
		key := kv[i].(string)
		val := kv[i+1].(rlp.RawValue)
		
		if filterSensitive && sensitiveFields[key] {
			continue
		}
		
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

func GetNodeIDFromENR(r *enr.Record) enode.ID {
	n, err := enode.New(enode.ValidSchemes, r)
	if err != nil {
		return enode.ID{}
	}
	return n.ID()
}

// attrFormatters contains formatting functions for well-known ENR keys.
var attrFormatters = map[string]func(rlp.RawValue) (string, bool){
	"id":     formatAttrString,
	"ip":     formatAttrIP,
	"ip6":    formatAttrIP,
	"tcp":    formatAttrUint,
	"tcp6":   formatAttrUint,
	"udp":    formatAttrUint,
	"udp6":   formatAttrUint,
	"quic":   formatAttrUint,
	"quic6":  formatAttrUint,
	"client": formatAttrClient,
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
	if err != nil || (len(content) != 4 && len(content) != 16) {
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

func formatAttrClient(v rlp.RawValue) (string, bool) {
	var client []string
	if err := rlp.DecodeBytes(v, &client); err != nil {
		return "", false
	}
	result := ""
	for i, part := range client {
		if i > 0 {
			result += ", "
		}
		result += part
	}
	return result, true
}

// ConvertPeerIDStringToEnodeID converts a libp2p peer ID string to an enode.ID.
func ConvertPeerIDStringToEnodeID(pidStr string) (enode.ID, error) {
	// Decode the string into a peer.ID.
	pid, err := libp2pPeer.Decode(pidStr)
	if err != nil {
		return enode.ID{}, err
	}

	// Extract the public key from the peer ID.
	pubKey, err := pid.ExtractPublicKey()
	if err != nil {
		return enode.ID{}, err
	}

	// Ensure the public key is of type secp256k1.
	if pubKey.Type() != libp2pCrypto.Secp256k1 {
		return enode.ID{}, errors.New("public key is not secp256k1")
	}

	// Get the raw bytes of the public key (compressed format).
	pubKeyBytes, err := pubKey.Raw()
	if err != nil {
		return enode.ID{}, err
	}

	// Decompress the public key using Ethereum's crypto library.
	ecdsaPubKey, err := ethcrypto.DecompressPubkey(pubKeyBytes)
	if err != nil {
		return enode.ID{}, err
	}

	// Compute the enode.ID from the ECDSA public key.
	id := enode.PubkeyToIDV4(ecdsaPubKey)

	return id, nil
}
