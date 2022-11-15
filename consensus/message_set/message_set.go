package message_set

import (
	"fmt"
	"math/big"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/bft"
)

// Construct a new message set to accumulate messages for given height/view number.
func NewMessageSet(valSet bft.ValidatorSet) *MessageSet {
	return &MessageSet{
		view: &bft.View{
			Round:  new(big.Int),
			Height: new(big.Int),
		},
		mtx:  new(sync.Mutex),
		msgs: make(map[common.Address]*bft.Message),
		vs:   valSet,
	}
}

type MessageSet struct {
	view *bft.View
	vs   bft.ValidatorSet
	mtx  *sync.Mutex
	msgs map[common.Address]*bft.Message
}

func (s *MessageSet) View() *bft.View {
	return s.view
}

func (s *MessageSet) Add(msg *bft.Message) error {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	if err := s.verify(msg); err != nil {
		return err
	}

	s.msgs[msg.Address] = msg
	return nil
}

func (s *MessageSet) Values() (result []*bft.Message) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	for _, v := range s.msgs {
		result = append(result, v)
	}
	return
}

func (s *MessageSet) Size() int {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	return len(s.msgs)
}

func (s *MessageSet) Get(addr common.Address) *bft.Message {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	return s.msgs[addr]
}

func (s *MessageSet) String() string {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	addresses := make([]string, 0, len(s.msgs))
	for _, v := range s.msgs {
		addresses = append(addresses, v.Address.Hex())
	}
	return fmt.Sprintf("[%v]", strings.Join(addresses, ", "))
}

// verify if the message comes from one of the validators
func (s *MessageSet) verify(msg *bft.Message) error {
	if _, v := s.vs.GetByAddress(msg.Address); v == nil {
		return fmt.Errorf("unauthorized address")
	}

	return nil
}
