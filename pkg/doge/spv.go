package doge

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"time"
)

// NewSPVNode creates a new SPV node
func NewSPVNode() *SPVNode {
	return &SPVNode{
		headers: make(map[uint32]BlockHeader),
		peers: []string{
			"seed.dogecoin.net:22556",  // Mainnet seed node
			"seed.dogecoin.com:22556",  // Mainnet seed node
			"seed.multidoge.org:22556", // Mainnet seed node
		},
		watchAddresses: make(map[string]bool),
	}
}

// AddWatchAddress adds an address to watch for transactions
func (n *SPVNode) AddWatchAddress(address string) {
	n.watchAddresses[address] = true
	n.updateBloomFilter()
}

// updateBloomFilter updates the bloom filter based on watched addresses
func (n *SPVNode) updateBloomFilter() {
	// Create a bloom filter that includes all watched addresses
	var filter []byte
	for addr := range n.watchAddresses {
		// Convert address to script hash
		scriptHash := n.addressToScriptHash(addr)
		filter = append(filter, scriptHash...)
	}
	n.bloomFilter = filter
}

// addressToScriptHash converts a Dogecoin address to its script hash
func (n *SPVNode) addressToScriptHash(address string) []byte {
	hash := sha256.Sum256([]byte(address))
	return hash[:]
}

// ProcessTransaction processes a transaction and checks if it's relevant
func (n *SPVNode) ProcessTransaction(tx *Transaction) []string {
	var relevantAddresses []string
	for _, output := range tx.Outputs {
		scriptHash := sha256.Sum256(output.ScriptPubKey)
		if n.isRelevant(scriptHash[:]) {
			// Extract addresses from the script
			addresses := n.extractAddresses(output.ScriptPubKey)
			relevantAddresses = append(relevantAddresses, addresses...)
		}
	}
	return relevantAddresses
}

// isRelevant checks if a script hash matches our bloom filter
func (n *SPVNode) isRelevant(scriptHash []byte) bool {
	for _, filterHash := range n.bloomFilter {
		if bytes.Contains(scriptHash, []byte{filterHash}) {
			return true
		}
	}
	return false
}

// extractAddresses extracts addresses from a script
func (n *SPVNode) extractAddresses(script []byte) []string {
	// This is a simplified version - in reality you'd properly decode the script
	// For now, we'll just return the addresses we're watching
	var addresses []string
	for addr := range n.watchAddresses {
		addresses = append(addresses, addr)
	}
	return addresses
}

// ConnectToPeer establishes a connection to a Dogecoin peer
func (n *SPVNode) ConnectToPeer(peer string) error {
	conn, err := net.Dial("tcp", peer)
	if err != nil {
		return fmt.Errorf("failed to connect to peer: %v", err)
	}
	n.conn = conn

	// Set timeouts
	conn.SetDeadline(time.Now().Add(15 * time.Second))

	// Send version message
	versionMsg := n.createVersionMessage()
	if err := binary.Write(conn, binary.LittleEndian, versionMsg); err != nil {
		return fmt.Errorf("failed to send version message: %v", err)
	}

	// Read verack
	var verack [24]byte
	if err := binary.Read(conn, binary.LittleEndian, &verack); err != nil {
		return fmt.Errorf("failed to read verack: %v", err)
	}

	// Send filterload message with our bloom filter
	filterloadMsg := n.createFilterLoadMessage()
	if err := binary.Write(conn, binary.LittleEndian, filterloadMsg); err != nil {
		return fmt.Errorf("failed to send filterload message: %v", err)
	}

	return nil
}

// createVersionMessage creates a version message to send to peers
func (n *SPVNode) createVersionMessage() []byte {
	// Create version message
	msg := make([]byte, 0)

	// Version (4 bytes)
	version := uint32(70015) // Dogecoin protocol version
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, version)
	msg = append(msg, buf...)

	// Services (8 bytes)
	services := uint64(0x01) // NODE_NETWORK
	buf = make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, services)
	msg = append(msg, buf...)

	// Timestamp (8 bytes)
	timestamp := uint64(time.Now().Unix())
	buf = make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, timestamp)
	msg = append(msg, buf...)

	// AddrRecv (26 bytes)
	addrRecv := make([]byte, 26)
	msg = append(msg, addrRecv...)

	// AddrFrom (26 bytes)
	addrFrom := make([]byte, 26)
	msg = append(msg, addrFrom...)

	// Nonce (8 bytes)
	nonce := uint64(time.Now().UnixNano())
	buf = make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, nonce)
	msg = append(msg, buf...)

	// UserAgent (varint + string)
	userAgent := "/dogetracker:0.1.0/"
	msg = append(msg, byte(len(userAgent)))
	msg = append(msg, []byte(userAgent)...)

	// StartHeight (4 bytes)
	startHeight := uint32(0)
	buf = make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, startHeight)
	msg = append(msg, buf...)

	// Relay (1 byte)
	relay := byte(1)
	msg = append(msg, relay)

	return msg
}

