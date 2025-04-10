package doge

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// DecodeBlock decodes a block from raw bytes
func DecodeBlock(data []byte) (*Block, error) {
	if len(data) < 80 {
		return nil, fmt.Errorf("block data too short")
	}

	block := &Block{}
	offset := 0

	// Decode version
	versionBytes := data[offset : offset+4]
	block.Header.Version = int32(binary.LittleEndian.Uint32(versionBytes))
	offset += 4

	// Decode previous block hash
	var prevBlock [32]byte
	copy(prevBlock[:], data[offset:offset+32])
	block.Header.PrevBlock = prevBlock
	offset += 32

	// Decode merkle root
	var merkleRoot [32]byte
	copy(merkleRoot[:], data[offset:offset+32])
	block.Header.MerkleRoot = merkleRoot
	offset += 32

	// Decode timestamp
	block.Header.Time = binary.LittleEndian.Uint32(data[offset : offset+4])
	offset += 4

	// Decode bits
	block.Header.Bits = binary.LittleEndian.Uint32(data[offset : offset+4])
	offset += 4

	// Decode nonce
	block.Header.Nonce = binary.LittleEndian.Uint32(data[offset : offset+4])
	offset += 4

	// Decode transaction count
	txCount, bytesRead := DecodeVarInt(data[offset:])
	offset += bytesRead

	// Decode transactions
	block.Tx = make([]Transaction, txCount)
	for i := uint64(0); i < txCount; i++ {
		tx, err := DecodeTransaction(data[offset:])
		if err != nil {
			return nil, fmt.Errorf("error decoding transaction %d: %v", i, err)
		}
		block.Tx[i] = *tx
		offset += tx.SerializeSize()
	}

	return block, nil
}

// DecodeVarInt decodes a variable-length integer from the input bytes
// Returns the decoded value and the number of bytes read
func DecodeVarInt(data []byte) (uint64, int) {
	if len(data) == 0 {
		return 0, 0
	}

	// Read the first byte to determine the format
	firstByte := data[0]
	if firstByte < 0xfd {
		// Single byte
		return uint64(firstByte), 1
	} else if firstByte == 0xfd {
		// 2 bytes
		if len(data) < 3 {
			return 0, 0
		}
		return uint64(data[1]) | uint64(data[2])<<8, 3
	} else if firstByte == 0xfe {
		// 4 bytes
		if len(data) < 5 {
			return 0, 0
		}
		return uint64(data[1]) | uint64(data[2])<<8 | uint64(data[3])<<16 | uint64(data[4])<<24, 5
	} else {
		// 8 bytes
		if len(data) < 9 {
			return 0, 0
		}
		return uint64(data[1]) | uint64(data[2])<<8 | uint64(data[3])<<16 | uint64(data[4])<<24 |
			uint64(data[5])<<32 | uint64(data[6])<<40 | uint64(data[7])<<48 | uint64(data[8])<<56, 9
	}
}

// HexDecode decodes a hex string to bytes
func HexDecode(hexStr string) ([]byte, error) {
	return hex.DecodeString(hexStr)
}
