package doge

import (
	"encoding/hex"
	"errors"
)

// HexDecode decodes a hex string into bytes
func HexDecode(s string) ([]byte, error) {
	return hex.DecodeString(s)
}

// DecodeBlock decodes a block from raw bytes
func DecodeBlock(data []byte) (*Block, error) {
	if len(data) < 80 {
		return nil, errors.New("block data too short")
	}

	block := &Block{}
	offset := 0

	// Decode header
	block.Header.Version = uint32(data[offset]) | uint32(data[offset+1])<<8 | uint32(data[offset+2])<<16 | uint32(data[offset+3])<<24
	offset += 4

	block.Header.PrevBlock = make([]byte, 32)
	copy(block.Header.PrevBlock, data[offset:offset+32])
	offset += 32

	block.Header.MerkleRoot = make([]byte, 32)
	copy(block.Header.MerkleRoot, data[offset:offset+32])
	offset += 32

	block.Header.Timestamp = uint32(data[offset]) | uint32(data[offset+1])<<8 | uint32(data[offset+2])<<16 | uint32(data[offset+3])<<24
	offset += 4

	block.Header.Bits = uint32(data[offset]) | uint32(data[offset+1])<<8 | uint32(data[offset+2])<<16 | uint32(data[offset+3])<<24
	offset += 4

	block.Header.Nonce = uint32(data[offset]) | uint32(data[offset+1])<<8 | uint32(data[offset+2])<<16 | uint32(data[offset+3])<<24
	offset += 4

	// Check if this is an AuxPow block
	isAuxPow := block.Header.Version >= 0x20000000
	if isAuxPow {
		// For AuxPow blocks, we need to handle the special structure
		// 1. First comes the transaction count (varint)
		txCount, n := DecodeVarInt(data[offset:])
		if n == 0 {
			return nil, errors.New("invalid transaction count")
		}
		offset += n

		if txCount > 0 {
			// 2. The first transaction is the coinbase transaction
			// Skip the AuxPow data to get to the actual transaction
			// The AuxPow data structure is:
			// - Coinbase transaction
			// - AuxPow block header (80 bytes)
			// - AuxPow Merkle branch
			// - AuxPow parent block header (80 bytes)

			// First, find the start of the actual transaction
			// Look for the transaction version (0x01000000)
			for offset < len(data)-4 {
				if data[offset] == 0x01 && data[offset+1] == 0x00 && data[offset+2] == 0x00 && data[offset+3] == 0x00 {
					break
				}
				offset++
			}

			// Now decode the actual transaction
			tx, err := DecodeTransaction(data[offset:])
			if err != nil {
				return nil, err
			}
			block.Tx = append(block.Tx, *tx)
		}
		return block, nil
	}

	// For regular blocks, decode all transactions
	txCount, n := DecodeVarInt(data[offset:])
	if n == 0 {
		return nil, errors.New("invalid transaction count")
	}
	offset += n

	block.Tx = make([]Transaction, txCount)
	for i := uint64(0); i < txCount; i++ {
		tx, err := DecodeTransaction(data[offset:])
		if err != nil {
			return nil, err
		}
		block.Tx[i] = *tx
		// Move offset to next transaction
		offset += tx.SerializeSize()
	}

	return block, nil
}

// DecodeTx is an alias for DecodeTransaction
func DecodeTx(data []byte) (*Transaction, error) {
	return DecodeTransaction(data)
}
