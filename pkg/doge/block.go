package doge

// Block represents a Dogecoin block
type Block struct {
	Header BlockHeader
	Tx     []Transaction
}

// BlockHeader represents a Dogecoin block header
type BlockHeader struct {
	Version    uint32
	PrevBlock  []byte
	MerkleRoot []byte
	Timestamp  uint32
	Bits       uint32
	Nonce      uint32
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
