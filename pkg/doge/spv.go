package doge

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"sync"
	"time"
)

const (
	// Protocol version
	ProtocolVersion = 70015

	// MaxMessageSize is the maximum allowed size for a message
	MaxMessageSize = 32 * 1024 * 1024 // 32MB

	// MaxHeadersResults is the maximum number of headers to request in one getheaders message
	MaxHeadersResults = 1000
)

// SPVNode represents a Simplified Payment Verification node
type SPVNode struct {
	headers            map[uint32]BlockHeader
	blocks             map[string]*Block
	peers              []string
	watchAddresses     map[string]bool
	bloomFilter        []byte
	currentHeight      uint32
	verackReceived     chan struct{}
	db                 BlockDatabase
	logger             *log.Logger
	connected          bool
	lastMessage        time.Time
	messageTimeout     time.Duration
	chainParams        *ChainParams
	conn               net.Conn
	stopChan           chan struct{}
	reconnectDelay     time.Duration
	bestKnownHeight    uint32
	chainTip           *BlockHeader
	headerSyncComplete bool
	blockSyncComplete  bool
	startHeight        uint32
	headersMutex       sync.RWMutex
}

// NewSPVNode creates a new SPV node
func NewSPVNode(peers []string, startHeight uint32, db BlockDatabase) *SPVNode {
	// Get last processed block from database
	lastBlockHeight, err := db.GetLastProcessedBlock()
	if err != nil {
		log.Printf("Error getting last processed block: %v", err)
		// If no last block found, start from genesis
		lastBlockHeight = 0
	}

	// If startHeight is provided and is higher than last processed block, use it
	if startHeight > uint32(lastBlockHeight) {
		lastBlockHeight = int64(startHeight)
	}

	// Initialize chain params with Dogecoin mainnet parameters
	chainParams := &ChainParams{
		ChainName:    "mainnet",
		GenesisBlock: "1a91e3dace36e2be3bf030a65679fe821aa1d6ef92e7c9902eb318182c355691",
		DefaultPort:  22556,
		RPCPort:      22555,
		DNSSeeds:     []string{"seed.dogecoin.com", "seed.multidoge.org", "seed.dogechain.info"},
		Checkpoints:  make(map[int]string),
	}

	return &SPVNode{
		peers:              peers,
		headers:            make(map[uint32]BlockHeader),
		blocks:             make(map[string]*Block),
		currentHeight:      uint32(lastBlockHeight),
		startHeight:        uint32(lastBlockHeight),
		bestKnownHeight:    0,
		db:                 db,
		verackReceived:     make(chan struct{}),
		headerSyncComplete: false,
		blockSyncComplete:  false,
		logger:             log.New(log.Writer(), "SPV: ", log.LstdFlags),
		chainParams:        chainParams,
		stopChan:           make(chan struct{}),
		reconnectDelay:     5 * time.Second,
	}
}

// ConnectToPeer connects to a peer
func (n *SPVNode) ConnectToPeer(peer string) error {
	n.logger.Printf("Connecting to peer %s...", peer)

	// Close existing connection if any
	if n.conn != nil {
		n.conn.Close()
		n.conn = nil
		n.connected = false
	}

	// Connect to peer
	conn, err := net.Dial("tcp", peer)
	if err != nil {
		return fmt.Errorf("failed to connect to peer: %v", err)
	}
	n.conn = conn
	n.connected = true
	n.logger.Printf("Connected to peer %s", peer)

	// Send version message
	n.logger.Printf("Sending version message...")
	if err := n.sendVersionMessage(); err != nil {
		n.conn.Close()
		n.conn = nil
		n.connected = false
		return fmt.Errorf("failed to send version message: %v", err)
	}

	// Wait for version message from peer
	n.logger.Printf("Waiting for version message from peer...")
	msg, err := n.readMessage()
	if err != nil {
		n.conn.Close()
		n.conn = nil
		n.connected = false
		return fmt.Errorf("failed to read version message: %v", err)
	}

	command := string(bytes.TrimRight(msg.Command[:], "\x00"))
	if command != MsgVersion {
		n.conn.Close()
		n.conn = nil
		n.connected = false
		return fmt.Errorf("expected version message, got %s", command)
	}

	// Handle version message
	if err := n.handleVersionMessage(msg.Payload); err != nil {
		n.conn.Close()
		n.conn = nil
		n.connected = false
		return fmt.Errorf("failed to handle version message: %v", err)
	}

	// Send verack
	verackMsg := &Message{
		Magic:    0xc0c0c0c0,
		Length:   0,
		Checksum: [4]byte{},
	}
	copy(verackMsg.Command[:], MsgVerack)

	n.logger.Printf("Sending verack...")
	if err := n.sendMessage(verackMsg); err != nil {
		n.conn.Close()
		n.conn = nil
		n.connected = false
		return fmt.Errorf("failed to send verack: %v", err)
	}

	// Wait for verack
	n.logger.Printf("Waiting for verack...")
	msg, err = n.readMessage()
	if err != nil {
		n.conn.Close()
		n.conn = nil
		n.connected = false
		return fmt.Errorf("failed to read verack: %v", err)
	}

	command = string(bytes.TrimRight(msg.Command[:], "\x00"))
	if command != MsgVerack {
		n.conn.Close()
		n.conn = nil
		n.connected = false
		return fmt.Errorf("expected verack message, got %s", command)
	}

	// Send filter load message
	n.logger.Printf("Sending filter load message...")
	if err := n.sendFilterLoadMessage(); err != nil {
		n.conn.Close()
		n.conn = nil
		n.connected = false
		return fmt.Errorf("failed to send filter load message: %v", err)
	}

	// Send getheaders message
	n.logger.Printf("Sending getheaders message...")
	if err := n.sendGetHeaders(n.chainParams.GenesisBlock); err != nil {
		n.conn.Close()
		n.conn = nil
		n.connected = false
		return fmt.Errorf("failed to send getheaders message: %v", err)
	}

	// Start message handling goroutine
	n.logger.Printf("Starting message handling goroutine...")
	go n.handleMessages()

	return nil
}

