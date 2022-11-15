package core

import (
	"math/big"

	"github.com/ethereum/go-ethereum/consensus/bft"
	"github.com/ethereum/go-ethereum/consensus/bft/message_set"
)

type roundState struct {
	vs bft.ValidatorSet

	round  *big.Int
	height *big.Int
	state  State

	pendingRequest *bft.Request // leader's pending request
	proposal       bft.Proposal // Address's prepare proposal
	proposalLocked bool

	// o(4n)
	newViews       *message_set.MessageSet
	prepareVotes   *message_set.MessageSet
	preCommitVotes *message_set.MessageSet
	commitVotes    *message_set.MessageSet

	highQC      *bft.QuorumCert // leader highQC
	prepareQC   *bft.QuorumCert // prepareQC for repo and leader
	lockedQC    *bft.QuorumCert // lockedQC for repo and pre-committedQC for leader
	committedQC *bft.QuorumCert // committedQC for repo and leader
}

// newRoundState creates a new roundState instance with the given view and validatorSet
func newRoundState(view *bft.View, validatorSet bft.ValidatorSet, prepareQC *bft.QuorumCert) *roundState {
	rs := &roundState{
		vs:             validatorSet,
		round:          view.Round,
		height:         view.Height,
		state:          StateAcceptRequest,
		newViews:       message_set.NewMessageSet(validatorSet),
		prepareVotes:   message_set.NewMessageSet(validatorSet),
		preCommitVotes: message_set.NewMessageSet(validatorSet),
		commitVotes:    message_set.NewMessageSet(validatorSet),
	}
	if prepareQC != nil {
		rs.prepareQC = prepareQC.Copy()
		rs.lockedQC = prepareQC.Copy()
		rs.committedQC = prepareQC.Copy()
	}
	return rs
}

func (s *roundState) Height() *big.Int {
	return s.height
}

func (s *roundState) Round() *big.Int {
	return s.round
}

func (s *roundState) View() *bft.View {
	return &bft.View{
		Round:  s.round,
		Height: s.height,
	}
}

func (s *roundState) SetState(state State) {
	s.state = state
}

func (s *roundState) State() State {
	return s.state
}

func (s *roundState) SetProposal(proposal bft.Proposal) {
	s.proposal = proposal
}

func (s *roundState) Proposal() bft.Proposal {
	return s.proposal
}

func (s *roundState) LockProposal() {
	if s.proposal != nil && !s.proposalLocked {
		s.proposalLocked = true
	}
}

func (s *roundState) UnLockProposal() {
	if s.proposal != nil && s.proposalLocked {
		s.proposalLocked = false
		s.proposal = nil
	}
}

func (s *roundState) IsProposalLocked() bool {
	return s.proposalLocked
}

func (s *roundState) LastLockedProposal() (bool, bft.Proposal) {
	return s.proposalLocked, s.proposal
}

func (s *roundState) SetPendingRequest(req *bft.Request) {
	s.pendingRequest = req
}

func (s *roundState) PendingRequest() *bft.Request {
	return s.pendingRequest
}

func (s *roundState) Vote() *Vote {
	if s.proposal == nil || s.proposal.Hash() == EmptyHash {
		return nil
	}

	return &Vote{
		View: &bft.View{
			Round:  new(big.Int).Set(s.round),
			Height: new(big.Int).Set(s.height),
		},
		Digest: s.proposal.Hash(),
	}
}

// AddNewViews all valid Message, and invalid Message would be ignore
func (s *roundState) AddNewViews(msg *bft.Message) error {
	return s.newViews.Add(msg)
}

func (s *roundState) NewViewSize() int {
	return s.newViews.Size()
}

func (s *roundState) NewViews() []*bft.Message {
	return s.newViews.Values()
}

func (s *roundState) AddPrepareVote(msg *bft.Message) error {
	return s.prepareVotes.Add(msg)
}

func (s *roundState) PrepareVotes() []*bft.Message {
	return s.prepareVotes.Values()
}

func (s *roundState) PrepareVoteSize() int {
	return s.prepareVotes.Size()
}

func (s *roundState) AddPreCommitVote(msg *bft.Message) error {
	return s.preCommitVotes.Add(msg)
}

func (s *roundState) PreCommitVoteSize() int {
	return s.preCommitVotes.Size()
}

func (s *roundState) AddCommitVote(msg *bft.Message) error {
	return s.commitVotes.Add(msg)
}

func (s *roundState) CommitVoteSize() int {
	return s.commitVotes.Size()
}

func (s *roundState) SetHighQC(qc *bft.QuorumCert) {
	s.highQC = qc
}

func (s *roundState) HighQC() *bft.QuorumCert {
	return s.highQC
}

func (s *roundState) SetPrepareQC(qc *bft.QuorumCert) {
	s.prepareQC = qc
}

func (s *roundState) PrepareQC() *bft.QuorumCert {
	return s.prepareQC
}

func (s *roundState) SetPreCommittedQC(qc *bft.QuorumCert) {
	s.lockedQC = qc
}

func (s *roundState) PreCommittedQC() *bft.QuorumCert {
	return s.lockedQC
}

func (s *roundState) SetCommittedQC(qc *bft.QuorumCert) {
	s.committedQC = qc
}

func (s *roundState) CommittedQC() *bft.QuorumCert {
	return s.committedQC
}
