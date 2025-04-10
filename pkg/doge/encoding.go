package doge

import (
	"encoding/hex"
	"errors"
	"log"
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

	// Log block version and type
	isAuxPow := block.Header.Version >= 0x20000000
	log.Printf("Block version: %x (is AuxPow: %v)", block.Header.Version, isAuxPow)

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
	if isAuxPow {
		log.Printf("Processing AuxPow block, skipping AuxPow data")
		// For AuxPow blocks, we need to handle the special structure
		// 1. First comes the transaction count (varint)
		txCount, n := DecodeVarInt(data[offset:])
		if n == 0 {
			return nil, errors.New("invalid transaction count")
		}
		offset += n
		log.Printf("AuxPow block has %d transactions", txCount)

		if txCount > 0 {
			// 2. The first transaction is the coinbase transaction
			// For AuxPow blocks, we only process the coinbase transaction
			// and skip the rest of the AuxPow data
			tx, err := DecodeTransaction(data[offset:])
			if err != nil {
				log.Printf("Error decoding coinbase transaction in AuxPow block: %v", err)
				return nil, err
			}
			block.Tx = append(block.Tx, *tx)
			log.Printf("Successfully decoded coinbase transaction in AuxPow block")

			// Skip the rest of the AuxPow data
			// The AuxPow data structure is:
			// - Coinbase transaction (already processed)
			// - AuxPow block header (80 bytes)
			// - AuxPow Merkle branch
			// - AuxPow parent block header (80 bytes)
			// We don't need to process this data for our purposes
			return block, nil
		}
		return block, nil
	}

	// For regular blocks, decode all transactions
	txCount, n := DecodeVarInt(data[offset:])
	if n == 0 {
		return nil, errors.New("invalid transaction count")
	}
	offset += n
	log.Printf("Regular block has %d transactions", txCount)

	block.Tx = make([]Transaction, txCount)
	for i := uint64(0); i < txCount; i++ {
		tx, err := DecodeTransaction(data[offset:])
		if err != nil {
			log.Printf("Error decoding transaction %d in regular block: %v", i, err)
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
