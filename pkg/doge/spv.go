package doge

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/mr-tron/base58"
)

const (
	// Protocol version
	ProtocolVersion = 70015

	// Message types
	MsgVersion    = "version"
	MsgVerack     = "verack"
	MsgGetHeaders = "getheaders"
	MsgHeaders    = "headers"
	MsgGetData    = "getdata"
	MsgBlock      = "block"
	MsgTx         = "tx"
	MsgInv        = "inv"
	MsgFilterLoad = "filterload"
	MsgPing       = "ping"
	MsgPong       = "pong"

	// MaxMessageSize is the maximum allowed size for a message
	MaxMessageSize = 32 * 1024 * 1024 // 32MB

	// MaxHeadersResults is the maximum number of headers to request in one getheaders message
	MaxHeadersResults = 1000
)

// Message represents a Dogecoin protocol message
type Message struct {
	Command [12]byte
	Payload []byte
}

// Deserialize reads a block header from a byte slice
func (h *BlockHeader) Deserialize(data []byte) error {
	if len(data) < 80 {
		return fmt.Errorf("header data too short")
	}

	h.Version = binary.LittleEndian.Uint32(data[0:4])
	copy(h.PrevBlock[:], data[4:36])
	copy(h.MerkleRoot[:], data[36:68])
	h.Time = binary.LittleEndian.Uint32(data[68:72])
	h.Bits = binary.LittleEndian.Uint32(data[72:76])
	h.Nonce = binary.LittleEndian.Uint32(data[76:80])

	return nil
}

// SPVNode represents a Simplified Payment Verification node
type SPVNode struct {
	headers            map[uint32]BlockHeader
	blocks             map[string]*Block
	peers              []string
	watchAddresses     map[string]bool
	bloomFilter        []byte
	currentHeight      uint32
	verackReceived     chan struct{}
	db                 Database
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
	blockChan          chan *Block
	targetHeight       uint32
}

// NewSPVNode creates a new SPV node
func NewSPVNode(peers []string, startHeight uint32, db Database) *SPVNode {
	// Initialize chain params with Dogecoin mainnet parameters
	chainParams := &ChainParams{
		ChainName:    "mainnet",
		GenesisBlock: "1a91e3dace36e2be3bf030a65679fe821aa1d6ef92e7c9902eb318182c355691",
		DefaultPort:  22556,
		RPCPort:      22555,
		DNSSeeds:     []string{"seed.dogecoin.com", "seed.multidoge.org", "seed.dogechain.info"},
		Checkpoints:  make(map[int]string),
	}

	// Create SPV node
	node := &SPVNode{
		peers:              peers,
		headers:            make(map[uint32]BlockHeader),
		blocks:             make(map[string]*Block),
		currentHeight:      uint32(startHeight),
		startHeight:        uint32(startHeight),
		bestKnownHeight:    0,
		db:                 db,
		verackReceived:     make(chan struct{}),
		headerSyncComplete: false,
		blockSyncComplete:  false,
		logger:             log.New(log.Writer(), "SPV: ", log.LstdFlags),
		chainParams:        chainParams,
		stopChan:           make(chan struct{}),
		reconnectDelay:     5 * time.Second,
		blockChan:          make(chan *Block),
		targetHeight:       uint32(startHeight),
	}

	// Load existing headers from database
	if err := node.loadHeadersFromDB(); err != nil {
		log.Printf("Error loading headers from database: %v", err)
	}

	return node
}

