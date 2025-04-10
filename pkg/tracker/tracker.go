package tracker

/*
 * This package is based on the Dogecoin Foundation's DogeWalker project
 * (github.com/dogeorg/dogewalker) and has been modified to create
 * a transaction tracking system for Dogecoin addresses.
 */

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"log"
	"time"

	"github.com/qlpqlp/dogetracker/pkg/doge"
	"github.com/qlpqlp/dogetracker/pkg/spec"
)

const (
	RETRY_DELAY = 5 * time.Second // for RPC and Database errors.
)

// The type of the DogeTracker output channel; either block or undo
type BlockOrUndo struct {
	Block *ChainBlock     // either the next block in the chain
	Undo  *UndoForkBlocks // or an undo event (roll back blocks on a fork)
}

// NextBlock represents the next block in the blockchain.
type ChainBlock struct {
	Hash   string
	Height int64
	Block  doge.Block
}

// UndoForkBlocks represents a Fork in the Blockchain: blocks to undo on the off-chain fork
type UndoForkBlocks struct {
	LastValidHeight int64         // undo all blocks greater than this height
	ResumeFromBlock string        // hash of last valid on-chain block (to resume on restart)
	BlockHashes     []string      // hashes of blocks to be undone
	FullBlocks      []*ChainBlock // present if FullUndoBlocks is true in TrackerOptions
}

// Configuraton for WalkTheDoge.
type TrackerOptions struct {
	Chain           *doge.ChainParams // chain parameters, e.g. doge.DogeMainNetChain
	ResumeFromBlock string            // last processed block hash to begin walking from (hex)
	Client          spec.Blockchain   // from NewCoreRPCClient()
	TipChanged      chan string       // from TipChaser()
	FullUndoBlocks  bool              // fully decode blocks in UndoForkBlocks (or just hash and height)
}

// Private WalkTheDoge internal state.
type DogeTracker struct {
	output         chan BlockOrUndo
	client         spec.Blockchain
	chain          *doge.ChainParams
	tipChanged     chan string     // receive from TipChaser.
	stop           <-chan struct{} // ctx.Done() channel.
	stopping       bool            // set to exit the main loop.
	fullUndoBlocks bool            // fully decode blocks in UndoForkBlocks
}

// Tracker handles block processing and transaction tracking
type Tracker struct {
	db      *sql.DB
	spvNode *doge.SPVNode
}

// NewTracker creates a new tracker
func NewTracker(db *sql.DB, spvNode *doge.SPVNode) *Tracker {
	return &Tracker{
		db:      db,
		spvNode: spvNode,
	}
}

/*
 * WalkTheDoge walks the blockchain, keeping up with the Tip (Best Block)
 *
 * It outputs decoded blocks to the returned 'blocks' channel.
 *
 * If there's a reorganisation (fork), it will walk backwards to the
 * fork-point, building a list of blocks to undo, until it finds a block
 * that's still on the main chain. Then it will output UndoForkBlocks
 * to allow you to undo any data in your systems related to those blocks.
 *
 * Note: when you undo blocks, you will need to restore any UTXOs spent
 * by those blocks (spending blocks don't contain enough information to
 * re-create the spent UTXOs, so you must keep them for e.g. 100 blocks)
 *
 * fullUndoBlocks: pass fully decoded blocks to the UndoForkBlocks callback.
 * Useful if you want to manually undo each transaction, rather than undoing
 * everything above `LastValidHeight` by tagging data with block-heights.
 */
