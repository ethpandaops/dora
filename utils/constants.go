package utils

import "math/big"

var GWEI *big.Int = big.NewInt(1000000000)
var ETH *big.Int = big.NewInt(0).Mul(GWEI, GWEI)