// loadHeadersFromDB loads headers from the database
func (n *SPVNode) loadHeadersFromDB() error {
	// Get all headers from database
	headers, err := n.db.GetHeaders()
	if err != nil {
		return fmt.Errorf("error getting headers from database: %v", err)
	}

	// Store headers in memory
	n.headersMutex.Lock()
	defer n.headersMutex.Unlock()
	for _, header := range headers {
		n.headers[header.Height] = *header
		if header.Height > n.currentHeight {
			n.currentHeight = header.Height
		}
	}

	n.logger.Printf("Loaded %d headers from database, current height: %d", len(headers), n.currentHeight)
	return nil
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

	// Set connection timeout
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
	}

	// Connect to peer
	conn, err := dialer.Dial("tcp", peer)
	if err != nil {
		return fmt.Errorf("failed to connect to peer: %v", err)
	}

	// Set read and write timeouts
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	conn.SetWriteDeadline(time.Now().Add(30 * time.Second))

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

	// Reset read deadline
	conn.SetReadDeadline(time.Time{})

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
	n.logger.Printf("Sending verack...")
	if err := n.sendVerackMessage(); err != nil {
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
	prevBlock := n.headers[n.currentHeight].PrevBlock
	if err := n.sendGetHeaders(hex.EncodeToString(prevBlock[:])); err != nil {
		n.conn.Close()
		n.conn = nil
		n.connected = false
		return fmt.Errorf("error sending getheaders message: %v", err)
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
			if err := n.handleHeadersMessage(msg); err != nil {
				n.logger.Printf("Error handling headers message: %v", err)
			}
		case MsgBlock:
			if err := n.handleBlockMessage(msg.Payload); err != nil {
				n.logger.Printf("Error handling block message: %v", err)
			}
		case MsgTx:
			if err := n.handleTxMessage(msg); err != nil {
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

	// Send message
	return n.sendMessage(NewMessage(MsgHeaders, payload))
}

// readMessage reads a message from the connection
func (n *SPVNode) readMessage() (*Message, error) {
	// Read message header (24 bytes)
	header := make([]byte, 24)
	if _, err := io.ReadFull(n.conn, header); err != nil {
		return nil, fmt.Errorf("failed to read message header: %v", err)
	}

	// Parse message length
	length := binary.LittleEndian.Uint32(header[16:20])
	if length > MaxMessageSize {
		return nil, fmt.Errorf("message size %d exceeds maximum allowed size %d", length, MaxMessageSize)
	}

	// Read message payload
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(n.conn, payload); err != nil {
			return nil, fmt.Errorf("failed to read message payload: %v", err)
		}
	}

	// Create message
	msg := &Message{
		Command: [12]byte{},
		Payload: payload,
	}
	copy(msg.Command[:], header[4:16])

	// Verify checksum
	hash1 := sha256.Sum256(payload)
	hash2 := sha256.Sum256(hash1[:])
	if !bytes.Equal(hash2[:4], header[20:24]) {
		return nil, fmt.Errorf("invalid message checksum")
	}

	return msg, nil
}

// sendMessage sends a message to the peer
func (n *SPVNode) sendMessage(msg *Message) error {
	if n.conn == nil {
		return fmt.Errorf("not connected")
	}

	// Write message
	if err := binary.Write(n.conn, binary.LittleEndian, msg.Command); err != nil {
		return err
	}

	// Write payload length
	payloadLen := uint32(len(msg.Payload))
	if err := binary.Write(n.conn, binary.LittleEndian, payloadLen); err != nil {
		return err
	}

	// Write payload
	if _, err := n.conn.Write(msg.Payload); err != nil {
		return err
	}

	n.lastMessage = time.Now()
	return nil
}

// sendVersionMessage sends a version message
func (n *SPVNode) sendVersionMessage() error {
	// Create version message payload
	payload := make([]byte, 86) // Version message size
	binary.LittleEndian.PutUint32(payload[0:4], ProtocolVersion)

	// Services (8 bytes) - NODE_NETWORK
	binary.LittleEndian.PutUint64(payload[4:12], 1)

	// Timestamp (8 bytes)
	binary.LittleEndian.PutUint64(payload[12:20], uint64(time.Now().Unix()))

	// Receiver services (8 bytes)
	binary.LittleEndian.PutUint64(payload[20:28], 1)

	// Receiver IP (16 bytes) - IPv4 mapped to IPv6
	payload[28] = 0x00
	payload[29] = 0x00
	payload[30] = 0x00
	payload[31] = 0x00
	payload[32] = 0x00
	payload[33] = 0x00
	payload[34] = 0x00
	payload[35] = 0x00
	payload[36] = 0x00
	payload[37] = 0x00
	payload[38] = 0xFF
	payload[39] = 0xFF
	payload[40] = 0x00
	payload[41] = 0x00
	payload[42] = 0x00
	payload[43] = 0x00

	// Receiver port (2 bytes)
	binary.BigEndian.PutUint16(payload[44:46], 22556)

	// Sender services (8 bytes)
	binary.LittleEndian.PutUint64(payload[46:54], 1)

	// Sender IP (16 bytes) - IPv4 mapped to IPv6
	payload[54] = 0x00
	payload[55] = 0x00
	payload[56] = 0x00
	payload[57] = 0x00
	payload[58] = 0x00
	payload[59] = 0x00
	payload[60] = 0x00
	payload[61] = 0x00
	payload[62] = 0x00
	payload[63] = 0x00
	payload[64] = 0xFF
	payload[65] = 0xFF
	payload[66] = 0x00
	payload[67] = 0x00
	payload[68] = 0x00
	payload[69] = 0x00

	// Sender port (2 bytes)
	binary.BigEndian.PutUint16(payload[70:72], 22556)

	// Nonce (8 bytes)
	binary.LittleEndian.PutUint64(payload[72:80], uint64(time.Now().UnixNano()))

	// User agent length (varint)
	payload[80] = 0x00

	// Start height (4 bytes)
	binary.LittleEndian.PutUint32(payload[82:86], 0)

	return n.sendMessage(NewMessage(MsgVersion, payload))
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
	binary.LittleEndian.PutUint32(payload[len(payload):len(payload)+4], 11)

	// Tweak (4 bytes)
	binary.LittleEndian.PutUint32(payload[len(payload):len(payload)+4], uint32(time.Now().UnixNano()))

	// Flags (1 byte)
	payload = append(payload, 0x01) // BLOOM_UPDATE_ALL

	return n.sendMessage(NewMessage(MsgFilterLoad, payload))
}

// sendGetHeaders sends a getheaders message to request block headers
func (n *SPVNode) sendGetHeaders(startHash string) error {
	// Get last processed block info
	lastHash, lastHeight, _, err := n.db.GetLastProcessedBlock()
	if err != nil {
		return fmt.Errorf("error getting last processed block: %v", err)
	}

	// Build block locator hashes
	locatorHashes := [][]byte{}
	if lastHeight > 0 {
		// Start from last processed block
		hashBytes, err := hex.DecodeString(lastHash)
		if err != nil {
			return fmt.Errorf("error decoding block hash: %v", err)
		}
		locatorHashes = append(locatorHashes, hashBytes)
	} else {
		// Start from genesis block
		genesisHash, err := hex.DecodeString(n.chainParams.GenesisBlock)
		if err != nil {
			return fmt.Errorf("error decoding genesis hash: %v", err)
		}
		locatorHashes = append(locatorHashes, genesisHash)
	}

	// Build getheaders message
	payload := make([]byte, 0)
	payload = append(payload, uint32ToBytes(70015)...) // Protocol version
	payload = append(payload, uint8ToBytes(1)...)      // Hash count (always 1 for SPV)
	payload = append(payload, locatorHashes[0]...)     // Block locator hash
	payload = append(payload, make([]byte, 32)...)     // Stop hash (all zeros)

	msg := NewMessage("getheaders", payload)

	// Send message to all peers
	for _, peer := range n.peers {
		if err := n.sendMessage(msg); err != nil {
			log.Printf("Error sending getheaders to peer %s: %v", peer, err)
		}
	}

	return nil
}

// handleVersionMessage handles a version message
func (n *SPVNode) handleVersionMessage(payload []byte) error {
	// Parse version message
	version := binary.LittleEndian.Uint32(payload[0:4])
	if version < ProtocolVersion {
		return fmt.Errorf("peer version %d is too old", version)
	}

	// Send verack
	return n.sendVerackMessage()
}

// handleHeadersMessage processes incoming headers messages
func (n *SPVNode) handleHeadersMessage(msg *Message) error {
	// Read headers count
	count, err := ReadVarInt(msg.Payload)
	if err != nil {
		return fmt.Errorf("error reading headers count: %v", err)
	}

	// Process each header
	offset := 1 // Skip the count byte
	for i := uint64(0); i < count; i++ {
		if offset+80 > len(msg.Payload) {
			log.Printf("Partial headers message received, requesting more")
			return nil
		}

		header := &BlockHeader{}
		headerData := msg.Payload[offset : offset+80]
		header.Version = binary.LittleEndian.Uint32(headerData[0:4])
		copy(header.PrevBlock[:], headerData[4:36])
		copy(header.MerkleRoot[:], headerData[36:68])
		header.Time = binary.LittleEndian.Uint32(headerData[68:72])
		header.Bits = binary.LittleEndian.Uint32(headerData[72:76])
		header.Nonce = binary.LittleEndian.Uint32(headerData[76:80])

		// Update current height
		n.currentHeight++
		header.Height = uint32(n.currentHeight)

		// Store header
		if err := n.db.StoreBlock(&Block{Header: *header}); err != nil {
			return fmt.Errorf("error storing header: %v", err)
		}

		offset += 80
	}

	// Request more headers if needed
	if n.currentHeight < n.targetHeight {
		return n.sendGetHeaders(n.chainParams.GenesisBlock)
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
			if err := n.db.StoreTransaction(tx, blockHash, n.currentHeight); err != nil {
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

// handleTxMessage processes incoming transaction messages
func (n *SPVNode) handleTxMessage(msg *Message) error {
	tx := &Transaction{}
	offset := 0

	// Read version
	if offset+4 > len(msg.Payload) {
		return fmt.Errorf("not enough bytes for version")
	}
	tx.Version = binary.LittleEndian.Uint32(msg.Payload[offset : offset+4])
	offset += 4

	// Read inputs
	inputCount, bytesRead := binary.Uvarint(msg.Payload[offset:])
	if bytesRead <= 0 {
		return fmt.Errorf("error reading input count")
	}
	offset += bytesRead

	for i := uint64(0); i < inputCount; i++ {
		var input TxInput
		if offset+36 > len(msg.Payload) {
			return fmt.Errorf("not enough bytes for input")
		}
		copy(input.PreviousOutput.Hash[:], msg.Payload[offset:offset+32])
		input.PreviousOutput.Index = binary.LittleEndian.Uint32(msg.Payload[offset+32 : offset+36])
		offset += 36

		// Read script length
		scriptLen, bytesRead := binary.Uvarint(msg.Payload[offset:])
		if bytesRead <= 0 {
			return fmt.Errorf("error reading script length")
		}
		offset += bytesRead

		// Read script
		if offset+int(scriptLen) > len(msg.Payload) {
			return fmt.Errorf("not enough bytes for script")
		}
		input.ScriptSig = make([]byte, scriptLen)
		copy(input.ScriptSig, msg.Payload[offset:offset+int(scriptLen)])
		offset += int(scriptLen)

		// Read sequence
		if offset+4 > len(msg.Payload) {
			return fmt.Errorf("not enough bytes for sequence")
		}
		input.Sequence = binary.LittleEndian.Uint32(msg.Payload[offset : offset+4])
		offset += 4

		tx.Inputs = append(tx.Inputs, input)
	}

	// Calculate transaction ID
	txBytes := msg.Payload[:offset]
	hash1 := sha256.Sum256(txBytes)
	hash2 := sha256.Sum256(hash1[:])
	tx.TxID = hex.EncodeToString(hash2[:])

	// Store transaction in database if relevant
	if n.isRelevantTransaction(tx) {
		n.logger.Printf("Found relevant transaction %s", tx.TxID)
		if err := n.db.StoreTransaction(tx, "", uint32(n.currentHeight)); err != nil {
			n.logger.Printf("Error storing transaction %s in database: %v", tx.TxID, err)
			return err
		}
	}

	return nil
}

// isRelevantTransaction checks if a transaction is relevant to the SPV node
func (n *SPVNode) isRelevantTransaction(tx *Transaction) bool {
	// Check if any output addresses match our watched addresses
	for _, output := range tx.Outputs {
		if n.isWatchedScript(output.ScriptPubKey) {
			return true
		}
	}
	return false
}

// isWatchedScript checks if a script corresponds to a watched address
func (n *SPVNode) isWatchedScript(script []byte) bool {
	if len(script) < 25 {
		return false
	}

	// Check for P2PKH script
	if script[0] == 0x76 && script[1] == 0xa9 && script[2] == 0x14 && script[23] == 0x88 && script[24] == 0xac {
		pubKeyHash := script[3:23]
		for addr := range n.watchAddresses {
			if bytes.Equal(pubKeyHash, []byte(addr)) {
				return true
			}
		}
	}

	return false
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
			if err := n.sendGetDataMessage(invType, hash); err != nil {
				log.Printf("Error requesting block: %v", err)
			}
		case 1: // MSG_TX
			log.Printf("Received transaction inventory: %s", hashStr)
			// Request the transaction
			if err := n.sendGetDataMessage(invType, hash); err != nil {
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
	log.Printf("Received ping message, sending pong")
	return n.sendPongMessage(payload)
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

// GetBlockTransactions retrieves transactions for a block
func (n *SPVNode) GetBlockTransactions(blockHash string) ([]*Transaction, error) {
	// Convert block hash to bytes
	hashBytes, err := hex.DecodeString(blockHash)
	if err != nil {
		return nil, fmt.Errorf("error decoding block hash: %v", err)
	}
	if len(hashBytes) != 32 {
		return nil, fmt.Errorf("invalid block hash length: expected 32 bytes, got %d", len(hashBytes))
	}

	// Create getdata message payload
	payload := make([]byte, 37) // 1 byte for type + 32 bytes for hash
	payload[0] = 2              // Set inventory type to block (2)
	copy(payload[1:], hashBytes)

	// Send message
	err = n.sendMessage(NewMessage(MsgGetData, payload))
	if err != nil {
		return nil, fmt.Errorf("error sending getdata message: %v", err)
	}

	// Wait for block message
	select {
	case blockMsg := <-n.blockChan:
		return blockMsg.Transactions, nil
	case <-time.After(30 * time.Second):
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

func (n *SPVNode) sendGetDataMessage(invType uint32, hash [32]byte) error {
	payload := make([]byte, 37)
	binary.LittleEndian.PutUint32(payload[0:4], 1) // Count
	binary.LittleEndian.PutUint32(payload[4:8], invType)
	copy(payload[8:40], hash[:])
	return n.sendMessage(NewMessage(MsgGetData, payload))
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
		address := Base58CheckEncode(final, 0x1E)
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
		address := Base58CheckEncode(final, 0x16)
		addresses = append(addresses, address)
		return addresses
	}

	return addresses
}

// Base58CheckEncode encodes a byte slice with version prefix in base58 with checksum
func Base58CheckEncode(input []byte, version byte) string {
	b := make([]byte, 0, 1+len(input)+4)
	b = append(b, version)
	b = append(b, input...)
	cksum := checksum(b)
	b = append(b, cksum[:]...)
	return base58.Encode(b)
}

// checksum returns the first four bytes of the double SHA256 hash
func checksum(input []byte) [4]byte {
	h := sha256.Sum256(input)
	h2 := sha256.Sum256(h[:])
	var cksum [4]byte
	copy(cksum[:], h2[:4])
	return cksum
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

	n.logger.Printf("Sending getblocks message with payload length: %d", len(payload))
	n.logger.Printf("Requesting blocks starting from height %d", n.currentHeight)
	return n.sendMessage(NewMessage("getblocks", payload))
}

// Helper functions for message serialization
func uint32ToBytes(n uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, n)
	return b
}

func uint8ToBytes(n uint8) []byte {
	return []byte{n}
}

// ReadVarInt reads a variable length integer from a byte slice
func ReadVarInt(payload []byte) (uint64, error) {
	if len(payload) == 0 {
		return 0, fmt.Errorf("empty payload")
	}

	// Read the first byte to determine the format
	firstByte := payload[0]

	switch {
	case firstByte < 0xfd:
		return uint64(firstByte), nil
	case firstByte == 0xfd:
		if len(payload) < 3 {
			return 0, fmt.Errorf("payload too short for uint16")
		}
		return uint64(binary.LittleEndian.Uint16(payload[1:3])), nil
	case firstByte == 0xfe:
		if len(payload) < 5 {
			return 0, fmt.Errorf("payload too short for uint32")
		}
		return uint64(binary.LittleEndian.Uint32(payload[1:5])), nil
	case firstByte == 0xff:
		if len(payload) < 9 {
			return 0, fmt.Errorf("payload too short for uint64")
		}
		return binary.LittleEndian.Uint64(payload[1:9]), nil
	default:
		return 0, fmt.Errorf("invalid varint format")
	}
}

// NewMessage creates a new message with the given command and payload
func NewMessage(command string, payload []byte) *Message {
	msg := &Message{
		Payload: payload,
	}
	copy(msg.Command[:], command)
	return msg
}

func (n *SPVNode) sendVerackMessage() error {
	return n.sendMessage(NewMessage(MsgVerack, nil))
}

func (n *SPVNode) sendPingMessage() error {
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	return n.sendMessage(NewMessage(MsgPing, nonce))
}

func (n *SPVNode) sendPongMessage(nonce []byte) error {
	return n.sendMessage(NewMessage(MsgPong, nonce))
}
