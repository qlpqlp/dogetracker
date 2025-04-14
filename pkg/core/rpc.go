package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/dogeorg/dogetracker/pkg/spec"
)

// NewCoreRPCClient returns a Dogecoin Core Node client.
// Thread-safe, can be shared across Goroutines.
func NewCoreRPCClient(rpcHost string, rpcPort int, rpcUser string, rpcPass string) spec.Blockchain {
	url := fmt.Sprintf("http://%s:%d", rpcHost, rpcPort)
	return &CoreRPCClient{url: url, user: rpcUser, pass: rpcPass}
}

type CoreRPCClient struct {
	url  string
	user string
	pass string
	id   atomic.Uint64 // next unique request id
	lock sync.Mutex
}

func (c *CoreRPCClient) GetBlockHeader(blockHash string) (txn spec.BlockHeader, err error) {
	decode := true // to get back JSON rather than HEX
	err = c.Request("getblockheader", []any{blockHash, decode}, &txn)
	return
}

func (c *CoreRPCClient) GetBlock(blockHash string) (hex string, err error) {
	decode := false // to get back HEX
	err = c.Request("getblock", []any{blockHash, decode}, &hex)
	return
}

func (c *CoreRPCClient) GetBlockHash(blockHeight int64) (hash string, err error) {
	err = c.Request("getblockhash", []any{blockHeight}, &hash)
	return
}

func (c *CoreRPCClient) GetBestBlockHash() (blockHash string, err error) {
	err = c.Request("getbestblockhash", []any{}, &blockHash)
	return
}

func (c *CoreRPCClient) GetBlockCount() (blockCount int64, err error) {
	err = c.Request("getblockcount", []any{}, &blockCount)
	return
}

func (c *CoreRPCClient) Request(method string, params []any, result any) error {
	id := c.id.Add(1) // each request should use a unique ID
	c.lock.Lock()
	defer c.lock.Unlock()
	body := rpcRequest{
		Method: method,
		Params: params,
		Id:     id,
	}
	payload, err := json.Marshal(body) // HERE
	if err != nil {
		return fmt.Errorf("json-rpc marshal request: %v", err)
	}
	req, err := http.NewRequest("POST", c.url, bytes.NewBuffer(payload)) // HERE
	if err != nil {
		return fmt.Errorf("json-rpc request: %v", err)
	}
	req.SetBasicAuth(c.user, c.pass)
	res, err := http.DefaultClient.Do(req) // HERE
	if err != nil {
		return fmt.Errorf("json-rpc transport: %v", err)
	}
	// we MUST read all of res.Body and call res.Close,
	// otherwise the underlying connection cannot be re-used.
	defer res.Body.Close()
	res_bytes, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("json-rpc read response: %v", err)
	}
	if res.StatusCode != 200 {
		return fmt.Errorf("json-rpc status code: %s", res.Status)
	}
	// cannot use json.NewDecoder: "The decoder introduces its own buffering
	// and may read data from r beyond the JSON values requested."
	var rpcres rpcResponse
	err = json.Unmarshal(res_bytes, &rpcres)
	if err != nil {
		return fmt.Errorf("json-rpc unmarshal response: %v", err)
	}
	if rpcres.Id != body.Id {
		return fmt.Errorf("json-rpc wrong ID returned: %v vs %v", rpcres.Id, body.Id)
	}
	if rpcres.Error != nil {
		return fmt.Errorf("json-rpc error returned: %v", rpcres.Error)
	}
	if rpcres.Result == nil {
		return fmt.Errorf("json-rpc missing result")
	}
	err = json.Unmarshal(*rpcres.Result, result)
	if err != nil {
		return fmt.Errorf("json-rpc unmarshal result: %v | %v", err, string(*rpcres.Result))
	}
	return nil
}

type rpcRequest struct {
	Method string `json:"method"`
	Params []any  `json:"params"`
	Id     uint64 `json:"id"`
}
type rpcResponse struct {
	Id     uint64           `json:"id"`
	Result *json.RawMessage `json:"result"`
	Error  any              `json:"error"`
}

// GetRawTransaction retrieves a raw transaction by its ID
func (c *CoreRPCClient) GetRawTransaction(txID string) (string, error) {
	params := []interface{}{txID, 0} // 0 means return hex string
	var result string
	err := c.Request("getrawtransaction", params, &result)
	if err != nil {
		return "", fmt.Errorf("failed to get raw transaction: %v", err)
	}
	return result, nil
}

// DecodeRawTransaction decodes a raw transaction hex string
func (c *CoreRPCClient) DecodeRawTransaction(hex string) (spec.Transaction, error) {
	params := []interface{}{hex}
	var result spec.Transaction
	err := c.Request("decoderawtransaction", params, &result)
	if err != nil {
		return spec.Transaction{}, fmt.Errorf("failed to decode raw transaction: %v", err)
	}
	return result, nil
}

// GetRawMempool returns all transaction IDs in the mempool
func (c *CoreRPCClient) GetRawMempool() ([]string, error) {
	var result []string
	err := c.Request("getrawmempool", []any{}, &result)
	if err != nil {
		return nil, fmt.Errorf("failed to get raw mempool: %v", err)
	}
	return result, nil
}

// GetBlockVerbose retrieves a block with full transaction details
func (c *CoreRPCClient) GetBlockVerbose(blockHash string) (*spec.Block, error) {
	params := []interface{}{blockHash, 2} // 2 means full transaction details
	var block spec.Block
	err := c.Request("getblock", params, &block)
	if err != nil {
		return nil, fmt.Errorf("failed to get block: %v", err)
	}
	return &block, nil
}
