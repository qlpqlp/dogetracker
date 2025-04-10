package doge

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"time"
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
)

// Message represents a Dogecoin protocol message
type Message struct {
	Magic    [4]byte
	Command  [12]byte
	Length   uint32
	Checksum [4]byte
	Payload  []byte
}

// NewSPVNode creates a new SPV node
func NewSPVNode(peers []string) *SPVNode {
	log.Printf("Initializing SPV node with %d peers", len(peers))
	return &SPVNode{
		headers:        make(map[uint32]BlockHeader),
		peers:          peers,
		watchAddresses: make(map[string]bool),
		bloomFilter:    make([]byte, 256),
		verackReceived: make(chan struct{}),
	}
}

// ConnectToPeer connects to a peer
func (n *SPVNode) ConnectToPeer(peer string) error {
	log.Printf("Attempting to establish TCP connection to %s", peer)
	conn, err := net.DialTimeout("tcp", peer, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to peer %s: %v", peer, err)
	}
	n.conn = conn
	log.Printf("TCP connection established to %s", peer)

	// Start message handling loop
	go n.handleMessages()

	// Send version message
	log.Printf("Sending version message to %s", peer)
	if err := n.sendVersionMessage(); err != nil {
		n.conn.Close()
		return fmt.Errorf("failed to send version message: %v", err)
	}
	log.Printf("Version message sent successfully to %s", peer)

	// Wait for verack
	select {
	case <-time.After(10 * time.Second):
		n.conn.Close()
		return fmt.Errorf("timeout waiting for verack")
	case <-n.verackReceived:
		log.Printf("Received verack from %s", peer)
	}

	// Send filter load message
	log.Printf("Sending filter load message to %s", peer)
	if err := n.sendFilterLoadMessage(); err != nil {
		n.conn.Close()
		return fmt.Errorf("failed to send filter load message: %v", err)
	}
	log.Printf("Filter load message sent successfully to %s", peer)

	// Request headers
	log.Printf("Requesting headers from %s", peer)
	if err := n.sendGetHeaders(); err != nil {
		n.conn.Close()
		return fmt.Errorf("failed to send getheaders message: %v", err)
	}

	return nil
}

// handleMessages handles incoming messages from the peer
func (n *SPVNode) handleMessages() {
	for {
		msg, err := n.readMessage()
		if err != nil {
			if err == io.EOF {
				log.Printf("Connection closed by peer")
			} else {
				log.Printf("Error reading message: %v", err)
			}
			return
		}

		switch string(bytes.TrimRight(msg.Command[:], "\x00")) {
		case MsgVersion:
			if err := n.handleVersionMessage(msg.Payload); err != nil {
				log.Printf("Error handling version message: %v", err)
			}
		case MsgVerack:
			n.verackReceived <- struct{}{}
		case MsgHeaders:
			if err := n.handleHeadersMessage(msg.Payload); err != nil {
				log.Printf("Error handling headers message: %v", err)
			}
		case MsgBlock:
			if err := n.handleBlockMessage(msg.Payload); err != nil {
				log.Printf("Error handling block message: %v", err)
			}
		case MsgTx:
			if err := n.handleTxMessage(msg.Payload); err != nil {
				log.Printf("Error handling transaction message: %v", err)
			}
		case MsgInv:
			if err := n.handleInvMessage(msg.Payload); err != nil {
				log.Printf("Error handling inventory message: %v", err)
			}
		}
	}
}

// readMessage reads a complete message from the connection
func (n *SPVNode) readMessage() (*Message, error) {
	msg := &Message{}

	// Read magic number
	if _, err := io.ReadFull(n.conn, msg.Magic[:]); err != nil {
		return nil, err
	}

	// Read command
	if _, err := io.ReadFull(n.conn, msg.Command[:]); err != nil {
		return nil, err
	}

	// Read length
	if err := binary.Read(n.conn, binary.LittleEndian, &msg.Length); err != nil {
		return nil, err
	}

	// Read checksum
	if _, err := io.ReadFull(n.conn, msg.Checksum[:]); err != nil {
		return nil, err
	}

	// Read payload
	if msg.Length > 0 {
		msg.Payload = make([]byte, msg.Length)
		if _, err := io.ReadFull(n.conn, msg.Payload); err != nil {
			return nil, err
		}
	}

	return msg, nil
}

