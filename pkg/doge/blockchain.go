package doge

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// BlockchainBlock represents a block in the blockchain
type BlockchainBlock struct {
	Hash   string
	Height int64
	Time   int64
}

// Blockchain represents a connection to a Dogecoin node
type Blockchain interface {
	GetBlockHash(height int64) (string, error)
	GetBlock(hash string) ([]byte, error)
	GetBlockCount() (int64, error)
}

// NewSPVNode creates a new SPV node
func NewSPVNode() *SPVNode {
	return &SPVNode{
		peers: []string{
			"seed.dogecoin.com:22556",
			"seed.multidoge.org:22556",
			"seed.dogechain.info:22556",
		},
		watchAddresses: make(map[string]bool),
	}
}

// GetBlock gets block data using RPC
func GetBlock(blockHash string) (map[string]interface{}, error) {
	// Make RPC call to get block data
	reqBody := fmt.Sprintf(`{"jsonrpc": "1.0", "id": "curltest", "method": "getblock", "params": ["%s", 2]}`, blockHash)
	resp, err := http.Post("http://qlplock.ddns.net:22555", "application/json", strings.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("error making RPC call: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("error decoding response: %v", err)
	}

	if result["error"] != nil {
		return nil, fmt.Errorf("RPC error: %v", result["error"])
	}

	blockData, ok := result["result"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid block data")
	}

	return blockData, nil
}

// GetRawTransaction gets raw transaction data using RPC
func GetRawTransaction(txID string) (map[string]interface{}, error) {
	// Make RPC call to get transaction data
	reqBody := fmt.Sprintf(`{"jsonrpc": "1.0", "id": "curltest", "method": "getrawtransaction", "params": ["%s", true]}`, txID)
	resp, err := http.Post("http://qlplock.ddns.net:22555", "application/json", strings.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("error making RPC call: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("error decoding response: %v", err)
	}

	if result["error"] != nil {
		return nil, fmt.Errorf("RPC error: %v", result["error"])
	}

	txData, ok := result["result"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid transaction data")
	}

	return txData, nil
}

// GetBlockTransactions gets all transactions in a block using RPC
func GetBlockTransactions(blockHash string) ([]*Transaction, error) {
	// Get block data using RPC
	blockData, err := GetBlock(blockHash)
	if err != nil {
		return nil, fmt.Errorf("error getting block data: %v", err)
	}

	// Get all transaction IDs in the block
	txIDs, ok := blockData["tx"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid transaction data in block")
	}

	// Get transaction data for each transaction
	var transactions []*Transaction
	for _, txID := range txIDs {
		txIDStr, ok := txID.(string)
		if !ok {
			continue
		}

		// Get raw transaction data
		txData, err := GetRawTransaction(txIDStr)
		if err != nil {
			log.Printf("Error getting transaction %s: %v", txIDStr, err)
			continue
		}

		// Create transaction object
		tx := &Transaction{
			TxID:    txIDStr,
			Outputs: make([]TxOutput, 0),
		}

		// Get transaction outputs
		vouts, ok := txData["vout"].([]interface{})
		if !ok {
			continue
		}

		for _, vout := range vouts {
			voutMap, ok := vout.(map[string]interface{})
			if !ok {
				continue
			}

			// Get value
			value, ok := voutMap["value"].(float64)
			if !ok {
				continue
			}

			// Get scriptPubKey
			scriptPubKey, ok := voutMap["scriptPubKey"].(map[string]interface{})
			if !ok {
				continue
			}

			// Get addresses
			addresses, ok := scriptPubKey["addresses"].([]interface{})
			if !ok {
				continue
			}

			// Convert addresses to strings
			var addrStrings []string
			for _, addr := range addresses {
				if addrStr, ok := addr.(string); ok {
					addrStrings = append(addrStrings, addrStr)
				}
			}

			// Add output to transaction
			tx.Outputs = append(tx.Outputs, TxOutput{
				Value:     int64(value * 1e8), // Convert DOGE to satoshis
				Addresses: addrStrings,
			})
		}

		transactions = append(transactions, tx)
	}

	return transactions, nil
}
