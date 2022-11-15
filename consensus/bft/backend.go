package bft

import (
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
)

// Backend provides application specific functions for Istanbul core
type Backend interface {
	// Address returns the owner's address
	Address() common.Address

	// Validators returns current epoch participants
	Validators(height uint64) ValidatorSet

	// EventMux returns the event mux in backend
	EventMux() *event.TypeMux

	// Broadcast sends a message to all validators (include self)
	Broadcast(valSet ValidatorSet, payload []byte) error

	// Gossip sends a message to all validators (exclude self)
	Gossip(valSet ValidatorSet, payload []byte) error

	// Unicast send a message to single peer
	Unicast(valSet ValidatorSet, payload []byte) error

	// PreCommit write seal to header and assemble new qc
	PreCommit(proposal Proposal, seals [][]byte) (Proposal, error)

	// ForwardCommit assemble unsealed block and sealed extra into an new full block
	ForwardCommit(proposal Proposal, extra []byte) (Proposal, error)

	// Commit delivers an approved proposal to backend.
	// The delivered proposal will be put into blockchain.
	Commit(proposal Proposal) error

	// Verify verifies the proposal. If a consensus.ErrFutureBlock error is returned,
	// the time difference of the proposal and current time is also returned.
	Verify(Proposal) (time.Duration, error)

	// Verify verifies the proposal. If a consensus.ErrFutureBlock error is returned,
	// the time difference of the proposal and current time is also returned.
	VerifyUnsealedProposal(Proposal) (time.Duration, error)

	// LastProposal retrieves latest committed proposal and the address of proposer
	LastProposal() (Proposal, common.Address)

	// HasBadBlock returns whether the block with the hash is a bad block
	HasBadProposal(hash common.Hash) bool

	// ValidateBlock execute block which contained in prepare message, and validate block state
	ValidateBlock(block *types.Block) error

	Close() error
}

type CoreEngine interface {
	Start(chain consensus.ChainReader) error

	Stop() error

	// IsProposer return true if self address equal leader/proposer address in current round/height
	IsProposer() bool

	// verify if a hash is the same as the proposed block in the current pending request
	//
	// this is useful when the engine is currently the speaker
	//
	// pending request is populated right at the request stage so this would give us the earliest verification
	// to avoid any race condition of coming propagated blocks
	IsCurrentProposal(blockHash common.Hash) bool
}

type BftProtocol string

const (
	BFT_PROTOCOL_BASIC        BftProtocol = "basic"
	BFT_PROTOCOL_EVENT_DRIVEN BftProtocol = "event_driven"
)