// sendMessage sends a message to the peer
func (n *SPVNode) sendMessage(command string, payload []byte) error {
	msg := &Message{
		Magic:   [4]byte{0xc0, 0xc0, 0xc0, 0xc0}, // Dogecoin magic number
		Length:  uint32(len(payload)),
		Payload: payload,
	}

	// Set command
	copy(msg.Command[:], command)

	// Calculate checksum
	hash := sha256.Sum256(payload)
	hash = sha256.Sum256(hash[:])
	copy(msg.Checksum[:], hash[:4])

	// Write message
	if err := binary.Write(n.conn, binary.LittleEndian, msg.Magic); err != nil {
		return err
	}
	if err := binary.Write(n.conn, binary.LittleEndian, msg.Command); err != nil {
		return err
	}
	if err := binary.Write(n.conn, binary.LittleEndian, msg.Length); err != nil {
		return err
	}
	if err := binary.Write(n.conn, binary.LittleEndian, msg.Checksum); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := n.conn.Write(payload); err != nil {
			return err
		}
	}

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

	return n.sendMessage(MsgVersion, payload)
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

	return n.sendMessage(MsgFilterLoad, payload)
}

// sendGetHeaders sends a getheaders message
func (n *SPVNode) sendGetHeaders() error {
	// Create getheaders message payload
	payload := make([]byte, 0)

	// Version (4 bytes)
	versionBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(versionBytes, ProtocolVersion)
	payload = append(payload, versionBytes...)

	// Hash count (varint)
	payload = append(payload, 0x01) // One hash

	// Block locator hashes (32 bytes)
	// Start with genesis block hash
	genesisHash := [32]byte{
		0x1a, 0x91, 0xe3, 0x4d, 0x1b, 0x4a, 0x4a, 0xba,
		0x1e, 0xca, 0x0b, 0xfb, 0xc1, 0x67, 0x86, 0x86,
		0x51, 0x31, 0xea, 0x5d, 0x5c, 0x51, 0x25, 0x25,
		0x67, 0x9c, 0xba, 0x4f, 0xcb, 0x1f, 0x01, 0x00,
	}
	payload = append(payload, genesisHash[:]...)

	// Stop hash (32 bytes) - all zeros to get all headers
	stopHash := [32]byte{}
	payload = append(payload, stopHash[:]...)

	log.Printf("Sending getheaders message with payload length: %d", len(payload))
	log.Printf("Requesting headers starting from genesis block: %x", genesisHash)
	return n.sendMessage(MsgGetHeaders, payload)
}

// handleVersionMessage handles a version message
func (n *SPVNode) handleVersionMessage(payload []byte) error {
	// Parse version message
	version := binary.LittleEndian.Uint32(payload[0:4])
	if version < ProtocolVersion {
		return fmt.Errorf("peer version %d is too old", version)
	}

	// Send verack
	return n.sendMessage(MsgVerack, nil)
}

// handleHeadersMessage handles a headers message
func (n *SPVNode) handleHeadersMessage(payload []byte) error {
	log.Printf("Received headers message with payload length: %d", len(payload))

	// Parse headers count (varint)
	reader := bytes.NewReader(payload)
	count, err := binary.ReadUvarint(reader)
	if err != nil {
		return fmt.Errorf("error reading headers count: %v", err)
	}
	log.Printf("Headers count: %d", count)

	// Parse each header
	for i := uint64(0); i < count; i++ {
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
			return fmt.Errorf("error reading header time: %v", err)
		}

		// Bits (4 bytes)
		if err := binary.Read(reader, binary.LittleEndian, &header.Bits); err != nil {
			return fmt.Errorf("error reading header bits: %v", err)
		}

		// Nonce (4 bytes)
		if err := binary.Read(reader, binary.LittleEndian, &header.Nonce); err != nil {
			return fmt.Errorf("error reading header nonce: %v", err)
		}

		// Set height based on previous block
		if i == 0 {
			// First header is at height 0
			header.Height = 0
		} else {
			// Find previous header and increment height
			for h, prevHeader := range n.headers {
				if bytes.Equal(prevHeader.PrevBlock[:], header.PrevBlock[:]) {
					header.Height = h + 1
					break
				}
			}
		}

		// Store header
		n.headers[header.Height] = header
		log.Printf("Stored header at height %d", header.Height)

		// Update current height if this is the highest header
		if header.Height > n.currentHeight {
			n.currentHeight = header.Height
			log.Printf("Updated current height to %d", n.currentHeight)
		}
	}

	return nil
}