// handleMessages handles incoming messages from the peer
func (n *SPVNode) handleMessages() {
	for {
		msg, err := n.readMessage()
		if err != nil {
			if err == io.EOF {
				n.logger.Printf("Connection closed by peer")
			} else {
				n.logger.Printf("Error reading message: %v", err)
			}
			return
		}

		command := string(bytes.TrimRight(msg.Command[:], "\x00"))
		n.logger.Printf("Received message of type: %s", command)

		switch command {
		case MsgVersion:
			if err := n.handleVersionMessage(msg.Payload); err != nil {
				n.logger.Printf("Error handling version message: %v", err)
			}
		case MsgVerack:
			n.verackReceived <- struct{}{}
		case MsgHeaders:
			if err := n.handleHeadersMessage(msg.Payload); err != nil {
				n.logger.Printf("Error handling headers message: %v", err)
			}
		case MsgBlock:
			if err := n.handleBlockMessage(msg.Payload); err != nil {
				n.logger.Printf("Error handling block message: %v", err)
			}
		case MsgTx:
			if err := n.handleTxMessage(msg.Payload); err != nil {
				n.logger.Printf("Error handling transaction message: %v", err)
			}
		case MsgInv:
			if err := n.handleInvMessage(msg.Payload); err != nil {
				n.logger.Printf("Error handling inventory message: %v", err)
			}
		case MsgPing:
			if err := n.handlePingMessage(msg.Payload); err != nil {
				n.logger.Printf("Error handling ping message: %v", err)
			}
		case "sendheaders":
			// Acknowledge sendheaders message but don't send verack
			n.logger.Printf("Received sendheaders message")
		case "sendcmpct":
			// Acknowledge sendcmpct message but don't send verack
			n.logger.Printf("Received sendcmpct message")
		case "getheaders":
			// Handle getheaders message from peer
			n.logger.Printf("Received getheaders message, sending headers")
			if err := n.handleGetHeadersMessage(msg.Payload); err != nil {
				n.logger.Printf("Error handling getheaders message: %v", err)
			}
		case "feefilter":
			// Acknowledge feefilter message but don't send verack
			n.logger.Printf("Received feefilter message")
		default:
			n.logger.Printf("Ignoring unknown message type: %s", command)
		}
	}
}

// handleGetHeadersMessage handles a getheaders message from the peer
func (n *SPVNode) handleGetHeadersMessage(payload []byte) error {
	reader := bytes.NewReader(payload)

	// Read version (4 bytes)
	var version uint32
	if err := binary.Read(reader, binary.LittleEndian, &version); err != nil {
		return fmt.Errorf("error reading version: %v", err)
	}

	// Read hash count (varint)
	hashCount, err := binary.ReadUvarint(reader)
	if err != nil {
		return fmt.Errorf("error reading hash count: %v", err)
	}

	// Read block locator hashes
	locatorHashes := make([][32]byte, hashCount)
	for i := uint64(0); i < hashCount; i++ {
		if _, err := reader.Read(locatorHashes[i][:]); err != nil {
			return fmt.Errorf("error reading locator hash: %v", err)
		}
	}

	// Read stop hash (32 bytes)
	var stopHash [32]byte
	if _, err := reader.Read(stopHash[:]); err != nil {
		return fmt.Errorf("error reading stop hash: %v", err)
	}

	// Find headers to send
	var headers []BlockHeader
	for _, hash := range locatorHashes {
		for _, header := range n.headers {
			if bytes.Equal(header.PrevBlock[:], hash[:]) {
				headers = append(headers, header)
			}
		}
	}

	// Send headers message
	return n.sendHeadersMessage(headers)
}