func WalkTheDoge(ctx context.Context, opts TrackerOptions) (blocks chan BlockOrUndo, err error) {
	c := DogeTracker{
		// The larger this channel is, the more blocks we can decode-ahead.
		output:         make(chan BlockOrUndo, 100),
		client:         opts.Client,
		chain:          opts.Chain,
		tipChanged:     opts.TipChanged,
		stop:           ctx.Done(),
		fullUndoBlocks: opts.FullUndoBlocks,
	}
	err = c.verifyChain()
	if err != nil {
		return nil, err // bad config
	}
	go func() {
		// Recover from panic used to stop the service.
		// We use this to avoid returning a 'stopping' bool from every single function.
		defer func() {
			if r := recover(); r != nil {
				log.Println("DogeTracker: panic received:", r)
			}
		}()
		resumeFromBlock := opts.ResumeFromBlock
		if resumeFromBlock == "" {
			resumeFromBlock = c.fetchBlockHash(1)
			log.Printf("DogeTracker: no resume-from block hash: starting from origin block: %v", resumeFromBlock)
		}
		for {
			// Get the last-processed block header (restart point)
			head := c.fetchBlockHeader(resumeFromBlock)
			nextBlockHash := head.NextBlockHash // can be ""
			if head.Confirmations == -1 {
				// No longer on-chain, start with a rollback.
				undo, nextBlock := c.undoBlocks(head)
				c.output <- BlockOrUndo{Undo: undo}
				nextBlockHash = nextBlock // can be ""
			}

			// Follow the Blockchain to the Tip.
			lastProcessed := c.followTheChain(nextBlockHash)
			if lastProcessed != "" {
				resumeFromBlock = lastProcessed
			}

			// Wait for Core to signal a new Best Block (new block mined)
			// or a Command to arrive.
			select {
			case <-c.stop:
				log.Println("DogeTracker: received stop signal")
				c.stopping = true
				return
			case <-c.tipChanged:
				log.Println("DogeTracker: received new block signal")
			}
		}
	}()
	return c.output, nil
}

func (c *DogeTracker) verifyChain() error {
	genesisHash := c.fetchBlockHash(0)
	if genesisHash != c.chain.GenesisBlock {
		return fmt.Errorf("WRONG CHAIN! Expected chain '%s' but Core Node does not have a matching Genesis Block hash, it has %s", c.chain.ChainName, genesisHash)
	}
	return nil
}

func (c *DogeTracker) followTheChain(nextBlockHash string) (lastProcessed string) {
	// Follow the chain forwards.
	// If we encounter a fork, generate an Undo.
	for nextBlockHash != "" {
		head := c.fetchBlockHeader(nextBlockHash)
		if head.Confirmations != -1 {
			// This block is still on-chain.
			// Output the decoded block.
			blockData := c.fetchBlockData(head.Hash)

			// Log block data details
			log.Printf("Block data length: %d bytes", len(blockData))
			if len(blockData) > 0 {
				log.Printf("First 32 bytes of block data: %x", blockData[:32])
			}

			block := &ChainBlock{
				Hash:   head.Hash,
				Height: head.Height,
			}
			decodedBlock, err := doge.DecodeBlock(blockData)
			if err != nil {
				log.Printf("Error decoding block: %v", err)
				log.Printf("Block version: %x", binary.LittleEndian.Uint32(blockData[:4]))
				continue
			}
			block.Block = *decodedBlock
			c.output <- BlockOrUndo{Block: block}
			lastProcessed = block.Hash
			nextBlockHash = head.NextBlockHash
			continue
		}

		// This block is no longer on-chain.
		// Roll back until we find a block that is on-chain.
		undo, nextBlock := c.undoBlocks(head)
		c.output <- BlockOrUndo{Undo: undo}
		lastProcessed = undo.ResumeFromBlock
		nextBlockHash = nextBlock
		c.checkShutdown() // loops must check for shutdown.
	}
	return
}

