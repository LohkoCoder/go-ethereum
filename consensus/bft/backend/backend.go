package backend

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/bft"
	"github.com/ethereum/go-ethereum/consensus/bft/core"
	snr "github.com/ethereum/go-ethereum/consensus/bft/signer"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/trie"
	lru "github.com/hashicorp/golang-lru"
)

const (
	// fetcherID is the ID indicates the block is from HotStuff engine
	fetcherID = "bft"
)

// HotStuff is the scalable bft consensus engine
type backend struct {
	config         *bft.Config
	db             ethdb.Database // Database to store and retrieve necessary information
	core           bft.CoreEngine
	signer         bft.Signer
	chain          consensus.ChainReader
	currentBlock   func() *types.Block
	getBlockByHash func(hash common.Hash) *types.Block
	hasBadBlock    func(hash common.Hash) bool
	logger         log.Logger

	recents        *lru.ARCCache // Snapshots for recent block to speed up reorgs
	recentMessages *lru.ARCCache // the cache of peer's messages
	knownMessages  *lru.ARCCache // the cache of self messages

	epochs              map[uint64]*Epoch // map epoch start height to epochs
	maxEpochStartHeight uint64

	// The channels for bft engine notifications
	sealMu            sync.Mutex
	commitCh          chan *types.Block
	proposedBlockHash common.Hash
	coreStarted       bool
	sigMu             sync.RWMutex // Protects the address fields
	consenMu          sync.Mutex   // Ensure a round can only start after the last one has finished
	coreMu            sync.RWMutex

	// event subscription for ChainHeadEvent event
	broadcaster consensus.Broadcaster

	eventMux *event.TypeMux

	proposals map[common.Address]bool // Current list of proposals we are pushing
}

func New(config *bft.Config, privateKey *ecdsa.PrivateKey, db ethdb.Database) consensus.BFT {
	recents, _ := lru.NewARC(inmemorySnapshots)
	recentMessages, _ := lru.NewARC(inmemoryPeers)
	knownMessages, _ := lru.NewARC(inmemoryMessages)

	signer := snr.NewSigner(privateKey)
	backend := &backend{
		config:         config,
		db:             db,
		logger:         log.New(),
		commitCh:       make(chan *types.Block, 1),
		coreStarted:    false,
		eventMux:       new(event.TypeMux),
		signer:         signer,
		recentMessages: recentMessages,
		knownMessages:  knownMessages,
		recents:        recents,
		proposals:      make(map[common.Address]bool),
	}

	backend.core = core.New(backend, config, signer)
	if err := backend.LoadEpoch(); err != nil {
		panic(fmt.Sprintf("load epoch failed, err: %v", err))
	}
	return backend
}

// Address implements bft.Backend.Address
func (s *backend) Address() common.Address {
	return s.signer.Address()
}

// EventMux implements bft.Backend.EventMux
func (s *backend) EventMux() *event.TypeMux {
	return s.eventMux
}

// Broadcast implements bft.Backend.Broadcast
func (s *backend) Broadcast(valSet bft.ValidatorSet, payload []byte) error {
	// send to others
	if err := s.Gossip(valSet, payload); err != nil {
		return err
	}
	// send to self
	msg := bft.MessageEvent{
		Payload: payload,
	}
	go s.EventMux().Post(msg)
	return nil
}

// Broadcast implements bft.Backend.Gossip
func (s *backend) Gossip(valSet bft.ValidatorSet, payload []byte) error {
	hash := bft.RLPHash(payload)
	s.knownMessages.Add(hash, true)

	targets := make(map[common.Address]bool)
	for _, val := range valSet.List() { // bft/validator/default.go - defaultValidator
		if val.Address() != s.Address() {
			targets[val.Address()] = true
		}
	}
	if s.broadcaster != nil && len(targets) > 0 {
		ps := s.broadcaster.FindPeers(targets)
		for addr, p := range ps {
			ms, ok := s.recentMessages.Get(addr)
			var m *lru.ARCCache
			if ok {
				m, _ = ms.(*lru.ARCCache)
				if _, k := m.Get(hash); k {
					// This peer had this event, skip it
					continue
				}
			} else {
				m, _ = lru.NewARC(inmemoryMessages)
			}

			m.Add(hash, true)
			s.recentMessages.Add(addr, m)
			go p.Send(bftMsg, payload)
		}
	}
	return nil
}

// Unicast implements bft.Backend.Unicast
func (s *backend) Unicast(valSet bft.ValidatorSet, payload []byte) error {
	msg := bft.MessageEvent{Payload: payload}
	leader := valSet.GetProposer()
	target := leader.Address()
	hash := bft.RLPHash(payload)
	s.knownMessages.Add(hash, true)

	// send to self
	if s.Address() == target {
		go s.EventMux().Post(msg)
		return nil
	}

	// send to other peer
	if s.broadcaster != nil {
		if p := s.broadcaster.FindPeer(target); p != nil {
			ms, ok := s.recentMessages.Get(target)
			var m *lru.ARCCache
			if ok {
				m, _ = ms.(*lru.ARCCache)
				if _, k := m.Get(hash); k {
					return nil
				}
			} else {
				m, _ = lru.NewARC(inmemoryMessages)
			}
			m.Add(hash, true)
			s.recentMessages.Add(target, m)
			go func() {
				s.logger.Info("unicast message")
				if err := p.Send(bftMsg, payload); err != nil {
					s.logger.Error("unicast message failed", "err", err)
				}
			}()
		}
	}
	return nil
}

