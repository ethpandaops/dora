package utils

import (
	"html/template"
	"math/big"
)

var (
	GWEI *big.Int = big.NewInt(1000000000)
	ETH  *big.Int = big.NewInt(0).Mul(GWEI, GWEI)
)

func tokenSymbol() template.HTML {
	return template.HTML(Config.Chain.TokenSymbol)
}
