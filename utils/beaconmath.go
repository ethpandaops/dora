package utils

import (
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/params"
	"github.com/shopspring/decimal"
)

// EpochOfSlot returns the corresponding epoch of a slot
func EpochOfSlot(slot uint64) uint64 {
	return slot / Config.Chain.Config.SlotsPerEpoch
}

// DayOfSlot returns the corresponding day of a slot
func DayOfSlot(slot uint64) uint64 {
	return Config.Chain.Config.SecondsPerSlot * slot / (24 * 3600)
}

// WeekOfSlot returns the corresponding week of a slot
func WeekOfSlot(slot uint64) uint64 {
	return Config.Chain.Config.SecondsPerSlot * slot / (7 * 24 * 3600)
}

// SlotToTime returns a time.Time to slot
func SlotToTime(slot uint64) time.Time {
	return time.Unix(int64(Config.Chain.GenesisTimestamp+slot*Config.Chain.Config.SecondsPerSlot), 0)
}

// TimeToSlot returns time to slot in seconds
func TimeToSlot(timestamp uint64) uint64 {
	if Config.Chain.GenesisTimestamp > timestamp {
		return 0
	}
	return (timestamp - Config.Chain.GenesisTimestamp) / Config.Chain.Config.SecondsPerSlot
}

func TimeToFirstSlotOfEpoch(timestamp uint64) uint64 {
	slot := TimeToSlot(timestamp)
	lastEpochOffset := slot % Config.Chain.Config.SlotsPerEpoch
	slot = slot - lastEpochOffset
	return slot
}

// EpochToTime will return a time.Time for an epoch
func EpochToTime(epoch uint64) time.Time {
	return time.Unix(int64(Config.Chain.GenesisTimestamp+epoch*Config.Chain.Config.SecondsPerSlot*Config.Chain.Config.SlotsPerEpoch), 0)
}

// TimeToDay will return a days since genesis for an timestamp
func TimeToDay(timestamp uint64) uint64 {
	return uint64(time.Unix(int64(timestamp), 0).Sub(time.Unix(int64(Config.Chain.GenesisTimestamp), 0)).Hours() / 24)
	// return time.Unix(int64(Config.Chain.GenesisTimestamp), 0).Add(time.Hour * time.Duration(24*int(day)))
}

func DayToTime(day int64) time.Time {
	return time.Unix(int64(Config.Chain.GenesisTimestamp), 0).Add(time.Hour * time.Duration(24*int(day)))
}

// TimeToEpoch will return an epoch for a given time
func TimeToEpoch(ts time.Time) int64 {
	if int64(Config.Chain.GenesisTimestamp) > ts.Unix() {
		return 0
	}
	return (ts.Unix() - int64(Config.Chain.GenesisTimestamp)) / int64(Config.Chain.Config.SecondsPerSlot) / int64(Config.Chain.Config.SlotsPerEpoch)
}

func WeiToEther(wei *big.Int) decimal.Decimal {
	return decimal.NewFromBigInt(wei, 0).DivRound(decimal.NewFromInt(params.Ether), 18)
}

func WeiBytesToEther(wei []byte) decimal.Decimal {
	return WeiToEther(new(big.Int).SetBytes(wei))
}

func GWeiToEther(gwei *big.Int) decimal.Decimal {
	return decimal.NewFromBigInt(gwei, 0).Div(decimal.NewFromInt(params.GWei))
}

func GWeiBytesToEther(gwei []byte) decimal.Decimal {
	return GWeiToEther(new(big.Int).SetBytes(gwei))
}
