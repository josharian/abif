// package abif provides an ABIF file reader.
//
// No attempt is made at being either complete or strict.
//
// The API is not settled; see some of the TODOs and comments.
//
// The spec is available at http://www6.appliedbiosystems.com/support/software_community/ABIF_File_Format.pdf
package abif

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"time"
)

type Reader struct {
	src  io.ReadSeeker
	refs map[Tag]ref
}

// A Tag is a key used to look up data.
type Tag struct {
	// TODO: It'd be more convenient (API-wise) to use a string for Name.
	// It's a little less efficient, and it opens up the possibility for error
	// (non-length-4 strings), but it is probably worth doing anyway.
	// If we do that, we might want to change NewTag to be Parse and have it
	// accept Name:Num, or just accept Name:Num directly.
	Name [4]byte
	Num  int32
}

func (t Tag) String() string {
	return fmt.Sprintf("%s:%d", t.Name, t.Num)
}

func NewTag(name string, num int32) Tag {
	if len(name) != 4 {
		panic("tag name must have len 4")
	}
	var t Tag
	copy(t.Name[:], []byte(name))
	t.Num = num
	return t
}

// ref is a reference to data.
// For convenience, we use a single representation
// for both parsing and in-memory storage, even though
// this is slightly wasteful.
type ref struct {
	ElemType int16 // for strings, an element is a byte, not the string itself
	_        int16 // ElemSize, unused
	NElem    int32
	DataSize int32
	Data     [4]byte // the data itself if DataSize <= 4, otherwise offset to data in file, interpreted as BigEndian int32
	_        int32   // DataHandle, unused
}

func (r ref) dataOffset() int64 { return int64(int32(binary.BigEndian.Uint32(r.Data[:]))) }

// entry is a directory entry.
type entry struct {
	Tag Tag
	Ref ref
}

func NewReader(src io.ReadSeeker) (*Reader, error) {
	r := Reader{
		src: src,
	}

	// seek to beginning
	if _, err := src.Seek(0, 0); err != nil {
		return nil, err
	}
	// read and parse header
	var header struct {
		Magic   [4]byte
		Version uint16
		Dir     entry
	}
	if err := binary.Read(src, binary.BigEndian, &header); err != nil {
		return nil, err
	}
	// validate header
	switch {
	case string(header.Magic[:]) != "ABIF":
		return nil, fmt.Errorf("bad header magic %v", header.Magic)
	case header.Version/100 != 1:
		return nil, fmt.Errorf("unknown version %d", header.Version)
	}

	// seek and read the directory
	if _, err := src.Seek(header.Dir.Ref.dataOffset(), 0); err != nil {
		return nil, err
	}

	entries := make([]entry, header.Dir.Ref.NElem)
	if err := binary.Read(src, binary.BigEndian, &entries); err != nil {
		return nil, err
	}

	r.refs = make(map[Tag]ref, len(entries))
	for _, e := range entries {
		r.refs[e.Tag] = e.Ref
	}

	return &r, nil
}

// Tags returns a list of the tags available in the file.
func (r *Reader) Tags() []Tag {
	tags := make([]Tag, 0, len(r.refs))
	for t := range r.refs {
		tags = append(tags, t)
	}
	return tags
}

type (
	errNotFound Tag
	errBadValue Tag
	errBadType  struct {
		Tag
		ref
	}
)

func (t errNotFound) Error() string { return fmt.Sprintf("tag not found: %s", Tag(t)) }
func (t errBadValue) Error() string { return fmt.Sprintf("malformed value for tag: %s", Tag(t)) }
func (e errBadType) Error() string {
	return fmt.Sprintf("unknown value type for tag %s: %d", e.Tag, e.ref.ElemType)
}

// TODO: The Value API could cause lots of seeking; on a large file, this could be bad for performance.
// Consider offering an "AllValues()" or "Values(tt []Tag)" API that does sequential file reads.

