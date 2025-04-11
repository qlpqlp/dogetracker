package doge

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// DecodeTransaction decodes a transaction from raw bytes
func DecodeTransaction(data []byte) (*Transaction, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("transaction data too short")
	}

	tx := &Transaction{
		Inputs:  make([]TxInput, 0),
		Outputs: make([]TxOutput, 0),
	}

	// Read version
	tx.Version = uint32(binary.LittleEndian.Uint32(data[0:4]))
	offset := 4

	// Read number of inputs
	count, n := binary.Uvarint(data[offset:])
	if n <= 0 {
		return nil, fmt.Errorf("error reading input count")
	}
	offset += n

	// Read inputs
	for i := uint64(0); i < count; i++ {
		if offset+36 > len(data) {
			return nil, fmt.Errorf("transaction data too short for input")
		}

		input := TxInput{}
		copy(input.PreviousOutput.Hash[:], data[offset:offset+32])
		input.PreviousOutput.Index = binary.LittleEndian.Uint32(data[offset+32 : offset+36])
		offset += 36

		// Read script length
		scriptLen, n := binary.Uvarint(data[offset:])
		if n <= 0 {
			return nil, fmt.Errorf("error reading script length")
		}
		offset += n

		if offset+int(scriptLen) > len(data) {
			return nil, fmt.Errorf("transaction data too short for script")
		}

		input.ScriptSig = make([]byte, scriptLen)
		copy(input.ScriptSig, data[offset:offset+int(scriptLen)])
		offset += int(scriptLen)

		// Read sequence
		if offset+4 > len(data) {
			return nil, fmt.Errorf("transaction data too short for sequence")
		}
		input.Sequence = binary.LittleEndian.Uint32(data[offset : offset+4])
		offset += 4

		tx.Inputs = append(tx.Inputs, input)
	}

	// Read number of outputs
	count, n = binary.Uvarint(data[offset:])
	if n <= 0 {
		return nil, fmt.Errorf("error reading output count")
	}
	offset += n

	// Read outputs
	for i := uint64(0); i < count; i++ {
		if offset+8 > len(data) {
			return nil, fmt.Errorf("transaction data too short for output value")
		}

		output := TxOutput{}
		value := binary.LittleEndian.Uint64(data[offset : offset+8])
		output.Value = value
		offset += 8

		// Read script length
		scriptLen, n := binary.Uvarint(data[offset:])
		if n <= 0 {
			return nil, fmt.Errorf("error reading script length")
		}
		offset += n

		if offset+int(scriptLen) > len(data) {
			return nil, fmt.Errorf("transaction data too short for script")
		}

		output.ScriptPubKey = make([]byte, scriptLen)
		copy(output.ScriptPubKey, data[offset:offset+int(scriptLen)])
		offset += int(scriptLen)

		tx.Outputs = append(tx.Outputs, output)
	}

	// Read lock time
	if offset+4 > len(data) {
		return nil, fmt.Errorf("transaction data too short for lock time")
	}
	tx.LockTime = binary.LittleEndian.Uint32(data[offset : offset+4])

	// Calculate transaction ID
	firstHash := sha256.Sum256(data)
	secondHash := sha256.Sum256(firstHash[:])
	tx.TxID = fmt.Sprintf("%x", secondHash)

	return tx, nil
}

// SerializeSize returns the size of the transaction when serialized
func (tx *Transaction) SerializeSize() int {
	size := 4 // version
	size += binary.PutUvarint(make([]byte, binary.MaxVarintLen64), uint64(len(tx.Inputs)))

	for _, input := range tx.Inputs {
		size += 32 // previous output hash
		size += 4  // previous output index
		size += binary.PutUvarint(make([]byte, binary.MaxVarintLen64), uint64(len(input.ScriptSig)))
		size += len(input.ScriptSig)
		size += 4 // sequence
	}

	size += binary.PutUvarint(make([]byte, binary.MaxVarintLen64), uint64(len(tx.Outputs)))

	for _, output := range tx.Outputs {
		size += 8 // value
		size += binary.PutUvarint(make([]byte, binary.MaxVarintLen64), uint64(len(output.ScriptPubKey)))
		size += len(output.ScriptPubKey)
	}

	size += 4 // lock time

	return size
}