// sendHeadersMessage sends a headers message
func (n *SPVNode) sendHeadersMessage(headers []BlockHeader) error {
	// Create headers message payload
	payload := make([]byte, 0)

	// Headers count (varint)
	countBytes := make([]byte, binary.MaxVarintLen64)
	bytesWritten := binary.PutUvarint(countBytes, uint64(len(headers)))
	payload = append(payload, countBytes[:bytesWritten]...)

	// Each header
	for _, header := range headers {
		// Version (4 bytes)
		versionBytes := make([]byte, 4)
		binary.LittleEndian.PutUint32(versionBytes, header.Version)
		payload = append(payload, versionBytes...)

		// Previous block hash (32 bytes)
		payload = append(payload, header.PrevBlock[:]...)

		// Merkle root (32 bytes)
		payload = append(payload, header.MerkleRoot[:]...)

		// Time (4 bytes)
		timeBytes := make([]byte, 4)
		binary.LittleEndian.PutUint32(timeBytes, header.Time)
		payload = append(payload, timeBytes...)

		// Bits (4 bytes)
		bitsBytes := make([]byte, 4)
		binary.LittleEndian.PutUint32(bitsBytes, header.Bits)
		payload = append(payload, bitsBytes...)

		// Nonce (4 bytes)
		nonceBytes := make([]byte, 4)
		binary.LittleEndian.PutUint32(nonceBytes, header.Nonce)
		payload = append(payload, nonceBytes...)

		// Transaction count (varint) - should be 0 for headers message
		payload = append(payload, 0x00)
	}

	// Create message
	checksum := doubleSha256(payload)[:4]
	var checksumArray [4]byte
	copy(checksumArray[:], checksum)

	msg := &Message{
		Magic:    0xc0c0c0c0,
		Length:   uint32(len(payload)),
		Checksum: checksumArray,
		Payload:  payload,
	}
	copy(msg.Command[:], MsgHeaders)

	return n.sendMessage(msg)
}

// sendFilterLoadMessage sends a filter load message
func (n *SPVNode) sendFilterLoadMessage() error {
	// Create bloom filter
	n.updateBloomFilter()

	// Create filter load message payload
	payload := make([]byte, 0)

	// Filter size (varint)
	payload = append(payload, byte(len(n.bloomFilter)))

	// Filter data
	payload = append(payload, n.bloomFilter...)

	// Number of hash functions (4 bytes)
	hashFuncs := make([]byte, 4)
	binary.LittleEndian.PutUint32(hashFuncs, 11)
	payload = append(payload, hashFuncs...)

	// Tweak (4 bytes)
	tweak := make([]byte, 4)
	binary.LittleEndian.PutUint32(tweak, uint32(time.Now().UnixNano()))
	payload = append(payload, tweak...)

	// Flags (1 byte)
	payload = append(payload, 0x01) // BLOOM_UPDATE_ALL

	// Create message
	checksum := doubleSha256(payload)[:4]
	var checksumArray [4]byte
	copy(checksumArray[:], checksum)

	msg := &Message{
		Magic:    0xc0c0c0c0,
		Length:   uint32(len(payload)),
		Checksum: checksumArray,
		Payload:  payload,
	}
	copy(msg.Command[:], MsgFilterLoad)

	return n.sendMessage(msg)
}

// sendGetHeaders sends a getheaders message to request headers
func (n *SPVNode) sendGetHeaders(genesisBlock string) error {
	// Create block locator hashes
	var locatorHashes [][]byte

	// If we have no headers yet, start from the requested height
	if len(n.headers) == 0 {
		// Use genesis block hash from chain params
		genesisHash, err := hex.DecodeString(genesisBlock)
		if err != nil {
			return fmt.Errorf("failed to decode genesis block hash: %v", err)
		}
		// Reverse the hash (Dogecoin uses little-endian)
		for i, j := 0, len(genesisHash)-1; i < j; i, j = i+1, j-1 {
			genesisHash[i], genesisHash[j] = genesisHash[j], genesisHash[i]
		}
		locatorHashes = append(locatorHashes, genesisHash)
	} else {
		// Start from current height
		for height := n.currentHeight; height > 0 && len(locatorHashes) < 10; height-- {
			if header, exists := n.headers[height]; exists {
				hash := header.Hash()
				locatorHashes = append(locatorHashes, hash[:])
			}
		}

		// Always include genesis block hash
		genesisHeader := n.headers[0]
		genesisHash := genesisHeader.Hash()
		locatorHashes = append(locatorHashes, genesisHash[:])
	}

	// Create payload
	buf := new(bytes.Buffer)

	// Protocol version (4 bytes)
	binary.Write(buf, binary.LittleEndian, int32(70015))

	// Hash count (varint)
	hashCount := uint64(len(locatorHashes))
	hashCountBytes := make([]byte, binary.MaxVarintLen64)
	bytesWritten := binary.PutUvarint(hashCountBytes, hashCount)
	buf.Write(hashCountBytes[:bytesWritten])

	// Block locator hashes
	for _, hash := range locatorHashes {
		buf.Write(hash)
	}

	// Stop hash (32 bytes of zeros to get all headers)
	stopHash := make([]byte, 32)
	buf.Write(stopHash)

	payload := buf.Bytes()

	// Create message
	checksum := doubleSha256(payload)[:4]
	var checksumArray [4]byte
	copy(checksumArray[:], checksum)

	msg := &Message{
		Magic:    0xc0c0c0c0,
		Length:   uint32(len(payload)),
		Checksum: checksumArray,
		Payload:  payload,
	}
	copy(msg.Command[:], MsgGetHeaders)

	n.logger.Printf("Sending getheaders message with %d locator hashes, starting from height %d", len(locatorHashes), n.startHeight)
	return n.sendMessage(msg)
}