func (c *DogeTracker) undoBlocks(head spec.BlockHeader) (undo *UndoForkBlocks, nextBlockHash string) {
	// Walk backwards along the chain (in Core) to find an on-chain block.
	undo = &UndoForkBlocks{}
	for {
		// Accumulate undo info.
		undo.BlockHashes = append(undo.BlockHashes, head.Hash)
		if c.fullUndoBlocks {
			blockData := c.fetchBlockData(head.Hash)
			undo.FullBlocks = append(undo.FullBlocks, &ChainBlock{
				Hash:   head.Hash,
				Height: head.Height,
			})
			decodedBlock, err := doge.DecodeBlock(blockData)
			if err != nil {
				log.Printf("Error decoding block for undo: %v", err)
				continue
			}
			undo.FullBlocks[len(undo.FullBlocks)-1].Block = *decodedBlock
		}
		// Fetch the block header for the previous block.
		head = c.fetchBlockHeader(head.PreviousBlockHash)
		if head.Confirmations == -1 {
			// This block is no longer on-chain; keep walking backwards.
			c.checkShutdown() // loops must check for shutdown.
			continue
		} else {
			// Found an on-chain block: stop rolling back.
			undo.LastValidHeight = head.Height
			undo.ResumeFromBlock = head.Hash
			return undo, head.NextBlockHash
		}
	}
}

func (c *DogeTracker) fetchBlockData(blockHash string) []byte {
	for {
		hex, err := c.client.GetBlock(blockHash)
		if err != nil {
			log.Println("ChainTracker: error retrieving block (will retry):", err)
			c.sleepForRetry(0)
		} else {
			bytes, err := doge.HexDecode(hex)
			if err != nil {
				log.Println("ChainTracker: invalid block hex (will retry):", err)
				c.sleepForRetry(0)
			}
			return bytes
		}
	}
}

func (c *DogeTracker) fetchBlockHeader(blockHash string) spec.BlockHeader {
	for {
		block, err := c.client.GetBlockHeader(blockHash)
		if err != nil {
			log.Println("ChainTracker: error retrieving block header (will retry):", err)
			c.sleepForRetry(0)
		} else {
			return block
		}
	}
}

func (c *DogeTracker) fetchBlockHash(height int64) string {
	for {
		hash, err := c.client.GetBlockHash(height)
		if err != nil {
			log.Println("ChainTracker: error retrieving block hash (will retry):", err)
			c.sleepForRetry(0)
		} else {
			return hash
		}
	}
}

// func (c *DogeTracker) fetchBlockCount() int64 {
// 	for {
// 		count, err := c.client.GetBlockCount()
// 		if err != nil {
// 			log.Println("ChainTracker: error retrieving block count (will retry):", err)
// 			c.sleepForRetry(0)
// 		} else {
// 			return count
// 		}
// 	}
// }

func (c *DogeTracker) sleepForRetry(delay time.Duration) {
	if delay == 0 {
		delay = RETRY_DELAY
	}
	select {
	case <-c.stop:
		log.Println("ChainTracker: received stop signal")
		c.stopping = true
		panic("stopped") // caught in `Run` method.
	case <-time.After(delay):
		return
	}
}

func (c *DogeTracker) checkShutdown() {
	select {
	case <-c.stop:
		log.Println("ChainTracker: received stop signal")
		c.stopping = true
		panic("stopped") // caught in `Run` method.
	default:
		return
	}
}

// chainFromName returns ChainParams for: 'main', 'test', 'regtest'.
func ChainFromName(chainName string) (*doge.ChainParams, error) {
	switch chainName {
	case "main":
		return &doge.MainNetParams, nil
	case "test":
		return &doge.TestNetParams, nil
	case "regtest":
		return &doge.RegTestParams, nil
	default:
		return &doge.ChainParams{}, fmt.Errorf("unknown chain: %v", chainName)
	}
}

func (c *DogeTracker) processBlock(block *ChainBlock) {
	log.Printf("Starting to process block %d with %d transactions", block.Height, len(block.Block.Tx))
	for i, tx := range block.Block.Tx {
		log.Printf("Processing %d/%d: %s", i+1, len(block.Block.Tx), tx.TxID)

		// Check if this is a coinbase transaction
		isCoinbase := len(tx.Inputs) > 0 && len(tx.Inputs[0].PreviousOutput.Hash) == 0
		if isCoinbase {
			log.Printf("Transaction %s is a coinbase transaction", tx.TxID)
			continue
		}

		// Process inputs
		for _, input := range tx.Inputs {
			// Skip coinbase transactions
			if len(input.PreviousOutput.Hash) == 0 {
				continue
			}
			// Process input
			log.Printf("Processing input from transaction %x", input.PreviousOutput.Hash)
		}

		// Process outputs
		for i, output := range tx.Outputs {
			// Process output
			log.Printf("Processing output %d with value %d", i, output.Value)
		}
	}
}

