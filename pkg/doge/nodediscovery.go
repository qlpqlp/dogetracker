package doge

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// NodeDiscovery handles peer discovery and management
type NodeDiscovery struct {
	peers       []string
	activePeers map[string]net.Conn
	peersMutex  sync.RWMutex
	logger      *log.Logger
	stopChan    chan struct{}
}

// NewNodeDiscovery creates a new NodeDiscovery instance
func NewNodeDiscovery() *NodeDiscovery {
	return &NodeDiscovery{
		peers:       make([]string, 0),
		activePeers: make(map[string]net.Conn),
		logger:      log.New(log.Writer(), "NodeDiscovery: ", log.LstdFlags),
		stopChan:    make(chan struct{}),
	}
}

// DiscoverPeers discovers new peers using DNS seeds
func (nd *NodeDiscovery) DiscoverPeers() error {
	nd.logger.Printf("Discovering peers...")

	// Dogecoin DNS seeds
	seeds := []string{
		"seed.dogecoin.com",
		"seed.multidoge.org",
		"seed.dogechain.info",
	}

	// Try each DNS seed
	for _, seed := range seeds {
		ips, err := net.LookupIP(seed)
		if err != nil {
			nd.logger.Printf("Error looking up seed %s: %v", seed, err)
			continue
		}

		for _, ip := range ips {
			peer := fmt.Sprintf("%s:22556", ip.String()) // Dogecoin default port
			nd.peersMutex.Lock()
			nd.peers = append(nd.peers, peer)
			nd.peersMutex.Unlock()
			nd.logger.Printf("Discovered peer from DNS seed: %s", peer)
		}
	}

	return nil
}

// ConnectToPeer attempts to connect to a peer
func (nd *NodeDiscovery) ConnectToPeer(peer string) (net.Conn, error) {
	conn, err := net.Dial("tcp", peer)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to peer %s: %v", peer, err)
	}

	nd.peersMutex.Lock()
	nd.activePeers[peer] = conn
	nd.peersMutex.Unlock()

	nd.logger.Printf("Connected to peer %s", peer)
	return conn, nil
}

// GetPeers returns a list of discovered peers
func (nd *NodeDiscovery) GetPeers() []string {
	nd.peersMutex.RLock()
	defer nd.peersMutex.RUnlock()
	return nd.peers
}

// GetActivePeers returns a list of currently connected peers
func (nd *NodeDiscovery) GetActivePeers() map[string]net.Conn {
	nd.peersMutex.RLock()
	defer nd.peersMutex.RUnlock()
	return nd.activePeers
}

// Start starts the node discovery process
func (nd *NodeDiscovery) Start() {
	go func() {
		for {
			select {
			case <-nd.stopChan:
				return
			default:
				// Discover new peers if needed
				if len(nd.activePeers) < 6 { // Maintain at least 6 connections
					if err := nd.DiscoverPeers(); err != nil {
						nd.logger.Printf("Error discovering peers: %v", err)
					}
				}

				// Check active connections
				for peer, conn := range nd.activePeers {
					// Send ping to check connection
					pingMsg := &Message{
						Magic:    0xc0c0c0c0,
						Length:   8,
						Checksum: [4]byte{},
						Payload:  make([]byte, 8),
					}
					copy(pingMsg.Command[:], MsgPing)

					if err := nd.sendMessageToPeer(conn, pingMsg); err != nil {
						nd.logger.Printf("Connection to peer %s lost: %v", peer, err)
						conn.Close()
						delete(nd.activePeers, peer)
					}
				}

				time.Sleep(30 * time.Second)
			}
		}
	}()
}

// Stop stops the node discovery process
func (nd *NodeDiscovery) Stop() {
	close(nd.stopChan)
	for _, conn := range nd.activePeers {
		conn.Close()
	}
}

// sendMessageToPeer sends a message to a specific peer
func (nd *NodeDiscovery) sendMessageToPeer(conn net.Conn, msg *Message) error {
	// Serialize message
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.LittleEndian, msg.Magic); err != nil {
		return err
	}
	if _, err := buf.Write(msg.Command[:]); err != nil {
		return err
	}
	if err := binary.Write(buf, binary.LittleEndian, msg.Length); err != nil {
		return err
	}
	if _, err := buf.Write(msg.Checksum[:]); err != nil {
		return err
	}
	if _, err := buf.Write(msg.Payload); err != nil {
		return err
	}

	// Send message
	_, err := conn.Write(buf.Bytes())
	return err
}