// Value reads the value identified by t.
func (r *Reader) Value(t Tag) (interface{}, error) {
	x, ok := r.refs[t]
	if !ok {
		return nil, errNotFound(t)
	}

	var data []byte
	if x.DataSize <= 4 {
		data = x.Data[:x.DataSize]
	} else {
		if _, err := r.src.Seek(x.dataOffset(), 0); err != nil {
			return nil, err
		}
		data = make([]byte, x.DataSize)
		if _, err := io.ReadFull(r.src, data); err != nil {
			return nil, err
		}
	}

	if x.ElemType >= 1024 {
		// user-defined data structure; return as a slice
		return data, nil
	}

	if int(x.ElemType) >= len(dataTypes) {
		return nil, errBadType{Tag: t, ref: x}
	}
	typ := dataTypes[x.ElemType]

	if typ.size == 0 { // missing dataTypes element
		return nil, errBadType{Tag: t, ref: x}
	}
	if x.NElem < 1 {
		return nil, errBadValue(t)
	}
	if int(x.NElem)*typ.size > len(data) {
		return nil, errBadValue(t)
	}

	if x.NElem == 1 {
		return typ.one(data), nil
	}
	return typ.many(int(x.NElem), data), nil
}

var dataTypes = [...]struct {
	name string // debugging and documentation use only for now; the name and comment are taken verbatim from the spec
	size int
	one  func([]byte) interface{}
	many func(int, []byte) interface{}
}{
	// Current data types
	1: {
		name: "byte", // Unsigned 8-bit integer.
		size: 1,
		one:  func(b []byte) interface{} { return b[0] },
		many: func(n int, b []byte) interface{} {
			x := make([]byte, n)
			copy(x, b[:n])
			return x
		},
	},
	2: {
		name: "char", // 8-bit ASCII character or signed 8-bit integer
		size: 1,
		one:  func(b []byte) interface{} { return int8(b[0]) },
		many: func(n int, b []byte) interface{} {
			x := make([]int8, n)
			for i := range x {
				x[i] = int8(b[i])
			}
			return x
		},
	},
	3: {
		name: "word", // Unsigned 16-bit integer.
		size: 2,
		one:  func(b []byte) interface{} { return binary.BigEndian.Uint16(b[:2]) },
		many: func(n int, b []byte) interface{} {
			x := make([]uint16, n)
			for i := range x {
				x[i] = binary.BigEndian.Uint16(b[i*2 : i*2+2])
			}
			return x
		},
	},
	4: {
		name: "short", // Signed 16-bit integer.
		size: 2,
		one:  func(b []byte) interface{} { return int16(binary.BigEndian.Uint16(b[:2])) },
		many: func(n int, b []byte) interface{} {
			x := make([]int16, n)
			for i := range x {
				x[i] = int16(binary.BigEndian.Uint16(b[i*2 : i*2+2]))
			}
			return x
		},
	},
	5: {
		name: "long", // Signed 32-bit integer.
		size: 4,
		one:  func(b []byte) interface{} { return int32(binary.BigEndian.Uint32(b[:4])) },
		many: func(n int, b []byte) interface{} {
			x := make([]int32, n)
			for i := range x {
				x[i] = int32(binary.BigEndian.Uint32(b[i*4 : i*4+4]))
			}
			return x
		},
	},
	7: {
		name: "float", // 32-bit floating point value.
		size: 4,
		one:  func(b []byte) interface{} { return math.Float32frombits(binary.BigEndian.Uint32(b[:4])) },
		many: func(n int, b []byte) interface{} {
			x := make([]float32, n)
			for i := range x {
				x[i] = math.Float32frombits(binary.BigEndian.Uint32(b[i*4 : i*4+4]))
			}
			return x
		},
	},
	8: {
		name: "double", // 64-bit floating point value.
		size: 8,
		one:  func(b []byte) interface{} { return math.Float64frombits(binary.BigEndian.Uint64(b[:8])) },
		many: func(n int, b []byte) interface{} {
			x := make([]float64, n)
			for i := range x {
				x[i] = math.Float64frombits(binary.BigEndian.Uint64(b[i*8 : i*8+8]))
			}
			return x
		},
	},
	10: {
		name: "date",
		size: 4,
		one:  func(b []byte) interface{} { return parseDate(b) },
		many: func(n int, b []byte) interface{} {
			x := make([]time.Time, n)
			for i := range x {
				x[i] = parseDate(b[i*4 : i*4+4])
			}
			return x
		},
	},
	11: {
		name: "time",
		size: 4,
		one:  func(b []byte) interface{} { return parseTime(b) },
		many: func(n int, b []byte) interface{} {
			x := make([]time.Time, n)
			for i := range x {
				x[i] = parseTime(b[i*4 : i*4+4])
			}
			return x
		},
	},
	18: {
		name: "pString", // Pascal string, consisting of a character count (from 0 to 255) in the first byte followed by the 8-bit ASCII characters.
		size: 1,
		one: func(b []byte) interface{} {
			if b[0] != 0 {
				panic("bad pString len")
			}
			return ""
		},
		many: func(n int, b []byte) interface{} {
			if int(b[0]) != len(b)-1 {
				panic("bad pString len")
			}
			return string(b[1:])
		},
	},
	19: {
		name: "cString", // C-style string, consisting of a string of 8-bit ASCII characters followed by a null (zero) byte.
		size: 1,
		one: func(b []byte) interface{} {
			if b[0] != 0 {
				panic("bad cString terminator")
			}
			return ""
		},
		many: func(n int, b []byte) interface{} {
			var i int
			for i < len(b) && b[i] != 0 {
				i++
			}
			if i == len(b) {
				panic("bad cString terminator")
			}
			return string(b[:i])
		},
	},

	// Supported legacy data types
	12: {
		name: "thumb",
		size: 10,
		one:  func(b []byte) interface{} { return parseThumb(b) },
		many: func(n int, b []byte) interface{} {
			x := make([]Thumb, n)
			for i := range x {
				x[i] = parseThumb(b[i*10 : i*10+10])
			}
			return x
		},
	},
}