// DecodeVarInt decodes a variable-length integer from the input bytes
// Returns the decoded value and the number of bytes read
func DecodeVarInt(data []byte) (uint64, int) {
	if len(data) == 0 {
		return 0, 0
	}

	// Read the first byte to determine the format
	firstByte := data[0]
	if firstByte < 0xfd {
		// Single byte
		return uint64(firstByte), 1
	} else if firstByte == 0xfd {
		// 2 bytes
		if len(data) < 3 {
			return 0, 0
		}
		return uint64(data[1]) | uint64(data[2])<<8, 3
	} else if firstByte == 0xfe {
		// 4 bytes
		if len(data) < 5 {
			return 0, 0
		}
		return uint64(data[1]) | uint64(data[2])<<8 | uint64(data[3])<<16 | uint64(data[4])<<24, 5
	} else {
		// 8 bytes
		if len(data) < 9 {
			return 0, 0
		}
		return uint64(data[1]) | uint64(data[2])<<8 | uint64(data[3])<<16 | uint64(data[4])<<24 |
			uint64(data[5])<<32 | uint64(data[6])<<40 | uint64(data[7])<<48 | uint64(data[8])<<56, 9
	}
}

// storeTransaction stores a transaction in the database
func (t *Tracker) storeTransaction(tx *doge.Transaction, addresses []string) error {
	// Start transaction
	dbTx, err := t.db.Begin()
	if err != nil {
		return err
	}
	defer dbTx.Rollback()

	// Insert transaction
	_, err = dbTx.Exec(`
		INSERT INTO transactions (txid, block_height, timestamp)
		VALUES ($1, $2, $3)
		ON CONFLICT (txid) DO NOTHING
	`, tx.TxID, 0, time.Now()) // TODO: Get block height from somewhere
	if err != nil {
		return err
	}

	// Insert transaction outputs
	for _, output := range tx.Outputs {
		for _, addr := range output.Addresses {
			_, err = dbTx.Exec(`
				INSERT INTO transaction_outputs (txid, address, value)
				VALUES ($1, $2, $3)
			`, tx.TxID, addr, output.Value)
			if err != nil {
				return err
			}
		}
	}

	// Commit transaction
	return dbTx.Commit()
}

// ProcessBlocks processes blocks from the blockchain
func (t *Tracker) ProcessBlocks(ctx context.Context, startBlock int64) error {
	// Get current block height
	currentHeight, err := t.spvNode.GetBlockCount()
	if err != nil {
		return fmt.Errorf("error getting block count: %v", err)
	}

	// Process blocks from startBlock to currentHeight
	for height := startBlock; height <= currentHeight; height++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Get block hash
			blockHash, err := t.spvNode.GetBlockHash(height)
			if err != nil {
				return fmt.Errorf("error getting block hash at height %d: %v", height, err)
			}

			// Get block transactions
			transactions, err := t.spvNode.GetBlockTransactions(blockHash)
			if err != nil {
				return fmt.Errorf("error getting transactions for block %s: %v", blockHash, err)
			}

			// Process each transaction
			for _, tx := range transactions {
				// Check if transaction is relevant to watched addresses
				if t.spvNode.ProcessTransaction(&tx) {
					// Store transaction in database
					err = t.storeTransaction(&tx, []string{blockHash})
					if err != nil {
						return fmt.Errorf("error storing transaction %s: %v", tx.TxID, err)
					}
				}
			}
		}
	}

	return nil
}
