package doge

import (
	"errors"
	"log"
)

// Transaction represents a Dogecoin transaction
type Transaction struct {
	Version  uint32
	VIn      []TxIn
	VOut     []TxOut
	LockTime uint32
	TxID     string
}

// TxIn represents a transaction input
type TxIn struct {
	TxID     []byte
	VOut     uint32
	Script   []byte
	Sequence uint32
}

// TxOut represents a transaction output
type TxOut struct {
	Value        int64
	Script       []byte
	ScriptPubKey struct {
		Addresses []string
	}
}

// DecodeVarInt decodes a variable length integer from the input bytes
func DecodeVarInt(data []byte) (uint64, int) {
	if len(data) == 0 {
		return 0, 0
	}

	firstByte := data[0]
	switch {
	case firstByte < 0xfd:
		return uint64(firstByte), 1
	case firstByte == 0xfd:
		if len(data) < 3 {
			return 0, 0
		}
		return uint64(data[1]) | uint64(data[2])<<8, 3
	case firstByte == 0xfe:
		if len(data) < 5 {
			return 0, 0
		}
		return uint64(data[1]) | uint64(data[2])<<8 | uint64(data[3])<<16 | uint64(data[4])<<24, 5
	default:
		if len(data) < 9 {
			return 0, 0
		}
		return uint64(data[1]) | uint64(data[2])<<8 | uint64(data[3])<<16 | uint64(data[4])<<24 |
			uint64(data[5])<<32 | uint64(data[6])<<40 | uint64(data[7])<<48 | uint64(data[8])<<56, 9
	}
}

// DecodeTransaction decodes a transaction from the input bytes
func DecodeTransaction(data []byte) (*Transaction, error) {
	if len(data) < 4 {
		return nil, ErrInvalidTransaction
	}

	// Log transaction data details
	log.Printf("Transaction data length: %d bytes", len(data))
	if len(data) > 0 {
		log.Printf("First 32 bytes of transaction data: %x", data[:32])
	}

	tx := &Transaction{}
	offset := 0

	// Version
	tx.Version = uint32(data[offset]) | uint32(data[offset+1])<<8 | uint32(data[offset+2])<<16 | uint32(data[offset+3])<<24
	offset += 4

	// Input count
	inputCount, n := DecodeVarInt(data[offset:])
	if n == 0 {
		return nil, ErrInvalidTransaction
	}
	offset += n

	// Inputs
	tx.VIn = make([]TxIn, inputCount)
	for i := uint64(0); i < inputCount; i++ {
		if len(data) < offset+36 {
			return nil, ErrInvalidTransaction
		}

		// Previous output hash
		tx.VIn[i].TxID = make([]byte, 32)
		copy(tx.VIn[i].TxID, data[offset:offset+32])
		offset += 32

		// Previous output index
		tx.VIn[i].VOut = uint32(data[offset]) | uint32(data[offset+1])<<8 | uint32(data[offset+2])<<16 | uint32(data[offset+3])<<24
		offset += 4

		// Script length
		scriptLen, n := DecodeVarInt(data[offset:])
		if n == 0 {
			return nil, ErrInvalidTransaction
		}
		offset += n

		// Script
		if len(data) < offset+int(scriptLen) {
			return nil, ErrInvalidTransaction
		}
		tx.VIn[i].Script = make([]byte, scriptLen)
		copy(tx.VIn[i].Script, data[offset:offset+int(scriptLen)])
		offset += int(scriptLen)

		// Sequence
		if len(data) < offset+4 {
			return nil, ErrInvalidTransaction
		}
		tx.VIn[i].Sequence = uint32(data[offset]) | uint32(data[offset+1])<<8 | uint32(data[offset+2])<<16 | uint32(data[offset+3])<<24
		offset += 4
	}

	// Output count
	outputCount, n := DecodeVarInt(data[offset:])
	if n == 0 {
		return nil, ErrInvalidTransaction
	}
	offset += n

	// Outputs
	tx.VOut = make([]TxOut, outputCount)
	for i := uint64(0); i < outputCount; i++ {
		if len(data) < offset+8 {
			return nil, ErrInvalidTransaction
		}

		// Value
		tx.VOut[i].Value = int64(data[offset]) | int64(data[offset+1])<<8 | int64(data[offset+2])<<16 | int64(data[offset+3])<<24 |
			int64(data[offset+4])<<32 | int64(data[offset+5])<<40 | int64(data[offset+6])<<48 | int64(data[offset+7])<<56
		offset += 8

		// Script length
		scriptLen, n := DecodeVarInt(data[offset:])
		if n == 0 {
			return nil, ErrInvalidTransaction
		}
		offset += n

		// Script
		if len(data) < offset+int(scriptLen) {
			return nil, ErrInvalidTransaction
		}
		tx.VOut[i].Script = make([]byte, scriptLen)
		copy(tx.VOut[i].Script, data[offset:offset+int(scriptLen)])
		offset += int(scriptLen)
	}

	// Lock time
	if len(data) < offset+4 {
		return nil, ErrInvalidTransaction
	}
	tx.LockTime = uint32(data[offset]) | uint32(data[offset+1])<<8 | uint32(data[offset+2])<<16 | uint32(data[offset+3])<<24

	return tx, nil
}

// SerializeSize returns the number of bytes it would take to serialize the transaction
func (tx *Transaction) SerializeSize() int {
	size := 4 // Version

	// Input count
	size += VarIntSerializeSize(uint64(len(tx.VIn)))

	// Inputs
	for _, input := range tx.VIn {
		size += 32 // Previous output hash
		size += 4  // Previous output index
		size += VarIntSerializeSize(uint64(len(input.Script)))
		size += len(input.Script)
		size += 4 // Sequence
	}

	// Output count
	size += VarIntSerializeSize(uint64(len(tx.VOut)))

	// Outputs
	for _, output := range tx.VOut {
		size += 8 // Value
		size += VarIntSerializeSize(uint64(len(output.Script)))
		size += len(output.Script)
	}

	size += 4 // Lock time

	return size
}

// VarIntSerializeSize returns the number of bytes it would take to serialize a variable length integer
func VarIntSerializeSize(val uint64) int {
	if val < 0xfd {
		return 1
	}
	if val <= 0xffff {
		return 3
	}
	if val <= 0xffffffff {
		return 5
	}
	return 9
}

// Errors
var (
	ErrInvalidTransaction = errors.New("invalid transaction data")
)