// handleHeadersMessage handles a headers message
func (n *SPVNode) handleHeadersMessage(payload []byte) error {
	reader := bytes.NewReader(payload)

	// Read headers count (varint)
	count, err := binary.ReadUvarint(reader)
	if err != nil {
		return fmt.Errorf("error reading headers count: %v", err)
	}

	n.logger.Printf("Received %d headers", count)

	// Read each header
	headersProcessed := 0
	for i := uint64(0); i < count; i++ {
		// Check if we have enough bytes left
		if reader.Len() < 80 { // Minimum size for a header
			n.logger.Printf("Partial headers message received, processed %d headers, requesting more", headersProcessed)
			// Request more headers from where we left off
			if err := n.sendGetHeaders(n.chainParams.GenesisBlock); err != nil {
				return fmt.Errorf("error requesting more headers: %v", err)
			}
			return nil
		}

		header := BlockHeader{}

		// Version (4 bytes)
		if err := binary.Read(reader, binary.LittleEndian, &header.Version); err != nil {
			return fmt.Errorf("error reading header version: %v", err)
		}

		// Previous block hash (32 bytes)
		if _, err := reader.Read(header.PrevBlock[:]); err != nil {
			return fmt.Errorf("error reading previous block hash: %v", err)
		}

		// Merkle root (32 bytes)
		if _, err := reader.Read(header.MerkleRoot[:]); err != nil {
			return fmt.Errorf("error reading merkle root: %v", err)
		}

		// Time (4 bytes)
		if err := binary.Read(reader, binary.LittleEndian, &header.Time); err != nil {
			return fmt.Errorf("error reading time: %v", err)
		}

		// Bits (4 bytes)
		if err := binary.Read(reader, binary.LittleEndian, &header.Bits); err != nil {
			return fmt.Errorf("error reading bits: %v", err)
		}

		// Nonce (4 bytes)
		if err := binary.Read(reader, binary.LittleEndian, &header.Nonce); err != nil {
			return fmt.Errorf("error reading nonce: %v", err)
		}

		// Transaction count (varint) - should be 0 for headers message
		txCount, err := binary.ReadUvarint(reader)
		if err != nil {
			return fmt.Errorf("error reading transaction count: %v", err)
		}
		if txCount != 0 {
			n.logger.Printf("Warning: header message contains transaction count %d, expected 0", txCount)
			// Skip the transaction data if present
			if txCount > 0 {
				// Skip the transaction data
				_, err = reader.Read(make([]byte, txCount))
				if err != nil {
					return fmt.Errorf("error skipping transaction data: %v", err)
				}
			}
		}

		// Calculate block hash
		hash := header.Hash()

		// Calculate height based on our current height
		height := n.currentHeight + uint32(headersProcessed) + 1

		// Only store headers at or above our start height
		if height >= n.startHeight {
			n.logger.Printf("Received header for block %x at height %d", hash, height)
			n.headersMutex.Lock()
			n.headers[height] = header
			n.currentHeight = height
			n.headersMutex.Unlock()
			headersProcessed++
		}
	}

	// If we processed all headers in this message, request more if needed
	if headersProcessed > 0 {
		n.logger.Printf("Processed %d headers, current height: %d", headersProcessed, n.currentHeight)
		// Request more headers if we haven't reached the target height
		if n.currentHeight < n.bestKnownHeight {
			n.logger.Printf("Requesting more headers from height %d", n.currentHeight+1)
			return n.sendGetHeaders(n.chainParams.GenesisBlock)
		} else {
			n.logger.Printf("Reached target height %d", n.currentHeight)
			n.headerSyncComplete = true
			// Start requesting blocks
			n.logger.Printf("Header sync complete, starting block download from height %d", n.startHeight)
			return n.sendGetBlocks()
		}
	}

	return nil
}

// handleBlockMessage handles a block message
func (n *SPVNode) handleBlockMessage(payload []byte) error {
	// Parse block message
	block, err := n.parseBlockMessage(payload)
	if err != nil {
		return fmt.Errorf("error parsing block message: %v", err)
	}

	// Calculate block hash
	headerBytes := block.Header.Serialize()
	hash1 := sha256.Sum256(headerBytes)
	hash2 := sha256.Sum256(hash1[:])
	blockHash := hex.EncodeToString(hash2[:])

	n.logger.Printf("Processing block %s at height %d", blockHash, block.Header.Height)

	// Store block in memory
	n.blocks[blockHash] = block

	// Store block in database
	if err := n.db.StoreBlock(block); err != nil {
		n.logger.Printf("Error storing block in database: %v", err)
		return fmt.Errorf("error storing block in database: %v", err)
	}
	n.logger.Printf("Successfully stored block %s in database", blockHash)

	// Process transactions
	relevantTxs := 0
	for _, tx := range block.Transactions {
		if n.ProcessTransaction(tx) {
			n.logger.Printf("Found relevant transaction %s", tx.TxID)
			// Store transaction in database
			if err := n.db.StoreTransaction(tx, blockHash, block.Header.Height); err != nil {
				n.logger.Printf("Error storing transaction %s in database: %v", tx.TxID, err)
				continue
			}
			relevantTxs++
		}
	}
	n.logger.Printf("Processed %d relevant transactions in block %s", relevantTxs, blockHash)

	// Check if we've reached the best known height
	if block.Header.Height >= n.bestKnownHeight-5 {
		n.logger.Printf("Block sync complete at height %d", block.Header.Height)
		n.blockSyncComplete = true
	} else {
		// Request next block
		n.logger.Printf("Requesting next block after %d", block.Header.Height)
		if err := n.sendGetBlocks(); err != nil {
			return fmt.Errorf("error requesting next block: %v", err)
		}
	}

	return nil
}

