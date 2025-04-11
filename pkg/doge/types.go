package doge

import (
	"crypto/sha256"
	"encoding/binary"
)

// Block represents a Dogecoin block
type Block struct {
	Header       BlockHeader
	Transactions []*Transaction
}

// BlockHeader represents a Dogecoin block header
type BlockHeader struct {
	Version       uint32
	PrevBlock     [32]byte
	MerkleRoot    [32]byte
	Time          uint32
	Bits          uint32
	Nonce         uint32
	Height        uint32
	Confirmations int64
}

// Hash calculates the double SHA-256 hash of the block header
func (h *BlockHeader) Hash() []byte {
	headerBytes := h.Serialize()
	hash1 := sha256.Sum256(headerBytes)
	hash2 := sha256.Sum256(hash1[:])
	return hash2[:]
}

// Serialize serializes a block header into bytes
func (h *BlockHeader) Serialize() []byte {
	buf := make([]byte, 80)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(h.Version))
	copy(buf[4:36], h.PrevBlock[:])
	copy(buf[36:68], h.MerkleRoot[:])
	binary.LittleEndian.PutUint32(buf[68:72], h.Time)
	binary.LittleEndian.PutUint32(buf[72:76], h.Bits)
	binary.LittleEndian.PutUint32(buf[76:80], h.Nonce)
	return buf
}

// BlockchainBlock represents a block in the blockchain
type BlockchainBlock struct {
	Hash   string
	Height int64
	Time   int64
}

// Transaction represents a Dogecoin transaction
type Transaction struct {
	Version  uint32
	TxID     string
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
	Value        uint64
	ScriptPubKey []byte
}

// OutPoint represents a reference to a previous transaction output
type OutPoint struct {
	Hash  [32]byte
	Index uint32
}

// Blockchain represents a connection to a Dogecoin node
type Blockchain interface {
	GetBlockHash(height int64) (string, error)
	GetBlock(hash string) ([]byte, error)
	GetBlockCount() (int64, error)
}

// ChainParams represents the parameters for a Dogecoin chain
type ChainParams struct {
	ChainName    string
	GenesisBlock string
	DefaultPort  int
	RPCPort      int
	DNSSeeds     []string
	Checkpoints  map[int]string
}

// MainNetParams returns the parameters for the main Dogecoin network
var MainNetParams = ChainParams{
	ChainName:    "main",
	GenesisBlock: "1a91e3dace36e2be3bf030a65679fe821aa1d6ef92e7c9902eb318182c355691",
	DefaultPort:  22556,
	RPCPort:      22555,
	DNSSeeds: []string{
		"seed.dogecoin.com",
		"seed.multidoge.org",
		"seed.doger.dogecoin.com",
	},
	Checkpoints: map[int]string{
		0: "1a91e3dace36e2be3bf030a65679fe821aa1d6ef92e7c9902eb318182c355691",
	},
}

// TestNetParams returns the parameters for the Dogecoin test network
var TestNetParams = ChainParams{
	ChainName:    "test",
	GenesisBlock: "bb0a78264637406b6360aad926284d544d7049f45189db5664f3c4d07350559e",
	DefaultPort:  44556,
	RPCPort:      44555,
	DNSSeeds: []string{
		"testseed.jrn.me.uk",
	},
	Checkpoints: map[int]string{
		0: "bb0a78264637406b6360aad926284d544d7049f45189db5664f3c4d07350559e",
	},
}

// RegTestParams returns the parameters for the Dogecoin regression test network
var RegTestParams = ChainParams{
	ChainName:    "regtest",
	GenesisBlock: "fdc8bafc0b0c6c2b67c7fcd30a49f3b30a80f8ceb48c83c2c3e8e99cd8de5ca9",
	DefaultPort:  18444,
	RPCPort:      18443,
	DNSSeeds:     []string{},
	Checkpoints:  map[int]string{},
}
