package services

import (
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

func TestParseNameKey(t *testing.T) {
	pubkeyHex := "0x" + strings.Repeat("ab", 48)
	var pubkey phase0.BLSPubKey
	for i := range pubkey {
		pubkey[i] = 0xab
	}
	address := common.HexToAddress("0x8f614CdB61E37B6f18d9705942c93FA86B04D711")

	tests := []struct {
		key     string
		want    *nameKey
		wantErr bool
	}{
		{key: "123", want: &nameKey{kind: nameKeyRange, startIndex: 123, endIndex: 123}},
		{key: "100-199", want: &nameKey{kind: nameKeyRange, startIndex: 100, endIndex: 199}},
		{key: "pubkey:" + pubkeyHex, want: &nameKey{kind: nameKeyPubkey, pubkey: pubkey}},
		{key: "pubkey:" + pubkeyHex[2:], want: &nameKey{kind: nameKeyPubkey, pubkey: pubkey}},
		{key: "withdrawal:" + address.Hex(), want: &nameKey{kind: nameKeyWithdrawal, address: address}},
		{key: "depositor:" + address.Hex(), want: &nameKey{kind: nameKeyDepositOrigin, address: address}},
		{key: "deposit_origin:" + address.Hex(), want: &nameKey{kind: nameKeyDepositOrigin, address: address}},
		{key: "deposit_target:" + address.Hex(), want: &nameKey{kind: nameKeyDepositTarget, address: address}},
		{key: "pubkey:0x1234", wantErr: true},
		{key: "pubkey:0xzz", wantErr: true},
		{key: "unknown:0x1234", wantErr: true},
		{key: "abc", wantErr: true},
		{key: "200-100", wantErr: true},
		{key: "0-18446744073709551615", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got, err := parseNameKey(tt.key)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseNameKey(%q) = %+v, want error", tt.key, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseNameKey(%q) unexpected error: %v", tt.key, err)
			}
			if *got != *tt.want {
				t.Errorf("parseNameKey(%q) = %+v, want %+v", tt.key, got, tt.want)
			}
		})
	}
}

func TestValidatorNamesPubkeyKey(t *testing.T) {
	vn := newTestValidatorNames(map[uint64]string{}, nil)
	pubkeyHex := "0x" + strings.Repeat("cd", 48)

	count := vn.parseNamesMap(map[string]string{
		"pubkey:" + pubkeyHex: "by-pubkey",
		"5-6":                 "by-range",
	})
	if count != 3 {
		t.Errorf("parseNamesMap() count = %v, want 3", count)
	}

	var pubkey phase0.BLSPubKey
	for i := range pubkey {
		pubkey[i] = 0xcd
	}
	if entry := vn.namesByPubkey[pubkey]; entry == nil || entry.name != "by-pubkey" {
		t.Errorf("namesByPubkey[%x] = %+v, want by-pubkey", pubkey, entry)
	}
	if name := vn.GetValidatorName(6); name != "by-range" {
		t.Errorf("GetValidatorName(6) = %q, want by-range", name)
	}
}

func TestParseBuilderNamesMap(t *testing.T) {
	address := common.HexToAddress("0x8f614CdB61E37B6f18d9705942c93FA86B04D711")
	var pubkey phase0.BLSPubKey
	pubkey[0] = 0x01

	sources := parseBuilderNamesMap(map[string]string{
		"0-1": "range",
		"3":   "single",
		"pubkey:0x" + common.Bytes2Hex(pubkey[:]): "pubkey",
		"withdrawal:" + address.Hex():             "withdrawal",
		"depositor:" + address.Hex():              "depositor",
		"deposit_target:" + address.Hex():         "target",
		"invalid":                                 "ignored",
	})

	if len(sources.ranges) != 2 {
		t.Errorf("ranges = %+v, want 2 ranges", sources.ranges)
	}
	if sources.byPubkey[pubkey] != "pubkey" {
		t.Errorf("byPubkey = %v", sources.byPubkey)
	}
	if sources.byWithdrawal[address] != "withdrawal" {
		t.Errorf("byWithdrawal = %v", sources.byWithdrawal)
	}
	if sources.byDepositOrigin[address] != "depositor" {
		t.Errorf("byDepositOrigin = %v", sources.byDepositOrigin)
	}
	if sources.byDepositTarget[address] != "target" {
		t.Errorf("byDepositTarget = %v", sources.byDepositTarget)
	}
}

func TestGetBuilderName(t *testing.T) {
	var namedPubkey, supersededPubkey phase0.BLSPubKey
	namedPubkey[0] = 0x01
	supersededPubkey[0] = 0x02

	bn := &BuilderNames{}
	if name := bn.GetBuilderName(1); name != "" {
		t.Errorf("GetBuilderName() before publish = %q, want empty", name)
	}

	bn.nameIndex.Store(&builderNameIndex{
		ranges: []validatorNameRange{{startIndex: 0, endIndex: 1, name: "range"}},
		byPubkey: map[phase0.BLSPubKey]string{
			namedPubkey:      "named",
			supersededPubkey: "superseded",
		},
		byIndex: map[uint64]string{1: "resolved", 5: "named"},
	})

	tests := []struct {
		name  string
		index uint64
		want  string
	}{
		{name: "range name", index: 0, want: "range"},
		{name: "range wins over resolved name", index: 1, want: "range"},
		{name: "resolved name of current occupant", index: 5, want: "named"},
		{name: "builder index flag is ignored", index: 5 | BuilderIndexFlag, want: "named"},
		{name: "unnamed index", index: 6, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bn.GetBuilderName(tt.index); got != tt.want {
				t.Errorf("GetBuilderName(%v) = %q, want %q", tt.index, got, tt.want)
			}
		})
	}

	if got := bn.GetBuilderNameByPubkey(supersededPubkey); got != "superseded" {
		t.Errorf("GetBuilderNameByPubkey() = %q, want superseded", got)
	}
}

func TestLoadInternalBuilderNames(t *testing.T) {
	names, err := loadBuilderNamesYaml("~internal/sepolia.builders.yaml")
	if err != nil {
		t.Fatalf("loadBuilderNamesYaml() error: %v", err)
	}

	sources := parseBuilderNamesMap(names)
	address := common.HexToAddress("0x8f614CdB61E37B6f18d9705942c93FA86B04D711")
	if sources.byWithdrawal[address] != "nflaig" {
		t.Errorf("byWithdrawal[%v] = %q, want nflaig", address, sources.byWithdrawal[address])
	}
}
