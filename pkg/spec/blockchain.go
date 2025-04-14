package spec

// Blockchain provides access to the Dogecoin Blockchain.
type Blockchain interface {
	GetBlockHeader(blockHash string) (txn BlockHeader, err error)
	GetBlock(blockHash string) (hex string, err error)
	GetBlockHash(blockHeight int64) (hash string, err error)
	GetBestBlockHash() (blockHash string, err error)
	GetBlockCount() (blockCount int64, err error)
	GetRawTransaction(txID string) (hex string, err error)
	DecodeRawTransaction(hex string) (txn Transaction, err error)
}

// BlockHeader from Dogecoin Core
// Includes on-chain status (Confirmations = -1 means block is on a fork / orphan block)
// and current chain linkage (NextBlockHash)
// https://developer.bitcoin.org/reference/rpc/getblockheader.html
type BlockHeader struct {
	Hash              string  `json:"hash"`              // (string) the block hash (same as provided) (hex)
	Confirmations     int64   `json:"confirmations"`     // (numeric) The number of confirmations, or -1 if the block is not on the main chain
	Height            int64   `json:"height"`            // (numeric) The block height or index
	Version           uint32  `json:"version"`           // (numeric) The block version
	MerkleRoot        string  `json:"merkleroot"`        // (string) The merkle root (hex)
	Time              uint64  `json:"time"`              // (numeric) The block time in seconds since UNIX epoch (Jan 1 1970 GMT)
	MedianTime        uint64  `json:"mediantime"`        // (numeric) The median block time in seconds since UNIX epoch (Jan 1 1970 GMT)
	Nonce             uint32  `json:"nonce"`             // (numeric) The nonce
	Bits              string  `json:"bits"`              // (string) The bits (hex)
	Difficulty        float64 `json:"difficulty"`        // (numeric) The difficulty
	ChainWork         string  `json:"chainwork"`         // (string) Expected number of hashes required to produce the chain up to this block (hex)
	PreviousBlockHash string  `json:"previousblockhash"` // (string) The hash of the previous block (hex)
	NextBlockHash     string  `json:"nextblockhash"`     // (string) The hash of the next block (hex)
}

// Transaction represents a decoded raw transaction
type Transaction struct {
	TxID     string  `json:"txid"`
	Version  int32   `json:"version"`
	LockTime uint32  `json:"locktime"`
	Vin      []TxIn  `json:"vin"`
	Vout     []TxOut `json:"vout"`
}

// TxIn represents a transaction input
type TxIn struct {
	TxID      string `json:"txid"`
	Vout      uint32 `json:"vout"`
	ScriptSig Script `json:"scriptSig"`
	Sequence  uint32 `json:"sequence"`
}

// TxOut represents a transaction output
type TxOut struct {
	Value        float64 `json:"value"`
	N            uint32  `json:"n"`
	ScriptPubKey Script  `json:"scriptPubKey"`
}

// Script represents a script in a transaction
type Script struct {
	Asm       string   `json:"asm"`
	Hex       string   `json:"hex"`
	Type      string   `json:"type"`
	Addresses []string `json:"addresses"`
}
