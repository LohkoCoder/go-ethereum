package backend

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
)

// API is a user facing RPC API to allow controlling the address and voting
// mechanisms of the HotStuff scheme.
type API struct {
	chain    consensus.ChainHeaderReader
	bft *backend
}

// Proposals returns the current proposals the node tries to uphold and vote on.
func (api *API) Proposals() map[common.Address]bool {
	api.bft.sigMu.RLock()
	defer api.bft.sigMu.RUnlock()

	proposals := make(map[common.Address]bool)
	for address, auth := range api.bft.proposals {
		proposals[address] = auth
	}
	return proposals
}

// todo: add/del candidate validators approach console or api
// Propose injects a new authorization candidate that the validator will attempt to
// push through.
func (api *API) Propose(address common.Address, auth bool) {
	api.bft.sigMu.Lock()
	defer api.bft.sigMu.Unlock()

	api.bft.proposals[address] = auth
}

// Discard drops a currently running candidate, stopping the validator from casting
// further votes (either for or against).
func (api *API) Discard(address common.Address) {
	api.bft.sigMu.Lock()
	defer api.bft.sigMu.Unlock()

	delete(api.bft.proposals, address)
}