// handleBlockMessage handles a block message
func (n *SPVNode) handleBlockMessage(payload []byte) error {
	// Parse and process block
	return nil
}

// handleTxMessage handles a transaction message
func (n *SPVNode) handleTxMessage(payload []byte) error {
	// Parse and process transaction
	return nil
}

// handleInvMessage handles an inventory message
func (n *SPVNode) handleInvMessage(payload []byte) error {
	// Parse inventory and request items
	return nil
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
	var maxHeight uint32
	for height := range n.headers {
		if height > maxHeight {
			maxHeight = height
		}
	}
	log.Printf("Current block height: %d", maxHeight)
	return int64(maxHeight), nil
}

// GetBlockTransactions returns transactions in a block
func (n *SPVNode) GetBlockTransactions(blockHash string) ([]Transaction, error) {
	if n.conn == nil {
		return nil, fmt.Errorf("not connected to peer")
	}

	log.Printf("Requesting transactions for block %s", blockHash)

	// Convert block hash to bytes
	hashBytes, err := hex.DecodeString(blockHash)
	if err != nil {
		return nil, fmt.Errorf("invalid block hash: %v", err)
	}
	log.Printf("Converted block hash to bytes: %x", hashBytes)

	// Create getdata message for the block
	payload := make([]byte, 37) // 1 byte for count + 36 bytes for inventory
	payload[0] = 1              // Count of inventory items
	payload[1] = 2              // MSG_BLOCK type
	copy(payload[2:], hashBytes)
	log.Printf("Created getdata message payload: %x", payload)

	// Send getdata message
	log.Printf("Sending getdata message...")
	if err := n.sendMessage(MsgGetData, payload); err != nil {
		return nil, fmt.Errorf("failed to send getdata message: %v", err)
	}
	log.Printf("Getdata message sent successfully")

	// Create a channel to receive the block message
	blockChan := make(chan *Block, 1)
	errChan := make(chan error, 1)

	// Start a goroutine to wait for the block message
	go func() {
		log.Printf("Starting goroutine to wait for block message...")
		// Set a timeout for receiving the block
		timeout := time.After(30 * time.Second)

		for {
			select {
			case <-timeout:
				log.Printf("Timeout waiting for block message")
				errChan <- fmt.Errorf("timeout waiting for block message")
				return
			default:
				log.Printf("Waiting for next message...")
				msg, err := n.readMessage()
				if err != nil {
					log.Printf("Error reading message: %v", err)
					errChan <- fmt.Errorf("error reading message: %v", err)
					return
				}

				command := string(bytes.TrimRight(msg.Command[:], "\x00"))
				log.Printf("Received message of type: %s", command)

				if command == MsgBlock {
					log.Printf("Received block message, parsing...")
					// Parse block message
					block, err := n.parseBlockMessage(msg.Payload)
					if err != nil {
						log.Printf("Error parsing block message: %v", err)
						errChan <- fmt.Errorf("error parsing block message: %v", err)
						return
					}

					// Verify block hash matches what we requested
					log.Printf("Verifying block hash...")
					headerBytes := block.Header.Serialize()
					hash1 := sha256.Sum256(headerBytes)
					hash2 := sha256.Sum256(hash1[:])
					if !bytes.Equal(hash2[:], hashBytes) {
						log.Printf("Block hash mismatch. Expected: %x, Got: %x", hashBytes, hash2[:])
						errChan <- fmt.Errorf("received block hash does not match requested hash")
						return
					}
					log.Printf("Block hash verified successfully")

					blockChan <- block
					return
				} else {
					log.Printf("Ignoring non-block message of type: %s", command)
				}
			}
		}
	}()

	// Wait for either the block or an error
	log.Printf("Waiting for block or error...")
	select {
	case block := <-blockChan:
		log.Printf("Received block, filtering transactions...")
		// Filter transactions that match our bloom filter
		var relevantTxs []Transaction
		for _, tx := range block.Tx {
			if n.ProcessTransaction(&tx) {
				log.Printf("Found relevant transaction: %s", tx.TxID)
				relevantTxs = append(relevantTxs, tx)
			}
		}
		log.Printf("Found %d relevant transactions", len(relevantTxs))
		return relevantTxs, nil
	case err := <-errChan:
		log.Printf("Received error: %v", err)
		return nil, err
	}
}

