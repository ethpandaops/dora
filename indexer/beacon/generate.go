package beacon

//go:generate go tool dynssz-gen -without-dynamic-expressions -package . -legacy -output epochstats_ssz.go -types EpochStatsPacked
