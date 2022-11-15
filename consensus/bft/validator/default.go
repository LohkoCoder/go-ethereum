package validator

import (
	"errors"
	"math"
	"reflect"
	"sort"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/bft"
)

var ErrInvalidParticipant = errors.New("invalid participants")

type defaultValidator struct {
	address common.Address
}

func (val *defaultValidator) Address() common.Address {
	return val.address
}

func (val *defaultValidator) String() string {
	return val.Address().String()
}

// ----------------------------------------------------------------------------

type defaultSet struct {
	validators bft.Validators
	policy     bft.SelectProposerPolicy

	proposer    bft.Validator
	validatorMu sync.RWMutex
	selector    bft.ProposalSelector
}

func newDefaultSet(addrs []common.Address, policy bft.SelectProposerPolicy) *defaultSet {
	valSet := &defaultSet{}

	valSet.policy = policy
	// init validators
	valSet.validators = make([]bft.Validator, len(addrs))
	for i, addr := range addrs {
		valSet.validators[i] = New(addr)
	}
	// sort validator
	sort.Sort(valSet.validators)
	// init proposer
	if valSet.Size() > 0 {
		valSet.proposer = valSet.GetByIndex(0)
	}
	valSet.selector = roundRobinSelector
	if policy == bft.Sticky {
		valSet.selector = stickySelector
	}
	if policy == bft.VRF {
		valSet.selector = vrfSelector
	}

	return valSet
}

func (valSet *defaultSet) Size() int {
	valSet.validatorMu.RLock()
	defer valSet.validatorMu.RUnlock()
	return len(valSet.validators)
}

func (valSet *defaultSet) List() []bft.Validator {
	valSet.validatorMu.RLock()
	defer valSet.validatorMu.RUnlock()
	return valSet.validators
}

func (valSet *defaultSet) AddressList() []common.Address {
	valSet.validatorMu.RLock()
	defer valSet.validatorMu.RUnlock()

	vals := make([]common.Address, valSet.Size())
	for i, v := range valSet.List() {
		vals[i] = v.Address()
	}
	return vals
}

func (valSet *defaultSet) GetByIndex(i uint64) bft.Validator {
	valSet.validatorMu.RLock()
	defer valSet.validatorMu.RUnlock()
	if i < uint64(valSet.Size()) {
		return valSet.validators[i]
	}
	return nil
}

func (valSet *defaultSet) GetByAddress(addr common.Address) (int, bft.Validator) {
	for i, val := range valSet.List() {
		if addr == val.Address() {
			return i, val
		}
	}
	return -1, nil
}

func (valSet *defaultSet) GetProposer() bft.Validator {
	return valSet.proposer
}

func (valSet *defaultSet) IsProposer(address common.Address) bool {
	_, val := valSet.GetByAddress(address)
	return reflect.DeepEqual(valSet.GetProposer(), val)
}

func (valSet *defaultSet) CalcProposer(lastProposer common.Address, round uint64) {
	valSet.validatorMu.RLock()
	defer valSet.validatorMu.RUnlock()
	valSet.proposer = valSet.selector(valSet, lastProposer, round)
}

func (valSet *defaultSet) CalcProposerByIndex(index uint64) {
	if index > 1 {
		index = (index - 1) % uint64(len(valSet.validators))
	} else {
		index = 0
	}
	valSet.proposer = valSet.validators[index]
}

func calcSeed(valSet bft.ValidatorSet, proposer common.Address, round uint64) uint64 {
	offset := 0
	if idx, val := valSet.GetByAddress(proposer); val != nil {
		offset = idx
	}
	return uint64(offset) + round
}

func emptyAddress(addr common.Address) bool {
	return addr == common.Address{}
}

func roundRobinSelector(valSet bft.ValidatorSet, proposer common.Address, round uint64) bft.Validator {
	if valSet.Size() == 0 {
		return nil
	}
	seed := uint64(0)
	if emptyAddress(proposer) {
		seed = round
	} else {
		seed = calcSeed(valSet, proposer, round) + 1
	}
	pick := seed % uint64(valSet.Size())
	return valSet.GetByIndex(pick)
}

func stickySelector(valSet bft.ValidatorSet, proposer common.Address, round uint64) bft.Validator {
	if valSet.Size() == 0 {
		return nil
	}
	seed := uint64(0)
	if emptyAddress(proposer) {
		seed = round
	} else {
		seed = calcSeed(valSet, proposer, round)
	}
	pick := seed % uint64(valSet.Size())
	return valSet.GetByIndex(pick)
}

// TODO: implement VRF
func vrfSelector(valSet bft.ValidatorSet, proposer common.Address, round uint64) bft.Validator {
	return nil
}

func (valSet *defaultSet) AddValidator(address common.Address) bool {
	valSet.validatorMu.Lock()
	defer valSet.validatorMu.Unlock()
	for _, v := range valSet.validators {
		if v.Address() == address {
			return false
		}
	}
	valSet.validators = append(valSet.validators, New(address))
	// TODO: we may not need to re-sort it again
	// sort validator
	sort.Sort(valSet.validators)
	return true
}

func (valSet *defaultSet) RemoveValidator(address common.Address) bool {
	valSet.validatorMu.Lock()
	defer valSet.validatorMu.Unlock()

	for i, v := range valSet.validators {
		if v.Address() == address {
			valSet.validators = append(valSet.validators[:i], valSet.validators[i+1:]...)
			return true
		}
	}
	return false
}

func (valSet *defaultSet) Copy() bft.ValidatorSet {
	valSet.validatorMu.RLock()
	defer valSet.validatorMu.RUnlock()

	addresses := make([]common.Address, 0, len(valSet.validators))
	for _, v := range valSet.validators {
		addresses = append(addresses, v.Address())
	}
	return NewSet(addresses, valSet.policy)
}

func (valSet *defaultSet) ParticipantsNumber(list []common.Address) int {
	if list == nil || len(list) == 0 {
		return 0
	}
	size := 0
	for _, v := range list {
		if index, _ := valSet.GetByAddress(v); index < 0 {
			continue
		} else {
			size += 1
		}
	}
	return size
}

func (valSet *defaultSet) CheckQuorum(committers []common.Address) error {
	validators := valSet.Copy()
	validSeal := 0
	for _, addr := range committers {
		if validators.RemoveValidator(addr) {
			validSeal++
			continue
		}
		return ErrInvalidParticipant
	}

	// The length of validSeal should be larger than number of faulty node + 1
	if validSeal <= validators.Q() {
		return ErrInvalidParticipant
	}
	return nil
}

func (valSet *defaultSet) F() int { return int(math.Ceil(float64(valSet.Size())/3)) - 1 }

func (valSet *defaultSet) Q() int { return int(math.Ceil(float64(2*valSet.Size()) / 3)) }

func (valSet *defaultSet) Policy() bft.SelectProposerPolicy { return valSet.policy }

func (valSet *defaultSet) Cmp(src bft.ValidatorSet) bool {
	n := valSet.ParticipantsNumber(src.AddressList())
	if n != valSet.Size() || n != src.Size() {
		return false
	}
	return true
}