// TODO: publicly document date and time value handling

func parseDate(b []byte) time.Time {
	// Packed structure to represent calendar date:
	// {
	//   SInt16 year; // 4-digit year
	//   UInt8 month; // month 1-12
	//   UInt8 day; // day 1-31
	// }
	y := int16(binary.BigEndian.Uint16(b))
	m := b[2]
	d := b[3]
	return time.Date(int(y), time.Month(m), int(d), 0, 0, 0, 0, time.UTC)
}

func parseTime(b []byte) time.Time {
	// Packed structure to represent time of day:
	// {
	//   UInt8 hour; // hour 0-23
	//   UInt8 minute; // minute 0-59
	//   UInt8 second; // second 0-59
	//   UInt8 hsecond; // 0.01 second 0-99
	// }
	h := b[0]
	m := b[1]
	s := b[2]
	hs := b[3]
	const centisecond = int(time.Millisecond * 100)
	return time.Date(0, 0, 0, int(h), int(m), int(s), int(hs)*centisecond, time.UTC)
}

// A Thumb is an ABIF thumbprint.
// According to the spec:
//
// The "thumbprint" structure was intended to provide a unique file identifier
// that could be generated on a local (non-networked) computer and
// yet would be highly likely to be different from
// any other thumbprint structure generated on any other computer.
type Thumb struct {
	D int32
	U int32
	C uint8
	N uint8
}

func parseThumb(b []byte) Thumb {
	return Thumb{
		D: int32(binary.BigEndian.Uint32(b)),
		U: int32(binary.BigEndian.Uint32(b[4:])),
		C: b[8],
		N: b[9],
	}
}
