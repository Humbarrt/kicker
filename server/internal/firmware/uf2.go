package firmware

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// UF2 (https://github.com/microsoft/uf2): 512-byte blocks, each carrying up to 476 bytes of
// flash content with its address. The RP2 boot drive takes the blocks written to any sector.
const (
	uf2Block    = 512
	uf2Magic0   = 0x0A324655
	uf2Magic1   = 0x9E5D5157
	uf2MagicEnd = 0x0AB16F30
	flagFamily  = 0x00002000
	flagNotMain = 0x00000001
)

// Family ids of the RP2 chips (picotool / pico-sdk).
const (
	FamilyRP2040      = 0xe48bff56
	FamilyAbsolute    = 0xe48bff57
	FamilyData        = 0xe48bff58
	FamilyRP2350ARMS  = 0xe48bff59
	FamilyRP2350RISCV = 0xe48bff5a
	FamilyRP2350ARMNS = 0xe48bff5b
)

// Chip names, as told by the boot drive's USB product id.
const (
	ChipRP2040 = "RP2040"
	ChipRP2350 = "RP2350"
)

// UF2 is a checked UF2 file.
type UF2 struct {
	Data   []byte
	Blocks int
	Family uint32 // of the flash blocks
	Size   int    // bytes of flash content
}

// ErrUF2 is a file that is not a valid UF2 for an RP2 chip.
var ErrUF2 = errors.New("not a valid .uf2 file")

// ParseUF2 checks every block: the magic numbers, numbering, payload size and family.
func ParseUF2(data []byte) (UF2, error) {
	bad := func(f string, a ...any) (UF2, error) {
		return UF2{}, fmt.Errorf("%w: "+f, append([]any{ErrUF2}, a...)...)
	}
	if len(data) == 0 || len(data)%uf2Block != 0 {
		return bad("the size is not a multiple of 512 bytes")
	}
	u := UF2{Data: data, Blocks: len(data) / uf2Block}
	var total uint32
	for i := 0; i < u.Blocks; i++ {
		b := data[i*uf2Block : (i+1)*uf2Block]
		le := binary.LittleEndian
		if le.Uint32(b[0:]) != uf2Magic0 || le.Uint32(b[4:]) != uf2Magic1 || le.Uint32(b[508:]) != uf2MagicEnd {
			return bad("block %d: wrong magic numbers", i)
		}
		flags, size, no, num := le.Uint32(b[8:]), le.Uint32(b[16:]), le.Uint32(b[20:]), le.Uint32(b[24:])
		if size == 0 || size > 476 {
			return bad("block %d: payload size %d", i, size)
		}
		if flags&flagFamily == 0 {
			return bad("block %d: no family id", i)
		}
		fam := le.Uint32(b[28:])
		if flags&flagNotMain != 0 || fam == FamilyAbsolute || fam == FamilyData {
			continue // extra blocks (e.g. the RP2350-E10 workaround); not counted for the family
		}
		if total == 0 {
			total = num
			u.Family = fam
		}
		if num != total || no >= num {
			return bad("block %d: number %d of %d", i, no, num)
		}
		if fam != u.Family {
			return bad("block %d: mixed chip families", i)
		}
		u.Size += int(size)
	}
	if u.Family == 0 {
		return bad("no flash blocks")
	}
	return u, nil
}

// Chip is the chip this file is built for.
func (u UF2) Chip() string {
	switch u.Family {
	case FamilyRP2040:
		return ChipRP2040
	case FamilyRP2350ARMS, FamilyRP2350RISCV, FamilyRP2350ARMNS:
		return ChipRP2350
	}
	return fmt.Sprintf("family %#x", u.Family)
}
