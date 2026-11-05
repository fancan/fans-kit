package main

import (
	"context"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/fancan/fans-kit/chain"
)

func main() {
	if err := chain.InitFile("config/example.yaml"); err != nil {
		log.Fatal(err)
	}
	defer chain.Close()

	mux := http.NewServeMux()
	if err := chain.RegisterMetrics(mux); err != nil {
		log.Printf("metrics disabled: %v", err)
	}
	go func() { _ = http.ListenAndServe(":9090", mux) }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	block, err := chain.BlockNumber(ctx)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("ethereum block=%d", block)

	// Generic method: newly supported RPC methods require no SDK changes.
	var gas hexutil.Uint64
	if err := chain.CallInto(ctx, &gas, "eth_estimateGas", map[string]any{"to": common.HexToAddress("0x0000000000000000000000000000000000000001")}); err != nil {
		log.Printf("estimate gas: %v", err)
	}

	// Batch reads.
	var chainID hexutil.Uint64
	var latest hexutil.Uint64
	batch := []chain.BatchElem{
		{Method: "eth_chainId", Result: &chainID},
		{Method: "eth_blockNumber", Result: &latest},
	}
	if err := chain.Batch(ctx, batch); err != nil {
		log.Printf("batch: %v", err)
	}

	// Reliable logs: fromBlock is the initial replay cursor; WS handles live updates.
	logs, err := chain.SubscribeLogs(ctx, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(block),
		Addresses: []common.Address{common.HexToAddress("0x0000000000000000000000000000000000000001")},
	})
	if err != nil {
		log.Printf("subscribe logs: %v", err)
	} else {
		for {
			select {
			case <-ctx.Done():
				return
			case lg, ok := <-logs:
				if !ok {
					return
				}
				handleLog(lg)
			}
		}
	}
}

func handleLog(lg types.Log) {
	_ = time.Now()
	log.Printf("log block=%d removed=%t tx=%s", lg.BlockNumber, lg.Removed, lg.TxHash.Hex())
}