// createFilterLoadMessage creates a filterload message to send to peers
func (n *SPVNode) createFilterLoadMessage() []byte {
	// Create filterload message
	msg := make([]byte, 0)

	// Filter length (varint)
	msg = append(msg, byte(len(n.bloomFilter)))

	// Filter data
	msg = append(msg, n.bloomFilter...)

	// Hash functions (4 bytes)
	hashFuncs := uint32(11) // Number of hash functions
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, hashFuncs)
	msg = append(msg, buf...)

	// Tweak (4 bytes)
	tweak := uint32(0)
	buf = make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, tweak)
	msg = append(msg, buf...)

	// Flags (1 byte)
	flags := byte(1) // BLOOM_UPDATE_ALL
	msg = append(msg, flags)

	return msg
}

// GetBlockTransactions gets all transactions in a block using SPV
func (n *SPVNode) GetBlockTransactions(blockHash string) ([]*Transaction, error) {
	if n.conn == nil {
		return nil, fmt.Errorf("not connected to peer")
	}

	// Create getdata message for block
	msg := make([]byte, 0)

	// Command (12 bytes)
	command := "getdata"
	msg = append(msg, []byte(command)...)
	msg = append(msg, make([]byte, 12-len(command))...)

	// Inventory count (varint)
	msg = append(msg, byte(1))

	// Inventory type (4 bytes)
	invType := uint32(2) // MSG_BLOCK
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, invType)
	msg = append(msg, buf...)

	// Block hash (32 bytes)
	hashBytes, err := hex.DecodeString(blockHash)
	if err != nil {
		return nil, fmt.Errorf("invalid block hash: %v", err)
	}
	msg = append(msg, hashBytes...)

	// Send message
	if err := binary.Write(n.conn, binary.LittleEndian, msg); err != nil {
		return nil, fmt.Errorf("failed to send getdata message: %v", err)
	}

	// Read block message
	// Note: In a real implementation, you would need to:
	// 1. Read the message header
	// 2. Read the block data
	// 3. Parse the block
	// 4. Extract transactions
	// 5. Filter relevant transactions

	// For now, return an empty slice
	return []*Transaction{}, nil
}

// GetBlockHeader gets a block header from the network
func (n *SPVNode) GetBlockHeader(blockHash string) (*BlockHeader, error) {
	if n.conn == nil {
		return nil, fmt.Errorf("not connected to peer")
	}

	// Create getheaders message
	msg := make([]byte, 0)

	// Command (12 bytes)
	command := "getheaders"
	msg = append(msg, []byte(command)...)
	msg = append(msg, make([]byte, 12-len(command))...)

	// Version (4 bytes)
	version := uint32(70015)
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, version)
	msg = append(msg, buf...)

	// Hash count (varint)
	msg = append(msg, byte(1))

	// Block hash (32 bytes)
	hashBytes, err := hex.DecodeString(blockHash)
	if err != nil {
		return nil, fmt.Errorf("invalid block hash: %v", err)
	}
	msg = append(msg, hashBytes...)

	// Stop hash (32 bytes)
	stopHash := make([]byte, 32)
	msg = append(msg, stopHash...)

	// Send message
	if err := binary.Write(n.conn, binary.LittleEndian, msg); err != nil {
		return nil, fmt.Errorf("failed to send getheaders message: %v", err)
	}

	// Read headers message
	// Note: In a real implementation, you would need to:
	// 1. Read the message header
	// 2. Read the headers data
	// 3. Parse the headers
	// 4. Return the requested header

	// For now, return a dummy header
	return &BlockHeader{
		Version: 70015,
		Time:    uint32(time.Now().Unix()),
		Bits:    0x1e0ffff0,
		Nonce:   0,
		Height:  0,
	}, nil
}
