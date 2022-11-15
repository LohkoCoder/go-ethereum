package types

import (
	"bytes"
	"errors"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rlp"
	"io"
)

var (
	// BftDigest represents a hash of "Bft practical byzantine fault tolerance"
	// to identify whether the block is from Istanbul consensus engine
	BftDigest = common.HexToHash("0x63746963616c2062797a616e74696e65206661756c7420746f6c6572616e6365")

	BftExtraVanity = 32 // Fixed number of extra-data bytes reserved for validator vanity
	BftExtraSeal   = 65 // Fixed number of extra-data bytes reserved for validator seal

	// ErrInvalidBftHeaderExtra is returned if the length of extra-data is less than 32 bytes
	ErrInvalidBftHeaderExtra = errors.New("invalid istanbul header extra-data")
)

type BftExtra struct {
	Validators    []common.Address // consensus participants address for next epoch, and in the first block, it contains all genesis validators. keep empty if no epoch change.
	Seal          []byte           // proposer signature
	CommittedSeal [][]byte         // consensus participants signatures and it's size should be greater than 2/3 of validators
	Salt          []byte           // omit empty
}

// EncodeRLP serializes ist into the Ethereum RLP format.
func (ist *BftExtra) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, []interface{}{
		ist.Validators,
		ist.Seal,
		ist.CommittedSeal,
		ist.Salt,
	})
}

// DecodeRLP implements rlp.Decoder, and load the istanbul fields from a RLP stream.
func (ist *BftExtra) DecodeRLP(s *rlp.Stream) error {
	var extra struct {
		Validators    []common.Address
		Seal          []byte
		CommittedSeal [][]byte
		Salt          []byte
	}
	if err := s.Decode(&extra); err != nil {
		return err
	}
	ist.Validators, ist.Seal, ist.CommittedSeal, ist.Salt = extra.Validators, extra.Seal, extra.CommittedSeal, extra.Salt
	return nil
}

// ExtractBftExtra extracts all values of the bftExtra from the header. It returns an
// error if the length of the given extra-data is less than 32 bytes or the extra-data can not
// be decoded.
func ExtractBftExtra(h *Header) (*BftExtra, error) {
	return ExtractBftExtraPayload(h.Extra)
}

func ExtractBftExtraPayload(extra []byte) (*BftExtra, error) {
	if len(extra) < BftExtraVanity {
		return nil, ErrInvalidBftHeaderExtra
	}

	var bftExtra *BftExtra
	err := rlp.DecodeBytes(extra[BftExtraVanity:], &bftExtra)
	if err != nil {
		return nil, err
	}
	return bftExtra, nil
}

// bftFilteredHeader returns a filtered header which some information (like seal, committed seals)
// are clean to fulfill the Istanbul hash rules. It returns nil if the extra-data cannot be
// decoded/encoded by rlp.
func BftFilteredHeader(h *Header, keepSeal bool) *Header {
	newHeader := CopyHeader(h)
	extra, err := ExtractBftExtra(newHeader)
	if err != nil {
		return nil
	}

	if !keepSeal {
		extra.Seal = []byte{}
	}
	extra.CommittedSeal = [][]byte{}
	//extra.Salt = []byte{}

	payload, err := rlp.EncodeToBytes(&extra)
	if err != nil {
		return nil
	}

	newHeader.Extra = append(newHeader.Extra[:BftExtraVanity], payload...)

	return newHeader
}

func BftHeaderFillWithValidators(header *Header, vals []common.Address) error {
	var buf bytes.Buffer

	// compensate the lack bytes if header.Extra is not enough IstanbulExtraVanity bytes.
	if len(header.Extra) < BftExtraVanity {
		header.Extra = append(header.Extra, bytes.Repeat([]byte{0x00}, BftExtraVanity-len(header.Extra))...)
	}
	buf.Write(header.Extra[:BftExtraVanity])

	if vals == nil {
		vals = []common.Address{}
	}
	ist := &BftExtra{
		Validators:    vals,
		Seal:          []byte{},
		CommittedSeal: [][]byte{},
		Salt:          []byte{},
	}

	payload, err := rlp.EncodeToBytes(&ist)
	if err != nil {
		return err
	}
	header.Extra = append(buf.Bytes(), payload...)
	return nil
}