// handleTxMessage handles a transaction message
func (n *SPVNode) handleTxMessage(payload []byte) error {
	// Parse and process transaction
	return nil
}

// handleInvMessage handles an inventory message
func (n *SPVNode) handleInvMessage(payload []byte) error {
	// Parse inventory count (varint)
	reader := bytes.NewReader(payload)
	count, err := binary.ReadUvarint(reader)
	if err != nil {
		return fmt.Errorf("error reading inventory count: %v", err)
	}
	log.Printf("Received inventory message with %d items", count)

	// Parse each inventory item
	for i := uint64(0); i < count; i++ {
		// Type (4 bytes)
		var invType uint32
		if err := binary.Read(reader, binary.LittleEndian, &invType); err != nil {
			return fmt.Errorf("error reading inventory type: %v", err)
		}

		// Hash (32 bytes)
		var hash [32]byte
		if _, err := reader.Read(hash[:]); err != nil {
			return fmt.Errorf("error reading inventory hash: %v", err)
		}

		// Convert hash to hex string
		hashStr := hex.EncodeToString(hash[:])

		switch invType {
		case 2: // MSG_BLOCK
			log.Printf("Received block inventory: %s", hashStr)
			// Request the block
			if err := n.sendGetData(invType, hash); err != nil {
				log.Printf("Error requesting block: %v", err)
			}
		case 1: // MSG_TX
			log.Printf("Received transaction inventory: %s", hashStr)
			// Request the transaction
			if err := n.sendGetData(invType, hash); err != nil {
				log.Printf("Error requesting transaction: %v", err)
			}
		default:
			log.Printf("Received unknown inventory type %d: %s", invType, hashStr)
		}
	}

	return nil
}

// handlePingMessage handles a ping message
func (n *SPVNode) handlePingMessage(payload []byte) error {
	// Send pong message with same nonce
	checksum := doubleSha256(payload)[:4]
	var checksumArray [4]byte
	copy(checksumArray[:], checksum)

	msg := &Message{
		Magic:    0xc0c0c0c0,
		Length:   uint32(len(payload)),
		Checksum: checksumArray,
		Payload:  payload,
	}
	copy(msg.Command[:], MsgPong)

	return n.sendMessage(msg)
}

// AddWatchAddress adds an address to watch
func (n *SPVNode) AddWatchAddress(address string) {
	log.Printf("Adding address to watch list: %s", address)
	n.watchAddresses[address] = true
	n.updateBloomFilter()
}

// GetBlockCount returns the current block height
func (n *SPVNode) GetBlockCount() (int64, error) {
	if n.conn == nil {
		return 0, fmt.Errorf("not connected to peer")
	}
	// In a real implementation, this would query the peer for the current height
	// For now, return the current height from our headers
	n.headersMutex.RLock()
	defer n.headersMutex.RUnlock()

	var maxHeight uint32
	for height := range n.headers {
		if height > maxHeight {
			maxHeight = height
		}
	}
	n.logger.Printf("Current block height: %d", maxHeight)
	return int64(maxHeight), nil
}

