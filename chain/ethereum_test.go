package chain

import (
	"encoding/json"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

// txJSON 构造一个合法的 legacy 交易 JSON（V/R/S 置零以跳过签名校验）。
func txJSON(blockNumber string) string {
	return `{"type":"0x0","nonce":"0x1","gasPrice":"0x3b9aca00","gas":"0x5208",` +
		`"to":"0x0000000000000000000000000000000000000001","value":"0x0","input":"0x",` +
		`"v":"0x0","r":"0x0","s":"0x0","blockNumber":` + blockNumber + `}`
}

// TestRPCTxResultUnmarshal 验证 eth_getTransactionByHash 结果的解析：
// 有 blockNumber 为已上链交易（非 pending），blockNumber 为 null 为 pending。
func TestRPCTxResultUnmarshal(t *testing.T) {
	// 已上链交易
	var mined rpcTxResult
	if err := json.Unmarshal([]byte(txJSON(`"0x100"`)), &mined); err != nil {
		t.Fatalf("unmarshal mined tx: %v", err)
	}
	if mined.tx == nil {
		t.Fatal("mined tx should be non-nil")
	}
	if mined.pending {
		t.Fatal("mined tx should not be pending")
	}

	// pending 交易（无 blockNumber）
	var pending rpcTxResult
	if err := json.Unmarshal([]byte(txJSON(`null`)), &pending); err != nil {
		t.Fatalf("unmarshal pending tx: %v", err)
	}
	if pending.tx == nil {
		t.Fatal("pending tx should be non-nil")
	}
	if !pending.pending {
		t.Fatal("pending tx should be pending")
	}

	// null 响应（交易不存在）
	var missing rpcTxResult
	if err := json.Unmarshal([]byte(`null`), &missing); err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	if missing.tx != nil {
		t.Fatal("null response should produce nil tx")
	}
}

// TestCallArg 验证 CallMsg 到 eth_call 参数对象的转换。
func TestCallArg(t *testing.T) {
	to := common.HexToAddress("0x0000000000000000000000000000000000000001")
	msg := ethereum.CallMsg{To: &to, Data: []byte{0xaa, 0xbb}}

	arg := callArg(msg)
	if got, ok := arg["to"].(*common.Address); !ok || got == nil || *got != to {
		t.Errorf("to = %v", arg["to"])
	}
	if _, ok := arg["input"]; !ok {
		t.Errorf("missing input field: %v", arg)
	}
}
