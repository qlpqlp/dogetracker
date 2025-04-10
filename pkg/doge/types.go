package doge

import "net"

// BlockHeader represents a Dogecoin block header
type BlockHeader struct {
	Version       int32
	PrevBlock     [32]byte
	MerkleRoot    [32]byte
	Time          uint32
	Bits          uint32
	Nonce         uint32
	Height        uint32
	Confirmations int64
}

// BlockchainBlock represents a block in the blockchain
type BlockchainBlock struct {
	Hash   string
	Height int64
	Time   int64
}

// Transaction represents a Dogecoin transaction
type Transaction struct {
	TxID     string
	Version  uint32
	Inputs   []TxInput
	Outputs  []TxOutput
	LockTime uint32
}

// TxInput represents a transaction input
type TxInput struct {
	PreviousOutput OutPoint
	ScriptSig      []byte
	Sequence       uint32
}

// TxOutput represents a transaction output
type TxOutput struct {
	Value        int64
	ScriptPubKey []byte
	Addresses    []string
}

// OutPoint represents a reference to a previous output
type OutPoint struct {
	Hash  [32]byte
	Index uint32
}

// SPVNode represents a Simplified Payment Verification node
type SPVNode struct {
	headers        map[uint32]BlockHeader
	currentHeight  uint32
	peers          []string
	watchAddresses map[string]bool
	bloomFilter    []byte
	conn           net.Conn
}

// Blockchain represents a connection to a Dogecoin node
type Blockchain interface {
	GetBlockHash(height int64) (string, error)
	GetBlock(hash string) ([]byte, error)
	GetBlockCount() (int64, error)
}