// parseNetworkTransaction parses a transaction from the network protocol
func parseNetworkTransaction(payload []byte) (Transaction, int, error) {
	if len(payload) < 4 {
		return Transaction{}, 0, fmt.Errorf("transaction message too short")
	}

	tx := Transaction{
		Version: binary.LittleEndian.Uint32(payload[0:4]),
	}

	// Parse input count
	inputCount, n := binary.Uvarint(payload[4:])
	if n <= 0 {
		return Transaction{}, 0, fmt.Errorf("failed to parse input count")
	}
	offset := 4 + n

	// Parse inputs
	tx.Inputs = make([]TxInput, inputCount)
	for i := uint64(0); i < inputCount; i++ {
		if len(payload[offset:]) < 36 {
			return Transaction{}, 0, fmt.Errorf("input %d too short", i)
		}

		input := TxInput{
			PreviousOutput: OutPoint{
				Hash:  [32]byte{},
				Index: binary.LittleEndian.Uint32(payload[offset+32 : offset+36]),
			},
		}
		copy(input.PreviousOutput.Hash[:], payload[offset:offset+32])

		// Parse script length
		scriptLen, n := binary.Uvarint(payload[offset+36:])
		if n <= 0 {
			return Transaction{}, 0, fmt.Errorf("failed to parse script length for input %d", i)
		}
		offset += 36 + n

		// Parse script
		if len(payload[offset:]) < int(scriptLen) {
			return Transaction{}, 0, fmt.Errorf("script for input %d too short", i)
		}
		input.ScriptSig = make([]byte, scriptLen)
		copy(input.ScriptSig, payload[offset:offset+int(scriptLen)])
		offset += int(scriptLen)

		// Parse sequence
		if len(payload[offset:]) < 4 {
			return Transaction{}, 0, fmt.Errorf("sequence for input %d too short", i)
		}
		input.Sequence = binary.LittleEndian.Uint32(payload[offset : offset+4])
		offset += 4

		tx.Inputs[i] = input
	}

	// Parse output count
	outputCount, n := binary.Uvarint(payload[offset:])
	if n <= 0 {
		return Transaction{}, 0, fmt.Errorf("failed to parse output count")
	}
	offset += n

	// Parse outputs
	tx.Outputs = make([]TxOutput, outputCount)
	for i := uint64(0); i < outputCount; i++ {
		if len(payload[offset:]) < 8 {
			return Transaction{}, 0, fmt.Errorf("output %d too short", i)
		}

		output := TxOutput{
			Value: binary.LittleEndian.Uint64(payload[offset : offset+8]),
		}
		offset += 8

		// Parse script length
		scriptLen, n := binary.Uvarint(payload[offset:])
		if n <= 0 {
			return Transaction{}, 0, fmt.Errorf("failed to parse script length for output %d", i)
		}
		offset += n

		// Parse script
		if len(payload[offset:]) < int(scriptLen) {
			return Transaction{}, 0, fmt.Errorf("script for output %d too short", i)
		}
		output.ScriptPubKey = make([]byte, scriptLen)
		copy(output.ScriptPubKey, payload[offset:offset+int(scriptLen)])
		offset += int(scriptLen)

		tx.Outputs[i] = output
	}

	// Parse lock time
	if len(payload[offset:]) < 4 {
		return Transaction{}, 0, fmt.Errorf("lock time too short")
	}
	tx.LockTime = binary.LittleEndian.Uint32(payload[offset : offset+4])
	offset += 4

	// Calculate TxID (double SHA-256 of the serialized transaction)
	hash1 := sha256.Sum256(payload[:offset])
	hash2 := sha256.Sum256(hash1[:])
	tx.TxID = hex.EncodeToString(hash2[:])

	return tx, offset, nil
}

// isRelevant checks if a transaction is relevant to our watch addresses
func (n *SPVNode) isRelevant(tx *Transaction) bool {
	// Check if any of our watch addresses are in the transaction
	for _, output := range tx.Outputs {
		scriptHash := sha256.Sum256(output.ScriptPubKey)
		if n.bloomFilter != nil {
			// Check if the script hash matches our bloom filter
			if bytes.Contains(n.bloomFilter, scriptHash[:]) {
				return true
			}
		}
	}
	return false
}

// parseBlockMessage parses a block message from the network protocol
func (n *SPVNode) parseBlockMessage(payload []byte) (*Block, error) {
	if len(payload) < 80 {
		return nil, fmt.Errorf("block message too short")
	}

	block := &Block{
		Header: BlockHeader{
			Version:    binary.LittleEndian.Uint32(payload[0:4]),
			PrevBlock:  [32]byte{},
			MerkleRoot: [32]byte{},
			Time:       binary.LittleEndian.Uint32(payload[68:72]),
			Bits:       binary.LittleEndian.Uint32(payload[72:76]),
			Nonce:      binary.LittleEndian.Uint32(payload[76:80]),
		},
		Transactions: make([]*Transaction, 0),
	}

	copy(block.Header.PrevBlock[:], payload[4:36])
	copy(block.Header.MerkleRoot[:], payload[36:68])

	// Parse transaction count
	txCount, bytesRead := binary.Uvarint(payload[80:])
	if bytesRead <= 0 {
		return nil, fmt.Errorf("failed to parse transaction count")
	}

	// Parse transactions
	offset := 80 + bytesRead
	for i := uint64(0); i < txCount; i++ {
		tx, bytesRead, err := parseNetworkTransaction(payload[offset:])
		if err != nil {
			return nil, fmt.Errorf("failed to parse transaction %d: %v", i, err)
		}
		block.Transactions = append(block.Transactions, &tx)
		offset += bytesRead
	}

	return block, nil
}

