package backend

import (
	"bytes"
	"io/ioutil"
	"math/big"
	"reflect"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/bft"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/p2p"
	lru "github.com/hashicorp/golang-lru"
)

const (
	NewBlockMsg = 0x07
	bftMsg = 0x11
)

func (s *backend) decode(msg p2p.Msg) ([]byte, common.Hash, error) {
	var data []byte
	if err := msg.Decode(&data); err != nil {
		return nil, common.Hash{}, errDecodeFailed
	}

	return data, bft.RLPHash(data), nil
}

// HandleMsg implements consensus.Handler.HandleMsg
func (s *backend) HandleMsg(addr common.Address, msg p2p.Msg) (bool, error) {
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	if msg.Code == bftMsg {
		if !s.coreStarted {
			return true, ErrStoppedEngine
		}

		data, hash, err := s.decode(msg)
		if err != nil {
			return true, errDecodeFailed
		}
		// Mark peer's message
		ms, ok := s.recentMessages.Get(addr)
		var m *lru.ARCCache
		if ok {
			m, _ = ms.(*lru.ARCCache)
		} else {
			m, _ = lru.NewARC(inmemoryMessages)
			s.recentMessages.Add(addr, m)
		}
		m.Add(hash, true)

		// Mark self known message
		if _, ok := s.knownMessages.Get(hash); ok {
			return true, nil
		}
		s.knownMessages.Add(hash, true)

		go s.eventMux.Post(bft.MessageEvent{
			Payload: data,
		})
		return true, nil
	}
	if msg.Code == NewBlockMsg && s.coreStarted && s.core.IsProposer() { // eth.NewBlockMsg: import cycle
		// this case is to safeguard the race of similar block which gets propagated from other node while this node is proposing
		// as p2p.Msg can only be decoded once (get EOF for any subsequence read), we need to make sure the payload is restored after we decode it
		s.logger.Debug("proposer received NewBlockMsg", "size", msg.Size, "payload.type", reflect.TypeOf(msg.Payload), "sender", addr)
		if reader, ok := msg.Payload.(*bytes.Reader); ok {
			payload, err := ioutil.ReadAll(reader)
			if err != nil {
				return true, err
			}
			reader.Reset(payload)       // ready to be decoded
			defer reader.Reset(payload) // restore so main eth/handler can decode
			var request struct {        // this has to be same as eth/protocol.go#newBlockData as we are reading NewBlockMsg
				Block *types.Block
				TD    *big.Int
			}
			if err := msg.Decode(&request); err != nil {
				s.logger.Debug("Proposer was unable to decode the NewBlockMsg", "error", err)
				return false, nil
			}
			newRequestedBlock := request.Block
			if newRequestedBlock.Header().MixDigest == types.BftDigest && s.core.IsCurrentProposal(newRequestedBlock.Hash()) {
				s.logger.Debug("Proposer already proposed this block", "hash", newRequestedBlock.Hash(), "sender", addr)
				return true, nil
			}
		}
	}
	return false, nil
}

// SetBroadcaster implements consensus.Handler.SetBroadcaster
func (s *backend) SetBroadcaster(broadcaster consensus.Broadcaster) {
	s.broadcaster = broadcaster
}

func (s *backend) NewChainHead(header *types.Header) error {
	s.coreMu.RLock()
	defer s.coreMu.RUnlock()
	if !s.coreStarted {
		return ErrStoppedEngine
	}
	go s.eventMux.Post(bft.FinalCommittedEvent{Header: header})
	return nil
}
