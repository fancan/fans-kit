package abi

import (
	_ "embed"
	"strings"

	goabi "github.com/ethereum/go-ethereum/accounts/abi"
)

//go:embed erc20.json
var erc20JSON string

//go:embed multicall.json
var multicallJSON string

// ERC20 是 ERC20 的最小 ABI（name / symbol / decimals）。
var ERC20 goabi.ABI

// Multicall 是 Multicall v1/v2 的 ABI（aggregate / tryAggregate）。
var Multicall goabi.ABI

func init() {
	var err error
	if ERC20, err = goabi.JSON(strings.NewReader(erc20JSON)); err != nil {
		panic("abi: parse erc20.json: " + err.Error())
	}
	if Multicall, err = goabi.JSON(strings.NewReader(multicallJSON)); err != nil {
		panic("abi: parse multicall.json: " + err.Error())
	}
}
