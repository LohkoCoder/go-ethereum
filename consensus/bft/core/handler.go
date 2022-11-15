package core

import (
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/bft"
)

var once sync.Once

// Start implements core.Engine.Start
func (c *core) Start(chain consensus.ChainReader) error {
	once.Do(func() {
		bft.RegisterMsgTypeConvertHandler(func(data interface{}) bft.MsgType {
			code := data.(uint64)
			return MsgType(code)
		})
	})

	c.isRunning = true
	c.requests = newRequestSet()
	c.backlogs = newBackLog()
	c.current = nil

	// Start a new round from last sequence + 1
	c.startNewRound(common.Big0)

	// Tests will handle events itself, so we have to make subscribeEvents()
	// be able to call in test.
	c.subscribeEvents()
	go c.handleEvents()
	return nil
}

// Stop implements core.Engine.Stop
func (c *core) Stop() error {
	c.stopTimer()
	c.unsubscribeEvents()
	c.isRunning = false
	return nil
}

// ----------------------------------------------------------------------------

// Subscribe both internal and external events
func (c *core) subscribeEvents() {
	c.events = c.backend.EventMux().Subscribe(
		// external events
		bft.RequestEvent{},
		bft.MessageEvent{},
		// internal events
		backlogEvent{},
	)
	c.timeoutSub = c.backend.EventMux().Subscribe(
		timeoutEvent{},
	)
	c.finalCommittedSub = c.backend.EventMux().Subscribe(
		bft.FinalCommittedEvent{},
	)
}

// Unsubscribe all events
func (c *core) unsubscribeEvents() {
	c.events.Unsubscribe()
	c.timeoutSub.Unsubscribe()
	c.finalCommittedSub.Unsubscribe()
}

func (c *core) handleEvents() {
	logger := c.logger.New("handleEvents", "state", c.currentState())

	for {
		select {
		case event, ok := <-c.events.Chan():
			if !ok {
				logger.Error("Failed to receive msg Event")
				return
			}
			// A real Event arrived, process interesting content
			switch ev := event.Data.(type) {
			case bft.RequestEvent:
				c.handleRequest(&bft.Request{Proposal: ev.Proposal})

			case bft.MessageEvent:
				c.handleMsg(ev.Payload)

			case backlogEvent:
				c.handleCheckedMsg(ev.msg, ev.src)
			}

		case _, ok := <-c.timeoutSub.Chan():
			//logger.Trace("handle timeout Event")
			if !ok {
				logger.Error("Failed to receive timeout Event")
				return
			}
			c.handleTimeoutMsg()

		case evt, ok := <-c.finalCommittedSub.Chan():
			if !ok {
				logger.Error("Failed to receive finalCommitted Event")
				return
			}
			switch ev := evt.Data.(type) {
			case bft.FinalCommittedEvent:
				c.handleFinalCommitted(ev.Header)
			}
		}
	}
}

// sendEvent sends events to mux
func (c *core) sendEvent(ev interface{}) {
	c.backend.EventMux().Post(ev)
}

func (c *core) handleMsg(payload []byte) error {
	logger := c.logger.New()

	// Decode Message and check its signature
	msg := new(bft.Message)
	if err := msg.FromPayload(payload, c.validateFn); err != nil {
		logger.Error("Failed to decode Message from payload", "err", err)
		return err
	}

	// Only accept Message if the address is valid
	_, src := c.valSet.GetByAddress(msg.Address)
	if src == nil {
		logger.Error("Invalid address in Message", "msg", msg)
		return errInvalidSigner
	}

	// handle checked Message
	if err := c.handleCheckedMsg(msg, src); err != nil {
		return err
	}
	return nil
}

func (c *core) handleCheckedMsg(msg *bft.Message, src bft.Validator) (err error) {
	switch msg.Code {
	case MsgTypeNewView:
		err = c.handleNewView(msg, src)
	case MsgTypePrepare:
		err = c.handlePrepare(msg, src)
	case MsgTypePrepareVote:
		err = c.handlePrepareVote(msg, src)
	case MsgTypePreCommit:
		err = c.handlePreCommit(msg, src)
	case MsgTypePreCommitVote:
		err = c.handlePreCommitVote(msg, src)
	case MsgTypeCommit:
		err = c.handleCommit(msg, src)
	case MsgTypeCommitVote:
		err = c.handleCommitVote(msg, src)
	default:
		err = errInvalidMessage
		c.logger.Error("msg type invalid", "unknown type", msg.Code)
	}

	if err == errFutureMessage {
		c.storeBacklog(msg, src)
	}
	return
}

func (c *core) handleTimeoutMsg() {
	c.logger.Trace("handleTimeout", "state", c.currentState(), "view", c.currentView())
	round := new(big.Int).Add(c.current.Round(), common.Big1)
	c.startNewRound(round)
}
