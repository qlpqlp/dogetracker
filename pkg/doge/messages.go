package doge

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

const (
	// Message types
	MsgVersion    = "version"
	MsgVerack     = "verack"
	MsgHeaders    = "headers"
	MsgGetHeaders = "getheaders"
	MsgGetData    = "getdata"
	MsgBlock      = "block"
	MsgTx         = "tx"
	MsgInv        = "inv"
	MsgFilterLoad = "filterload"
	MsgPing       = "ping"
	MsgPong       = "pong"
)

// Message represents a Dogecoin protocol message
type Message struct {
	Magic    uint32
	Command  [12]byte
	Length   uint32
	Checksum [4]byte
	Payload  []byte
}

// VersionMessage represents a version message
type VersionMessage struct {
	Version     int32
	Services    uint64
	Timestamp   int64
	AddrRecv    NetAddress
	AddrFrom    NetAddress
	Nonce       uint64
	UserAgent   string
	StartHeight int32
	Relay       bool
}

// NetAddress represents a network address
type NetAddress struct {
	Services uint64
	IP       [16]byte
	Port     uint16
}

// readMessage reads a message from the connection
func (n *SPVNode) readMessage() (*Message, error) {
	n.logger.Printf("Reading message header...")
	// Read message header
	header := make([]byte, 24)
	if _, err := io.ReadFull(n.conn, header); err != nil {
		n.logger.Printf("Error reading message header: %v", err)
		return nil, err
	}
	n.logger.Printf("Read message header: %x", header)

	// Parse header
	msg := &Message{}
	msg.Magic = binary.LittleEndian.Uint32(header[0:4])
	copy(msg.Command[:], header[4:16])
	msg.Length = binary.LittleEndian.Uint32(header[16:20])
	copy(msg.Checksum[:], header[20:24])

	n.logger.Printf("Parsed message header: magic=0x%x, command=%s, length=%d",
		msg.Magic, string(bytes.TrimRight(msg.Command[:], "\x00")), msg.Length)

	// Read payload
	if msg.Length > 0 {
		n.logger.Printf("Reading message payload of length %d...", msg.Length)
		msg.Payload = make([]byte, msg.Length)
		if _, err := io.ReadFull(n.conn, msg.Payload); err != nil {
			n.logger.Printf("Error reading message payload: %v", err)
			return nil, err
		}
		n.logger.Printf("Read message payload: %x", msg.Payload)
	}

	return msg, nil
}

// sendMessage sends a message to the connection
func (n *SPVNode) sendMessage(msg *Message) error {
	buf := new(bytes.Buffer)

	// Write magic
	binary.Write(buf, binary.LittleEndian, msg.Magic)

	// Write command
	buf.Write(msg.Command[:])

	// Write length
	binary.Write(buf, binary.LittleEndian, msg.Length)

	// Write checksum
	buf.Write(msg.Checksum[:])

	// Write payload
	if msg.Length > 0 {
		buf.Write(msg.Payload)
	}

	_, err := n.conn.Write(buf.Bytes())
	return err
}

// sendVersionMessage sends a version message
func (n *SPVNode) sendVersionMessage() error {
	n.logger.Printf("Sending version message...")
	msg := &VersionMessage{
		Version:     70015,
		Services:    0,
		Timestamp:   time.Now().Unix(),
		UserAgent:   "/dogetracker:0.1.0/",
		StartHeight: 0,
		Relay:       true,
	}

	// Serialize message
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, msg.Version)
	binary.Write(buf, binary.LittleEndian, msg.Services)
	binary.Write(buf, binary.LittleEndian, msg.Timestamp)
	binary.Write(buf, binary.LittleEndian, msg.AddrRecv)
	binary.Write(buf, binary.LittleEndian, msg.AddrFrom)
	binary.Write(buf, binary.LittleEndian, msg.Nonce)
	binary.Write(buf, binary.LittleEndian, uint8(len(msg.UserAgent)))
	buf.WriteString(msg.UserAgent)
	binary.Write(buf, binary.LittleEndian, msg.StartHeight)
	binary.Write(buf, binary.LittleEndian, msg.Relay)

	// Create message
	payload := buf.Bytes()
	checksum := doubleSha256(payload)[:4]
	var checksumArray [4]byte
	copy(checksumArray[:], checksum)

	message := &Message{
		Magic:    0xc0c0c0c0, // Dogecoin magic number
		Length:   uint32(len(payload)),
		Checksum: checksumArray,
		Payload:  payload,
	}
	copy(message.Command[:], MsgVersion)

	n.logger.Printf("Sending version message with payload length: %d", len(payload))
	return n.sendMessage(message)
}

// sendVerackMessage sends a verack message
func (n *SPVNode) sendVerackMessage() error {
	message := &Message{
		Magic:    0xc0c0c0c0,
		Length:   0,
		Checksum: [4]byte{},
	}
	copy(message.Command[:], MsgVerack)

	return n.sendMessage(message)
}

// doubleSha256 calculates double SHA-256 hash
func doubleSha256(data []byte) []byte {
	hash := sha256.Sum256(data)
	hash = sha256.Sum256(hash[:])
	return hash[:]
}

// handleVersionMessage handles a version message
func (n *SPVNode) handleVersionMessage(payload []byte) error {
	n.logger.Printf("Handling version message with payload length: %d", len(payload))
	reader := bytes.NewReader(payload)

	// Parse version message fields
	var version int32
	if err := binary.Read(reader, binary.LittleEndian, &version); err != nil {
		return fmt.Errorf("error reading version: %v", err)
	}

	var services uint64
	if err := binary.Read(reader, binary.LittleEndian, &services); err != nil {
		return fmt.Errorf("error reading services: %v", err)
	}

	var timestamp int64
	if err := binary.Read(reader, binary.LittleEndian, &timestamp); err != nil {
		return fmt.Errorf("error reading timestamp: %v", err)
	}

	// Skip addr_recv and addr_from for now
	reader.Seek(52, io.SeekCurrent)

	var nonce uint64
	if err := binary.Read(reader, binary.LittleEndian, &nonce); err != nil {
		return fmt.Errorf("error reading nonce: %v", err)
	}

	// Read user agent length
	userAgentLen, err := binary.ReadUvarint(reader)
	if err != nil {
		return fmt.Errorf("error reading user agent length: %v", err)
	}

	// Read user agent
	userAgent := make([]byte, userAgentLen)
	if _, err := reader.Read(userAgent); err != nil {
		return fmt.Errorf("error reading user agent: %v", err)
	}

	var startHeight int32
	if err := binary.Read(reader, binary.LittleEndian, &startHeight); err != nil {
		return fmt.Errorf("error reading start height: %v", err)
	}

	var relay bool
	if err := binary.Read(reader, binary.LittleEndian, &relay); err != nil {
		return fmt.Errorf("error reading relay: %v", err)
	}

	n.logger.Printf("Received version message: version=%d, services=%d, userAgent=%s, startHeight=%d",
		version, services, string(userAgent), startHeight)

	// Send verack message
	n.logger.Printf("Sending verack message...")
	if err := n.sendVerackMessage(); err != nil {
		return fmt.Errorf("error sending verack: %v", err)
	}
	n.logger.Printf("Sent verack message")

	// Signal that we've received version and sent verack
	select {
	case n.verackReceived <- struct{}{}:
		n.logger.Printf("Sent version handshake signal")
	default:
		n.logger.Printf("No one waiting for version handshake")
	}

	return nil
}
