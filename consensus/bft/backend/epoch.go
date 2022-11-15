package backend

import (
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/bft"
	"github.com/ethereum/go-ethereum/consensus/bft/validator"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
)

func init() {
	core.StoreGenesis = func(db ethdb.Database, header *types.Header) error {
		extra, err := types.ExtractBftExtra(header)
		if err != nil {
			return err
		}
		epoch := &Epoch{
			StartHeight:          0,
			ValSet:               newValSet(extra.Validators),
			LastEpochStartHeight: 0,
		}
		return storeCurEpoch(db, epoch)
	}
}

func (s *backend) Validators(height uint64) bft.ValidatorSet {
	startHeight := s.maxEpochStartHeight
	for height < startHeight {
		epoch := s.epochs[startHeight]
		if height >= epoch.StartHeight {
			return s.epochs[epoch.StartHeight].ValSet.Copy()
		} else {
			startHeight = epoch.LastEpochStartHeight
		}
	}
	return s.epochs[startHeight].ValSet.Copy()
}

func (s *backend) LoadEpoch() error {
	if s.epochs == nil {
		s.epochs = make(map[uint64]*Epoch)
	}
	epoch, err := getCurEpoch(s.db)
	if err != nil {
		return err
	}
	s.maxEpochStartHeight = epoch.StartHeight
	s.epochs[s.maxEpochStartHeight] = epoch

	log.Info("[epoch]", "load current epoch", epoch.String())
	startHeight := epoch.LastEpochStartHeight
	for startHeight > 0 {
		ep, err := s.readEpoch(startHeight)
		if err != nil {
			return err
		}
		startHeight = ep.LastEpochStartHeight
	}
	if startHeight == 0 {
		if _, err := s.readEpoch(0); err != nil {
			return err
		}
	}
	return nil
}

func (s *backend) UpdateEpoch(parent, header *types.Header) error {
	height := header.Number.Uint64()
	if height <= s.maxEpochStartHeight || height == 1 {
		return nil
	}

	parentExt, err := types.ExtractBftExtra(parent)
	if err != nil {
		return err
	}

	if parentExt.Validators == nil || len(parentExt.Validators) == 0 {
		return nil
	}
	return s.saveEpoch(height, parentExt.Validators)
}

func (s *backend) ChangeEpoch(height uint64, list []common.Address) error {
	return s.saveEpoch(height, list)
}

func (s *backend) DumpEpochs() string {
	str := ""
	for _, v := range s.epochs {
		str += v.String() + "\r\n"
	}
	return str
}

func (s *backend) saveEpoch(height uint64, list []common.Address) error {
	if _, ok := s.epochs[height]; ok {
		return nil
	}
	if s.maxEpochStartHeight == height {
		log.Warn("[epoch]", "dump epoch", "epoch should be persisted before", "max epoch height", s.maxEpochStartHeight)
		return nil
	}

	epoch := &Epoch{
		StartHeight:          height,
		ValSet:               newValSet(list),
		LastEpochStartHeight: s.maxEpochStartHeight,
	}
	if err := storeCurEpoch(s.db, epoch); err != nil {
		return err
	}
	s.epochs[height] = epoch
	s.maxEpochStartHeight = height

	log.Info("[epoch]", "save epoch", epoch.String())
	return nil
}

func (s *backend) readEpoch(height uint64) (*Epoch, error) {
	epoch, err := getEpochByHeight(s.db, height)
	if err != nil {
		log.Warn("[epoch]", "read epoch failed", err)
		return nil, err
	}
	s.epochs[epoch.StartHeight] = epoch
	if epoch.StartHeight > s.maxEpochStartHeight {
		s.maxEpochStartHeight = epoch.StartHeight
	}
	log.Info("[epoch]", "read epoch", epoch.String())
	return epoch, nil
}

func storeCurEpoch(db ethdb.Database, epoch *Epoch) error {
	blob, err := epoch.MarshalJSON()
	if err != nil {
		return err
	}
	// ToDo rawdb 部分需要单独存储Epoch信息
	if err := rawdb.WriteEpoch(db, epoch.StartHeight, blob); err != nil {
		return err
	}
	return rawdb.WriteCurrentEpochHeight(db, epoch.StartHeight)
}

func getCurEpoch(db ethdb.Database) (*Epoch, error) {
	// ToDo rawdb 部分需要单独存储Epoch信息
	height, err := rawdb.ReadCurrentEpochHeight(db)
	if err != nil {
		return nil, err
	}
	return getEpochByHeight(db, height)
}

func getEpochByHeight(db ethdb.Database, height uint64) (*Epoch, error) {
	// ToDo rawdb 部分需要单独存储Epoch信息
	blob, err := rawdb.ReadEpoch(db, height)
	if err != nil {
		return nil, err
	}
	epoch := new(Epoch)
	if err := epoch.UnmarshalJSON(blob); err != nil {
		return nil, err
	}
	return epoch, nil
}

type Epoch struct {
	StartHeight          uint64
	ValSet               bft.ValidatorSet
	LastEpochStartHeight uint64
}

func (e *Epoch) Copy() *Epoch {
	return &Epoch{
		StartHeight:          e.StartHeight,
		ValSet:               e.ValSet.Copy(),
		LastEpochStartHeight: e.LastEpochStartHeight,
	}
}

func (e *Epoch) String() string {
	return fmt.Sprintf("{StartHeight: %d, LastStartHeight: %d, Valset: %v, Size: %d}",
		e.StartHeight, e.LastEpochStartHeight, e.ValSet.AddressList(), e.ValSet.Size())
}

type epochJSON struct {
	StartHeight          uint64           `json:"start_height"`
	Validators           []common.Address `json:"validators"`
	LastEpochStartHeight uint64           `json:"last_epoch_start_height"`
}

func (e *Epoch) toJSONStruct() *epochJSON {
	return &epochJSON{
		StartHeight:          e.StartHeight,
		Validators:           e.ValSet.AddressList(),
		LastEpochStartHeight: e.LastEpochStartHeight,
	}
}

// Unmarshal from a json byte array
func (e *Epoch) UnmarshalJSON(b []byte) error {
	var j epochJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}

	e.StartHeight = j.StartHeight
	e.ValSet = newValSet(j.Validators)
	e.LastEpochStartHeight = j.LastEpochStartHeight
	return nil
}

// Marshal to a json byte array
func (e *Epoch) MarshalJSON() ([]byte, error) {
	j := e.toJSONStruct()
	return json.Marshal(j)
}

func newValSet(list []common.Address) bft.ValidatorSet {
	return validator.NewSet(list, bft.RoundRobin)
}