// parseBlockMessage parses a block message payload into a Block struct
func (n *SPVNode) parseBlockMessage(payload []byte) (*Block, error) {
	block := &Block{}
	reader := bytes.NewReader(payload)

	// Parse block header
	header := BlockHeader{}
	if err := binary.Read(reader, binary.LittleEndian, &header.Version); err != nil {
		return nil, err
	}
	if _, err := reader.Read(header.PrevBlock[:]); err != nil {
		return nil, err
	}
	if _, err := reader.Read(header.MerkleRoot[:]); err != nil {
		return nil, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &header.Time); err != nil {
		return nil, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &header.Bits); err != nil {
		return nil, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &header.Nonce); err != nil {
		return nil, err
	}
	block.Header = header

	// Parse transaction count (varint)
	txCount, err := binary.ReadUvarint(reader)
	if err != nil {
		return nil, err
	}

	// Parse transactions
	block.Tx = make([]Transaction, txCount)
	for i := uint64(0); i < txCount; i++ {
		tx, err := n.parseTransaction(reader)
		if err != nil {
			return nil, err
		}
		block.Tx[i] = *tx
	}

	return block, nil
}

// parseTransaction parses a transaction from a reader
func (n *SPVNode) parseTransaction(reader *bytes.Reader) (*Transaction, error) {
	tx := &Transaction{}

	// Parse version
	if err := binary.Read(reader, binary.LittleEndian, &tx.Version); err != nil {
		return nil, err
	}

	// Parse input count (varint)
	inputCount, err := binary.ReadUvarint(reader)
	if err != nil {
		return nil, err
	}

	// Parse inputs
	tx.Inputs = make([]TxInput, inputCount)
	for i := uint64(0); i < inputCount; i++ {
		input := TxInput{}
		if _, err := reader.Read(input.PreviousOutput.Hash[:]); err != nil {
			return nil, err
		}
		if err := binary.Read(reader, binary.LittleEndian, &input.PreviousOutput.Index); err != nil {
			return nil, err
		}
		scriptLen, err := binary.ReadUvarint(reader)
		if err != nil {
			return nil, err
		}
		input.ScriptSig = make([]byte, scriptLen)
		if _, err := reader.Read(input.ScriptSig); err != nil {
			return nil, err
		}
		if err := binary.Read(reader, binary.LittleEndian, &input.Sequence); err != nil {
			return nil, err
		}
		tx.Inputs[i] = input
	}

	// Parse output count (varint)
	outputCount, err := binary.ReadUvarint(reader)
	if err != nil {
		return nil, err
	}

	// Parse outputs
	tx.Outputs = make([]TxOutput, outputCount)
	for i := uint64(0); i < outputCount; i++ {
		output := TxOutput{}
		if err := binary.Read(reader, binary.LittleEndian, &output.Value); err != nil {
			return nil, err
		}
		scriptLen, err := binary.ReadUvarint(reader)
		if err != nil {
			return nil, err
		}
		output.ScriptPubKey = make([]byte, scriptLen)
		if _, err := reader.Read(output.ScriptPubKey); err != nil {
			return nil, err
		}
		tx.Outputs[i] = output
	}

	// Parse lock time
	if err := binary.Read(reader, binary.LittleEndian, &tx.LockTime); err != nil {
		return nil, err
	}

	// Calculate transaction ID
	txBytes := make([]byte, reader.Size())
	reader.Seek(0, io.SeekStart)
	reader.Read(txBytes)
	hash1 := sha256.Sum256(txBytes)
	hash2 := sha256.Sum256(hash1[:])
	tx.TxID = hex.EncodeToString(hash2[:])

	return tx, nil
}

// ProcessTransaction checks if a transaction is relevant to our watched addresses
func (n *SPVNode) ProcessTransaction(tx *Transaction) bool {
	log.Printf("Processing transaction %s", tx.TxID)
	for _, output := range tx.Outputs {
		// Extract addresses from output script
		addresses := extractAddressesFromScript(output.ScriptPubKey)
		for _, addr := range addresses {
			if n.watchAddresses[addr] {
				log.Printf("Found relevant transaction for watched address %s", addr)
				return true
			}
		}
	}
	return false
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

// isRelevant checks if a script hash matches the bloom filter
func (n *SPVNode) isRelevant(scriptHash []byte) bool {
	// TODO: Implement bloom filter checking
	return false
}

// extractAddresses extracts addresses from a script
func (n *SPVNode) extractAddresses(script []byte) []string {
	// TODO: Implement address extraction
	return []string{}
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