// PreCommit implements bft.Backend.PreCommit
func (s *backend) PreCommit(proposal bft.Proposal, seals [][]byte) (bft.Proposal, error) {
	// Check if the proposal is a valid block
	block, ok := proposal.(*types.Block)
	if !ok {
		s.logger.Error("Invalid proposal, %v", proposal)
		return nil, errInvalidProposal
	}

	h := block.Header()
	// Append seals into extra-data
	if err := s.signer.SealAfterCommit(h, seals); err != nil {
		return nil, err
	}

	// update block's header
	block = block.WithSeal(h)

	return block, nil
}

func (s *backend) ForwardCommit(proposal bft.Proposal, extra []byte) (bft.Proposal, error) {
	block, ok := proposal.(*types.Block)
	if !ok {
		s.logger.Error("Invalid proposal, %v", proposal)
		return nil, errInvalidProposal
	}

	h := block.Header()
	h.Extra = extra
	block = block.WithSeal(h)

	return block, nil
}

func (s *backend) Commit(proposal bft.Proposal) error {
	// Check if the proposal is a valid block
	block, ok := proposal.(*types.Block)
	if !ok {
		s.logger.Error("Committed to miner worker", "proposal", "not block")
		return errInvalidProposal
	}
	
	s.logger.Info("Committed", "address", s.Address(), "hash", proposal.Hash(), "number", proposal.Number().Uint64())
	// - if the proposed and committed blocks are the same, send the proposed hash
	//   to commit channel, which is being watched inside the engine.Seal() function.
	// - otherwise, we try to insert the block.
	// -- if success, the ChainHeadEvent event will be broadcasted, try to build
	//    the next block and the previous Seal() will be stopped.
	// -- otherwise, a error will be returned and a round change event will be fired.
	if s.proposedBlockHash == block.Hash() {
		// feed block hash to Seal() and wait the Seal() result
		s.commitCh <- block
		return nil
	}

	if s.broadcaster != nil {
		s.broadcaster.Enqueue(fetcherID, block)
	}
	return nil
}

// Verify implements bft.Backend.Verify
func (s *backend) Verify(proposal bft.Proposal) (time.Duration, error) {
	// Check if the proposal is a valid block
	block := &types.Block{}
	block, ok := proposal.(*types.Block)
	if !ok {
		s.logger.Error("Invalid proposal, %v", proposal)
		return 0, errInvalidProposal
	}

	// check bad block
	if s.HasBadProposal(block.Hash()) {
		return 0, errBADProposal
	}

	// check block body
	txnHash := types.DeriveSha(block.Transactions(), trie.NewStackTrie(nil))
	uncleHash := types.CalcUncleHash(block.Uncles())
	if txnHash != block.Header().TxHash {
		return 0, errMismatchTxhashes
	}
	if uncleHash != nilUncleHash {
		return 0, errInvalidUncleHash
	}

	// verify the header of proposed block
	err := s.VerifyHeader(s.chain, block.Header(), false)
	if err == nil {
		return 0, nil
	} else if err == consensus.ErrFutureBlock {
		return time.Unix(int64(block.Header().Time), 0).Sub(now()), consensus.ErrFutureBlock
	}
	return 0, err
}

func (s *backend) VerifyUnsealedProposal(proposal bft.Proposal) (time.Duration, error) {
	// Check if the proposal is a valid block
	block := &types.Block{}
	block, ok := proposal.(*types.Block)
	if !ok {
		s.logger.Error("Invalid proposal, %v", proposal)
		return 0, errInvalidProposal
	}

	// check bad block
	if s.HasBadProposal(block.Hash()) {
		return 0, errBADProposal
	}

	// check block body
	txnHash := types.DeriveSha(block.Transactions(), trie.NewStackTrie(nil))
	uncleHash := types.CalcUncleHash(block.Uncles())
	if txnHash != block.Header().TxHash {
		return 0, errMismatchTxhashes
	}
	if uncleHash != nilUncleHash {
		return 0, errInvalidUncleHash
	}

	// verify the header of proposed block
	if err := s.VerifyHeader(s.chain, block.Header(), false); err == nil {
		return 0, nil
	} else if err == consensus.ErrFutureBlock {
		return time.Unix(int64(block.Header().Time), 0).Sub(now()), consensus.ErrFutureBlock
	} else {
		return 0, err
	}
}

func (s *backend) LastProposal() (bft.Proposal, common.Address) {
	if s.currentBlock == nil {
		return nil, common.Address{}
	}

	block := s.currentBlock()
	var proposer common.Address
	if block.Number().Cmp(common.Big0) > 0 {
		var err error
		proposer, err = s.Author(block.Header())
		if err != nil {
			s.logger.Error("Failed to get block proposer", "err", err)
			return nil, common.Address{}
		}
	}

	// Return header only block here since we don't need block body
	return block, proposer
}

func (s *backend) GetProposal(hash common.Hash) bft.Proposal {
	return s.getBlockByHash(hash)
}

// HasProposal implements bft.Backend.HashBlock
func (s *backend) HasProposal(hash common.Hash, number *big.Int) bool {
	return s.chain.GetHeader(hash, number.Uint64()) != nil
}

// GetSpeaker implements bft.Backend.GetProposer
func (s *backend) GetProposer(number uint64) common.Address {
	if header := s.chain.GetHeaderByNumber(number); header != nil {
		a, _ := s.Author(header)
		return a
	}
	return common.Address{}
}

func (s *backend) HasBadProposal(hash common.Hash) bool {
	if s.hasBadBlock == nil {
		return false
	}
	return s.hasBadBlock(hash)
}
