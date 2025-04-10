package doge

import (
	"crypto/sha256"
	"encoding/binary"
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