// GetBlockTransactions requests and processes transactions for a specific block
func (n *SPVNode) GetBlockTransactions(blockHash string) ([]*Transaction, error) {
	if !n.connected {
		return nil, fmt.Errorf("not connected to peer")
	}

	// Convert block hash to bytes
	hashBytes, err := hex.DecodeString(blockHash)
	if err != nil {
		return nil, fmt.Errorf("failed to decode block hash: %v", err)
	}
	if len(hashBytes) != 32 {
		return nil, fmt.Errorf("invalid block hash length")
	}

	// Create getdata message payload
	payload := make([]byte, 1+32) // 1 byte for count + 32 bytes for hash
	payload[0] = 1                // Count of 1
	copy(payload[1:], hashBytes)

	// Create message
	checksum := doubleSha256(payload)[:4]
	var checksumArray [4]byte
	copy(checksumArray[:], checksum)

	msg := &Message{
		Magic:    0xc0c0c0c0,
		Length:   uint32(len(payload)),
		Checksum: checksumArray,
		Payload:  payload,
	}
	copy(msg.Command[:], MsgGetData)

	// Send getdata message
	n.logger.Printf("Sending getdata message for block %s", blockHash)
	if err := n.sendMessage(msg); err != nil {
		n.connected = false
		return nil, fmt.Errorf("failed to send getdata message: %v", err)
	}

	// Wait for block message with timeout
	timeout := time.After(30 * time.Second)
	blockChan := make(chan *Block)
	errorChan := make(chan error)

	go func() {
		for {
			msg, err := n.readMessage()
			if err != nil {
				if err == io.EOF {
					n.connected = false
					errorChan <- fmt.Errorf("connection closed by peer")
					return
				}
				errorChan <- fmt.Errorf("failed to read message: %v", err)
				return
			}

			command := string(bytes.TrimRight(msg.Command[:], "\x00"))
			n.logger.Printf("Received message type: %s", command)

			if command == MsgBlock {
				block, err := n.parseBlockMessage(msg.Payload)
				if err != nil {
					errorChan <- fmt.Errorf("failed to parse block message: %v", err)
					return
				}

				// Verify block hash matches
				blockHashBytes := block.Header.Hash()
				if !bytes.Equal(blockHashBytes, hashBytes) {
					errorChan <- fmt.Errorf("block hash mismatch")
					return
				}

				blockChan <- block
				return
			}
		}
	}()

	select {
	case block := <-blockChan:
		// Filter transactions that match our bloom filter
		var relevantTxs []*Transaction
		for _, tx := range block.Transactions {
			if n.isRelevant(tx) {
				n.logger.Printf("Found relevant transaction: %s", tx.TxID)
				relevantTxs = append(relevantTxs, tx)
			}
		}
		return relevantTxs, nil

	case err := <-errorChan:
		return nil, err

	case <-timeout:
		return nil, fmt.Errorf("timeout waiting for block message")
	}
}

// Internal functions

func (n *SPVNode) updateBloomFilter() {
	log.Printf("Updating bloom filter with %d watched addresses", len(n.watchAddresses))
	// In a real implementation, this would:
	// 1. Create a new bloom filter with appropriate size and false positive rate
	// 2. Add all watched addresses to the filter
	// 3. Send filterload message to peer
	n.bloomFilter = make([]byte, 256) // Placeholder implementation
}

// Helper functions

func extractAddressesFromScript(script []byte) []string {
	log.Printf("Extracting addresses from script of length %d", len(script))
	// In a real implementation, this would:
	// 1. Parse the script
	// 2. Extract P2PKH, P2SH, and other address types
	// 3. Convert to base58 addresses
	return []string{} // Placeholder implementation
}

// Message handling functions (to be implemented)

// Network protocol functions (to be implemented)

func (n *SPVNode) sendGetData(invType uint32, hash [32]byte) error {
	log.Printf("Sending getdata message for type %d, hash %x", invType, hash)
	return nil
}

func (n *SPVNode) sendMemPool() error {
	log.Printf("Sending mempool message")
	return nil
}

// Merkle block verification (to be implemented)

func (n *SPVNode) verifyMerkleProof(header BlockHeader, txid [32]byte, proof []byte) bool {
	log.Printf("Verifying merkle proof for transaction %x", txid)
	return false
}

// Chain validation functions (to be implemented)

func (n *SPVNode) validateHeader(header BlockHeader) error {
	log.Printf("Validating block header at height %d", header.Height)
	return nil
}

func (n *SPVNode) validateChain() error {
	log.Printf("Validating chain with %d headers", len(n.headers))
	return nil
}

// addressToScriptHash converts a Dogecoin address to its script hash
func (n *SPVNode) addressToScriptHash(address string) []byte {
	// TODO: Implement address to script hash conversion
	return []byte{}
}

// ExtractAddresses extracts addresses from a script
func (n *SPVNode) ExtractAddresses(script []byte) []string {
	addresses := make([]string, 0)

	// Check script length
	if len(script) < 23 { // Minimum length for P2PKH
		return addresses
	}

	// Check for P2PKH (Pay to Public Key Hash)
	// Format: OP_DUP OP_HASH160 <20 bytes> OP_EQUALVERIFY OP_CHECKSIG
	if len(script) == 25 && script[0] == 0x76 && script[1] == 0xa9 && script[2] == 0x14 && script[23] == 0x88 && script[24] == 0xac {
		pubKeyHash := script[3:23]
		// Convert to base58check with version byte 0x1E (Dogecoin P2PKH)
		version := []byte{0x1E}
		data := append(version, pubKeyHash...)
		hash1 := sha256.Sum256(data)
		hash2 := sha256.Sum256(hash1[:])
		checksum := hash2[:4]
		final := append(data, checksum...)
		address := base58Encode(final)
		addresses = append(addresses, address)
		return addresses
	}

	// Check for P2SH (Pay to Script Hash)
	// Format: OP_HASH160 <20 bytes> OP_EQUAL
	if len(script) == 23 && script[0] == 0xa9 && script[1] == 0x14 && script[22] == 0x87 {
		scriptHash := script[2:22]
		// Convert to base58check with version byte 0x16 (Dogecoin P2SH)
		version := []byte{0x16}
		data := append(version, scriptHash...)
		hash1 := sha256.Sum256(data)
		hash2 := sha256.Sum256(hash1[:])
		checksum := hash2[:4]
		final := append(data, checksum...)
		address := base58Encode(final)
		addresses = append(addresses, address)
		return addresses
	}

	return addresses
}