// parseTransaction parses a transaction from raw bytes and returns the transaction and the number of bytes read
func parseTransaction(data []byte) (Transaction, int, error) {
	tx := Transaction{
		Inputs:  make([]TxInput, 0),
		Outputs: make([]TxOutput, 0),
	}

	if len(data) < 4 {
		return tx, 0, fmt.Errorf("transaction data too short")
	}

	// Read version
	tx.Version = uint32(binary.LittleEndian.Uint32(data[0:4]))
	offset := 4

	// Read number of inputs
	count, n := binary.Uvarint(data[offset:])
	if n <= 0 {
		return tx, 0, fmt.Errorf("error reading input count")
	}
	offset += n

	// Read inputs
	for i := uint64(0); i < count; i++ {
		if offset+36 > len(data) {
			return tx, 0, fmt.Errorf("transaction data too short for input")
		}

		input := TxInput{}
		copy(input.PreviousOutput.Hash[:], data[offset:offset+32])
		input.PreviousOutput.Index = binary.LittleEndian.Uint32(data[offset+32 : offset+36])
		offset += 36

		// Read script length
		scriptLen, n := binary.Uvarint(data[offset:])
		if n <= 0 {
			return tx, 0, fmt.Errorf("error reading script length")
		}
		offset += n

		if offset+int(scriptLen) > len(data) {
			return tx, 0, fmt.Errorf("transaction data too short for script")
		}

		input.ScriptSig = make([]byte, scriptLen)
		copy(input.ScriptSig, data[offset:offset+int(scriptLen)])
		offset += int(scriptLen)

		// Read sequence
		if offset+4 > len(data) {
			return tx, 0, fmt.Errorf("transaction data too short for sequence")
		}
		input.Sequence = binary.LittleEndian.Uint32(data[offset : offset+4])
		offset += 4

		tx.Inputs = append(tx.Inputs, input)
	}

	// Read number of outputs
	count, n = binary.Uvarint(data[offset:])
	if n <= 0 {
		return tx, 0, fmt.Errorf("error reading output count")
	}
	offset += n

	// Read outputs
	for i := uint64(0); i < count; i++ {
		if offset+8 > len(data) {
			return tx, 0, fmt.Errorf("transaction data too short for output value")
		}

		output := TxOutput{}
		output.Value = binary.LittleEndian.Uint64(data[offset : offset+8])
		offset += 8

		// Read script length
		scriptLen, n := binary.Uvarint(data[offset:])
		if n <= 0 {
			return tx, 0, fmt.Errorf("error reading script length")
		}
		offset += n

		if offset+int(scriptLen) > len(data) {
			return tx, 0, fmt.Errorf("transaction data too short for script")
		}

		output.ScriptPubKey = make([]byte, scriptLen)
		copy(output.ScriptPubKey, data[offset:offset+int(scriptLen)])
		offset += int(scriptLen)

		tx.Outputs = append(tx.Outputs, output)
	}

	// Read lock time
	if offset+4 > len(data) {
		return tx, 0, fmt.Errorf("transaction data too short for lock time")
	}
	tx.LockTime = binary.LittleEndian.Uint32(data[offset : offset+4])
	offset += 4

	// Calculate transaction ID
	firstHash := sha256.Sum256(data[:offset])
	secondHash := sha256.Sum256(firstHash[:])
	tx.TxID = fmt.Sprintf("%x", secondHash)

	return tx, offset, nil
}

// DecodeTx decodes a transaction from hex string
func DecodeTx(hexStr string) (*Transaction, error) {
	data, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hex: %v", err)
	}

	tx, _, err := parseTransaction(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse transaction: %v", err)
	}

	return &tx, nil
}

// HexEncodeReversed encodes bytes to hex string in reverse order
func HexEncodeReversed(data []byte) string {
	reversed := make([]byte, len(data))
	for i := 0; i < len(data); i++ {
		reversed[i] = data[len(data)-1-i]
	}
	return hex.EncodeToString(reversed)
}

// ScriptType represents the type of a script
type ScriptType int

const (
	ScriptTypeUnknown ScriptType = iota
	ScriptTypeP2PKH
	ScriptTypeP2SH
	ScriptTypeP2PK
)

// ClassifyScript determines the type of a script
func ClassifyScript(script []byte, chain *ChainParams) ScriptType {
	if len(script) < 2 {
		return ScriptTypeUnknown
	}

	// P2PKH: OP_DUP OP_HASH160 <20 bytes> OP_EQUALVERIFY OP_CHECKSIG
	if len(script) == 25 && script[0] == 0x76 && script[1] == 0xa9 && script[2] == 0x14 && script[23] == 0x88 && script[24] == 0xac {
		return ScriptTypeP2PKH
	}

	// P2SH: OP_HASH160 <20 bytes> OP_EQUAL
	if len(script) == 23 && script[0] == 0xa9 && script[1] == 0x14 && script[22] == 0x87 {
		return ScriptTypeP2SH
	}

	// P2PK: <pubkey> OP_CHECKSIG
	if len(script) >= 35 && script[len(script)-1] == 0xac {
		return ScriptTypeP2PK
	}

	return ScriptTypeUnknown
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