// base58Encode encodes a byte slice to base58
func base58Encode(input []byte) string {
	const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

	// Convert big-endian bytes to big int
	var n big.Int
	n.SetBytes(input)

	// Divide by 58 until quotient becomes zero
	var result []byte
	mod := new(big.Int)
	zero := new(big.Int)
	base := big.NewInt(58)

	for n.Cmp(zero) > 0 {
		n.DivMod(&n, base, mod)
		result = append(result, alphabet[mod.Int64()])
	}

	// Add leading zeros
	for _, b := range input {
		if b != 0 {
			break
		}
		result = append(result, alphabet[0])
	}

	// Reverse
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	return string(result)
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

// GetBlockHash returns the hash of the block at the given height
func (n *SPVNode) GetBlockHash(height int64) (string, error) {
	// TODO: Implement block hash retrieval
	return "", nil
}

// ProcessTransaction checks if a transaction is relevant to our watched addresses
func (n *SPVNode) ProcessTransaction(tx *Transaction) bool {
	n.logger.Printf("Processing transaction")
	for _, output := range tx.Outputs {
		// Extract addresses from output script
		addresses := extractAddressesFromScript(output.ScriptPubKey)
		for _, addr := range addresses {
			if n.watchAddresses[addr] {
				n.logger.Printf("Found relevant transaction for watched address %s", addr)
				return true
			}
		}
	}
	return false
}

// StartConnectionManager manages the connection to peers
func (n *SPVNode) StartConnectionManager() {
	go func() {
		for {
			select {
			case <-n.stopChan:
				return
			default:
				if !n.connected {
					n.logger.Printf("Not connected, attempting to connect to peers...")
					for _, peer := range n.peers {
						if err := n.ConnectToPeer(peer); err != nil {
							n.logger.Printf("Failed to connect to peer %s: %v", peer, err)
							continue
						}
						break
					}
				}
				time.Sleep(n.reconnectDelay)
			}
		}
	}()
}

// Stop stops the SPV node
func (n *SPVNode) Stop() {
	close(n.stopChan)
	if n.conn != nil {
		n.conn.Close()
		n.conn = nil
	}
	n.connected = false
}

// sendGetBlocks sends a getblocks message to request blocks
func (n *SPVNode) sendGetBlocks() error {
	// Create getblocks message payload
	payload := make([]byte, 0)

	// Version (4 bytes)
	versionBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(versionBytes, ProtocolVersion)
	payload = append(payload, versionBytes...)

	// Hash count (varint)
	payload = append(payload, 0x01) // One hash

	// Block locator hashes (32 bytes)
	// Start with the block at current height
	if n.currentHeight > 0 {
		// Find the block hash at current height
		n.headersMutex.RLock()
		header, exists := n.headers[n.currentHeight]
		n.headersMutex.RUnlock()

		if exists {
			// Calculate hash of the header
			headerBytes := header.Serialize()
			hash1 := sha256.Sum256(headerBytes)
			hash2 := sha256.Sum256(hash1[:])
			payload = append(payload, hash2[:]...)
		} else {
			// If we don't have the header, use genesis block hash
			genesisHash, err := hex.DecodeString(n.chainParams.GenesisBlock)
			if err != nil {
				return fmt.Errorf("failed to decode genesis block hash: %v", err)
			}
			// Reverse the hash (Dogecoin uses little-endian)
			for i, j := 0, len(genesisHash)-1; i < j; i, j = i+1, j-1 {
				genesisHash[i], genesisHash[j] = genesisHash[j], genesisHash[i]
			}
			payload = append(payload, genesisHash...)
		}
	} else {
		// Start with genesis block hash
		genesisHash, err := hex.DecodeString(n.chainParams.GenesisBlock)
		if err != nil {
			return fmt.Errorf("failed to decode genesis block hash: %v", err)
		}
		// Reverse the hash (Dogecoin uses little-endian)
		for i, j := 0, len(genesisHash)-1; i < j; i, j = i+1, j-1 {
			genesisHash[i], genesisHash[j] = genesisHash[j], genesisHash[i]
		}
		payload = append(payload, genesisHash...)
	}

	// Stop hash (32 bytes) - all zeros to get all blocks
	stopHash := make([]byte, 32)
	payload = append(payload, stopHash...)

	// Create message
	checksum := doubleSha256(payload)[:4]
	var checksumArray [4]byte
	copy(checksumArray[:], checksum)

	msg := &Message{
		Magic:    0xc0c0c0c0,
		Length:   uint32(len(payload)),
		Checksum: checksumArray,
		Payload:  payload,
	}
	copy(msg.Command[:], "getblocks")

	n.logger.Printf("Sending getblocks message with payload length: %d", len(payload))
	n.logger.Printf("Requesting blocks starting from height %d", n.currentHeight)
	return n.sendMessage(msg)
}
